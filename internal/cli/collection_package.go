package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/jsonc"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
	"github.com/devcontainers/cli/internal/pfs"
	"github.com/iancoleman/orderedmap"
)

// packageCollection packages the Features or Templates at targetFolder into
// outputDir, as TS prepPackageCommand + doFeaturesPackageCommand/packageTemplates
// do: a folder holding devcontainer-<type>.json is a single item, any other is a
// collection whose sub-folders are the items. Each item is copied, its metadata
// completed (currentId, or a Template's type/files/fileCount/featureIds), and
// tarred; devcontainer-collection.json lists their metadata.
func packageCollection(targetFolder, outputDir, collectionType string, forceClean bool, logLevelStr string) error {
	// TS parity: --log-level is a yargs choice; reject out-of-range instead of
	// silently defaulting to Info (collectionCommonUtils/package.ts:18).
	if err := validateEnum("log-level", logLevelStr, logLevels); err != nil {
		return err
	}
	logger := log.New(log.Options{Level: log.ParseLevel(logLevelStr), Format: "text", Writer: os.Stderr})

	if !pfs.IsDir(targetFolder) {
		return fmt.Errorf("Target folder '%s' does not exist", targetFolder)
	}
	if pfs.IsDir(outputDir) {
		if !forceClean {
			logger.Write(fmt.Sprintf("(!) ERR: Output directory '%s' already exists. Manually delete, or pass '-f' to continue.", outputDir), log.LevelError)
			return &coreerrors.ExitCodeError{Code: 1}
		}
		if err := os.RemoveAll(outputDir); err != nil {
			return fmt.Errorf("clean output directory: %w", err)
		}
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	var metadata []*orderedmap.OrderedMap
	if pfs.IsFile(filepath.Join(targetFolder, "devcontainer-"+collectionType+".json")) {
		logger.Write(fmt.Sprintf("Packaging single %s...", collectionType), log.LevelInfo)
		metadata = packageSingleItem(logger, targetFolder, outputDir, collectionType)
	} else {
		logger.Write(fmt.Sprintf("Packaging %s collection...", collectionType), log.LevelInfo)
		metadata = packageCollectionItems(logger, targetFolder, outputDir, collectionType)
	}
	if metadata == nil {
		logger.Write(fmt.Sprintf("Failed to package %ss", collectionType), log.LevelError)
		return &coreerrors.ExitCodeError{Code: 1}
	}
	return writeCollectionMetadata(outputDir, collectionType, metadata)
}

// packageSingleItem is TS packageSingleFeatureOrTemplate: the archive is named
// by the item's id. nil means the item could not be packaged (reported).
func packageSingleItem(logger log.Logger, targetFolder, outputDir, collectionType string) []*orderedmap.OrderedMap {
	metadataFile := "devcontainer-" + collectionType + ".json"
	tmp, err := copyToTemp(targetFolder)
	if err != nil {
		logger.Write(fmt.Sprintf("(!) ERR: %v", err), log.LevelError)
		return nil
	}
	defer os.RemoveAll(tmp)

	jsonPath := filepath.Join(tmp, metadataFile)
	if !completeItemMetadata(logger, tmp, jsonPath, collectionType) {
		return nil
	}
	meta, err := readOrderedJSONC(jsonPath)
	if err != nil || !jsTruthy(meta, "id") || !jsTruthy(meta, "version") || !jsTruthy(meta, "name") {
		logger.Write(fmt.Sprintf("%s is missing one of the following required properties in its %s: 'id', 'version', 'name'.", collectionType, metadataFile), log.LevelError)
		return nil
	}
	id, _ := meta.Get("id")
	archiveName := fmt.Sprintf("devcontainer-%s-%v.tgz", collectionType, id)
	if err := createTarArchive(filepath.Join(outputDir, archiveName), tmp); err != nil {
		logger.Write(fmt.Sprintf("(!) ERR: package %v: %v", id, err), log.LevelError)
		return nil
	}
	logger.Write(fmt.Sprintf("Packaged %s '%v'", collectionType, id), log.LevelInfo)
	return []*orderedmap.OrderedMap{meta}
}

// packageCollectionItems is TS packageCollection: every entry of the folder, in
// name order, except those starting with '.'; one without a metadata file is
// skipped with a warning, while a Feature without install.sh, a Template whose
// metadata cannot be completed, or an item missing id/version/name fails the
// whole package. nil means nothing was packaged.
func packageCollectionItems(logger log.Logger, srcFolder, outputDir, collectionType string) []*orderedmap.OrderedMap {
	metadataFile := "devcontainer-" + collectionType + ".json"
	entries, err := os.ReadDir(srcFolder)
	if err != nil {
		logger.Write(fmt.Sprintf("(!) ERR: %v", err), log.LevelError)
		return nil
	}
	var metadata []*orderedmap.OrderedMap
	for _, entry := range entries {
		c := entry.Name()
		logger.Write(fmt.Sprintf("Processing %s: %s...", collectionType, c), log.LevelInfo)
		if strings.HasPrefix(c, ".") {
			continue
		}
		folder := filepath.Join(srcFolder, c)
		if !pfs.IsFile(filepath.Join(folder, metadataFile)) {
			logger.Write(fmt.Sprintf("(!) WARNING: %s '%s' is missing a %s. Skipping... ", collectionType, c, metadataFile), log.LevelWarning)
			continue
		}
		meta, ok := packageCollectionItem(logger, folder, c, outputDir, collectionType)
		if !ok {
			return nil
		}
		metadata = append(metadata, meta)
	}
	if len(metadata) == 0 {
		return nil
	}
	logger.Write(fmt.Sprintf("Packaged %d %ss!", len(metadata), collectionType), log.LevelInfo)
	return metadata
}

func packageCollectionItem(logger log.Logger, folder, name, outputDir, collectionType string) (*orderedmap.OrderedMap, bool) {
	metadataFile := "devcontainer-" + collectionType + ".json"
	tmp, err := copyToTemp(folder)
	if err != nil {
		logger.Write(fmt.Sprintf("(!) ERR: %v", err), log.LevelError)
		return nil, false
	}
	defer os.RemoveAll(tmp)

	if collectionType == "feature" && !pfs.IsFile(filepath.Join(tmp, "install.sh")) {
		logger.Write(fmt.Sprintf("Feature '%s' is missing an install.sh", name), log.LevelError)
		return nil, false
	}
	jsonPath := filepath.Join(tmp, metadataFile)
	if !completeItemMetadata(logger, tmp, jsonPath, collectionType) {
		return nil, false
	}
	if err := createTarArchive(filepath.Join(outputDir, fmt.Sprintf("devcontainer-%s-%s.tgz", collectionType, name)), tmp); err != nil {
		logger.Write(fmt.Sprintf("(!) ERR: package %s: %v", name, err), log.LevelError)
		return nil, false
	}
	meta, err := readOrderedJSONC(jsonPath)
	if err != nil || !jsTruthy(meta, "id") || !jsTruthy(meta, "version") || !jsTruthy(meta, "name") {
		logger.Write(fmt.Sprintf("%s '%s' is missing one of the following required properties in its %s: 'id', 'version', 'name'.", collectionType, name, metadataFile), log.LevelError)
		return nil, false
	}
	return meta, true
}

// completeItemMetadata rewrites the copied item's metadata file as TS does
// before tarring it: a Feature with legacyIds gets currentId; a Template gets
// type, files, fileCount and featureIds.
func completeItemMetadata(logger log.Logger, srcFolder, jsonPath, collectionType string) bool {
	switch collectionType {
	case "feature":
		addFeatureCurrentID(logger, jsonPath)
		return true
	case "template":
		return addTemplateProps(logger, srcFolder, jsonPath)
	}
	return true
}

// addFeatureCurrentID is TS addsAdditionalFeatureProps: a Feature that lists
// legacyIds records its id as currentId, so a reference by an old id resolves
// to the renamed Feature.
func addFeatureCurrentID(logger log.Logger, jsonPath string) {
	meta, err := readOrderedJSONC(jsonPath)
	if err != nil {
		return
	}
	legacy, _ := meta.Get("legacyIds")
	if ids, ok := legacy.([]any); !ok || len(ids) == 0 {
		return
	}
	id, _ := meta.Get("id")
	meta.Set("currentId", id)
	logger.Write(fmt.Sprintf("Programmatically adding currentId:%v...", id), log.LevelTrace)
	writeJSStringified(jsonPath, meta)
}

// addTemplateProps is TS addsAdditionalTemplateProps: it records the Template's
// type (from its devcontainer.json), its files and their count, and the
// resources of the Features it configures, and narrows an optionalPaths "dir/*"
// whose folder holds a single file to that file.
func addTemplateProps(logger log.Logger, srcFolder, jsonPath string) bool {
	devcontainerPath := filepath.Join(srcFolder, ".devcontainer.json")
	if !pfs.IsFile(devcontainerPath) {
		devcontainerPath = filepath.Join(srcFolder, ".devcontainer", "devcontainer.json")
	}
	if !pfs.IsFile(devcontainerPath) {
		logger.Write("Template is missing a devcontainer.json", log.LevelError)
		return false
	}
	var config map[string]any
	if data, err := os.ReadFile(devcontainerPath); err == nil {
		_ = jsonc.Unmarshal(data, &config)
	}
	var templateType string
	build, _ := config["build"].(map[string]any)
	_, hasBuild := build["dockerfile"]
	switch {
	case hasKey(config, "image"):
		templateType = "image"
	case hasKey(config, "dockerFile") || hasBuild:
		templateType = "dockerfile"
	case hasKey(config, "dockerComposeFile"):
		templateType = "dockerCompose"
	default:
		logger.Write(fmt.Sprintf("Dev container config (%s) is missing one of \"image\", \"dockerFile\" or \"dockerComposeFile\" properties.", devcontainerPath), log.LevelError)
		return false
	}

	files := templateFiles(srcFolder, "")

	data, err := readOrderedJSONC(jsonPath)
	if err != nil {
		data = orderedmap.New()
	}
	featureIDs := []any{}
	if feats, ok := config["features"].(map[string]any); ok {
		for _, id := range jsonc.MemberNames(mustRead(devcontainerPath), "features") {
			if _, ok := feats[id]; !ok {
				continue
			}
			if ref, err := oci.ParseRef(id); err == nil {
				featureIDs = append(featureIDs, ref.Resource)
			}
		}
	}
	data.Set("type", templateType)
	data.Set("files", files)
	data.Set("fileCount", len(files))
	data.Set("featureIds", featureIDs)

	if raw, ok := data.Get("optionalPaths"); ok {
		if paths, ok := raw.([]any); ok {
			for i, p := range paths {
				optPath, _ := p.(string)
				if !strings.HasSuffix(optPath, "/*") || len(optPath) < 3 {
					continue
				}
				dir := optPath[:len(optPath)-2]
				var inDir []string
				for _, f := range files {
					if strings.HasPrefix(f.(string), dir) {
						inDir = append(inDir, f.(string))
					}
				}
				logger.Write(fmt.Sprintf("Given optionalPath starting with '%s' has %d files", dir, len(inDir)), log.LevelTrace)
				if len(inDir) == 1 && pfs.IsFile(filepath.Join(srcFolder, inDir[0])) {
					paths[i] = inDir[0]
				}
			}
			data.Set("optionalPaths", paths)
		}
	}
	writeJSStringified(jsonPath, data)
	return true
}

// templateFiles lists the files under dir, relative to it: a folder's own
// files, by name, before the contents of its sub-folders, in name order. TS
// recursive-readdir appends each entry when its asynchronous stat completes, so
// its order is not defined; this is a deterministic order with the same set.
func templateFiles(root, rel string) []any {
	entries, err := os.ReadDir(filepath.Join(root, rel))
	if err != nil {
		return nil
	}
	var files, nested []any
	for _, e := range entries {
		p := path.Join(rel, e.Name())
		if e.IsDir() {
			nested = append(nested, templateFiles(root, p)...)
		} else {
			files = append(files, p)
		}
	}
	return append(files, nested...)
}

// writeCollectionMetadata writes devcontainer-collection.json as TS does
// (JSON.stringify(collection, null, 4)). Returns an error instead of swallowing
// a write failure: a full disk or read-only output would otherwise yield an
// incomplete package that the command still reports as a success.
func writeCollectionMetadata(outputDir, collectionType string, collection []*orderedmap.OrderedMap) error {
	items := make([]any, len(collection))
	for i, m := range collection {
		items[i] = m
	}
	collMeta := orderedmap.New()
	collMeta.Set("sourceInformation", map[string]any{"source": "devcontainer-cli"})
	collMeta.Set(collectionType+"s", items)
	if err := os.WriteFile(filepath.Join(outputDir, "devcontainer-collection.json"), jsStringify(collMeta), 0o644); err != nil {
		return fmt.Errorf("write collection metadata: %w", err)
	}
	return nil
}

func writeJSStringified(path string, v any) {
	_ = os.WriteFile(path, jsStringify(v), 0o644)
}

// jsStringify is JSON.stringify(v, null, 4): keys in insertion order, four-space
// indentation, no trailing newline, and '<', '>' and '&' left as they are.
func jsStringify(v any) []byte {
	var b bytes.Buffer
	writeJS(&b, v, "")
	return b.Bytes()
}

// jsStringifyCompact is JSON.stringify(v): no whitespace, keys in insertion
// order, and '<', '>' and '&' left as they are.
func jsStringifyCompact(v any) []byte {
	var b bytes.Buffer
	_ = json.Compact(&b, jsStringify(v))
	return b.Bytes()
}

func writeJS(b *bytes.Buffer, v any, indent string) {
	inner := indent + "    "
	switch t := v.(type) {
	case orderedmap.OrderedMap:
		writeJS(b, &t, indent)
	case *orderedmap.OrderedMap:
		keys := t.Keys()
		if len(keys) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{\n")
		for i, k := range keys {
			val, _ := t.Get(k)
			b.WriteString(inner)
			writeJSString(b, k)
			b.WriteString(": ")
			writeJS(b, val, inner)
			if i < len(keys)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(indent + "}")
	case map[string]any:
		om := orderedmap.New()
		for _, k := range slices.Sorted(maps.Keys(t)) {
			om.Set(k, t[k])
		}
		writeJS(b, om, indent)
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[\n")
		for i, e := range t {
			b.WriteString(inner)
			writeJS(b, e, inner)
			if i < len(t)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(indent + "]")
	case string:
		writeJSString(b, t)
	case float64:
		b.WriteString(strconv.FormatFloat(t, 'f', -1, 64)) // mutate-exempt: any negative precision is the shortest representation
	case int:
		b.WriteString(strconv.Itoa(t))
	case bool:
		b.WriteString(strconv.FormatBool(t))
	case nil:
		b.WriteString("null")
	default:
		data, _ := json.Marshal(t)
		b.Write(data)
	}
}

func writeJSString(b *bytes.Buffer, s string) {
	var sb bytes.Buffer
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	b.Write(bytes.TrimSuffix(sb.Bytes(), []byte("\n")))
}

func readOrderedJSONC(path string) (*orderedmap.OrderedMap, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	std, err := jsonc.StripComments(data)
	if err != nil {
		return nil, err
	}
	m := orderedmap.New()
	if err := json.Unmarshal(std, m); err != nil {
		return nil, err
	}
	return m, nil
}

func mustRead(path string) []byte {
	data, _ := os.ReadFile(path)
	return data
}

func hasKey(m map[string]any, key string) bool {
	_, ok := m[key]
	return ok
}

// jsTruthy is JS truthiness of metadata[key]: a missing key, null, false, 0 and
// "" are falsy, as `!metadata.id` reads them.
func jsTruthy(m *orderedmap.OrderedMap, key string) bool {
	v, ok := m.Get(key)
	if !ok {
		return false
	}
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return t != ""
	case bool:
		return t
	case float64:
		return t != 0
	}
	return true
}

// copyToTemp copies folder to a fresh temporary directory, as TS cpDirectoryLocal
// does before completing the metadata, so the source is left untouched.
func copyToTemp(folder string) (string, error) {
	tmp, err := os.MkdirTemp("", "devcontainer-package-")
	if err != nil {
		return "", err
	}
	if err := copyDir(folder, tmp); err != nil {
		os.RemoveAll(tmp)
		return "", fmt.Errorf("copy %s: %w", folder, err)
	}
	return tmp, nil
}
