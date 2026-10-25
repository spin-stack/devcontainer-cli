package cli

import (
	"archive/tar"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/jsonc"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
	"github.com/iancoleman/orderedmap"
	"github.com/spf13/cobra"
)

// --- Features package ---

func realFeaturesPackageCmd() *cobra.Command {
	var (
		outputFolder string
		forceClean   bool
		logLevel     string
	)

	cmd := &cobra.Command{
		Use:   "package <target>",
		Short: "Package Features",
		Args:  requireTarget,
		RunE: func(cmd *cobra.Command, args []string) error {
			target := args[0]
			return packageCollection(resolvePath(target), resolvePath(outputFolder), "feature", forceClean, logLevel)
		},
	}

	cmd.Flags().StringVarP(&outputFolder, "output-folder", "o", "./output", "Output directory.")
	cmd.Flags().BoolVarP(&forceClean, "force-clean-output-folder", "f", false, "Clean output folder first.")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "Log level.")

	return cmd
}

// --- Features publish ---

// requireTarget is yargs' check for a required <target> positional: TS declares
// `package <target>` and `publish <target>`, and stops when it is missing.
func requireTarget(_ *cobra.Command, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("Not enough non-option arguments: got %d, need at least 1", len(args))
	}
	return cobra.MaximumNArgs(1)(nil, args)
}

func realFeaturesPublishCmd() *cobra.Command {
	var (
		registry  string
		namespace string
		logLevel  string
	)

	cmd := &cobra.Command{
		Use:   "publish <target>",
		Short: "Package and publish Features",
		Args:  requireTarget,
		RunE: func(cmd *cobra.Command, args []string) error {
			target := args[0]
			return publishCollection(cmd.Context(), resolvePath(target), registry, namespace, "feature", logLevel)
		},
	}

	cmd.Flags().StringVarP(&registry, "registry", "r", "ghcr.io", "OCI registry.")
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "Collection namespace.")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "Log level.")

	return cmd
}

// --- Templates publish ---

func realTemplatesPublishCmd() *cobra.Command {
	var (
		registry  string
		namespace string
		logLevel  string
	)

	cmd := &cobra.Command{
		Use:   "publish <target>",
		Short: "Package and publish templates",
		Args:  requireTarget,
		RunE: func(cmd *cobra.Command, args []string) error {
			target := args[0]
			return publishCollection(cmd.Context(), resolvePath(target), registry, namespace, "template", logLevel)
		},
	}

	cmd.Flags().StringVarP(&registry, "registry", "r", "ghcr.io", "OCI registry.")
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "Collection namespace.")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "Log level.")

	return cmd
}

// --- Generate docs ---

func realFeaturesGenerateDocsCmd() *cobra.Command {
	var (
		projectFolder string
		registry      string
		namespace     string
		githubOwner   string
		githubRepo    string
		logLevel      string
	)

	cmd := &cobra.Command{
		Use:   "generate-docs [project-folder]",
		Short: "Generate documentation",
		Long: "Generate a README.md for each feature under the project folder.\n\n" +
			"The project folder may either contain one feature per sub-directory\n" +
			"(each with a devcontainer-feature.json), or be a single feature folder\n" +
			"itself. It can be given positionally or with --project-folder (default: .).",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				projectFolder = args[0]
			}
			return generateDocs(resolvePath(projectFolder), registry, namespace, githubOwner, githubRepo, "feature", logLevel)
		},
	}

	cmd.Flags().StringVarP(&projectFolder, "project-folder", "p", ".", "Project folder.")
	cmd.Flags().StringVarP(&registry, "registry", "r", "ghcr.io", "Registry.")
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "Namespace.")
	cmd.Flags().StringVar(&githubOwner, "github-owner", "", "GitHub owner.")
	cmd.Flags().StringVar(&githubRepo, "github-repo", "", "GitHub repo.")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "Log level.")
	cmd.MarkFlagRequired("namespace")

	return cmd
}

func realTemplatesGenerateDocsCmd() *cobra.Command {
	var (
		projectFolder string
		githubOwner   string
		githubRepo    string
		logLevel      string
	)

	cmd := &cobra.Command{
		Use:   "generate-docs [project-folder]",
		Short: "Generate documentation",
		Long: "Generate a README.md for each template under the project folder.\n\n" +
			"The project folder may either contain one template per sub-directory\n" +
			"(each with a devcontainer-template.json), or be a single template folder\n" +
			"itself. It can be given positionally or with --project-folder (default: .).",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				projectFolder = args[0]
			}
			return generateDocs(resolvePath(projectFolder), "", "", githubOwner, githubRepo, "template", logLevel)
		},
	}

	cmd.Flags().StringVarP(&projectFolder, "project-folder", "p", ".", "Project folder.")
	cmd.Flags().StringVar(&githubOwner, "github-owner", "", "GitHub owner.")
	cmd.Flags().StringVar(&githubRepo, "github-repo", "", "GitHub repo.")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "Log level.")

	return cmd
}

// --- Features test ---

func realFeaturesTestCmd() *cobra.Command {
	var (
		projectFolder       string
		featuresList        []string
		filter              string
		globalOnly          bool
		skipScenarios       bool
		skipAuto            bool
		skipDupl            bool
		baseImage           string
		remoteUser          string
		logLevel            string
		preserve            bool
		quiet               bool
		permitRandomization bool
	)

	cmd := &cobra.Command{
		Use:   "test [target]",
		Short: "Test Features",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Validate conflicting flags (matches TS behavior)
			if globalOnly && len(featuresList) > 0 {
				return fmt.Errorf("Cannot combine --global-scenarios-only and --features")
			}
			if globalOnly && skipScenarios {
				return fmt.Errorf("Cannot combine --skip-scenarios and --global-scenarios-only")
			}

			target := projectFolder
			if len(args) > 0 && args[0] != "." {
				target = args[0]
			}

			logger := log.New(log.Options{
				Level:  log.ParseLevel(logLevel),
				Format: "text",
				Writer: os.Stderr,
			})

			exitCode := runFeaturesTestCommand(
				outputFor(cmd),
				logger,
				resolvePath(target),
				featuresList,
				filter,
				globalOnly,
				skipScenarios,
				skipAuto,
				skipDupl,
				baseImage,
				remoteUser,
				preserve,
				quiet,
				permitRandomization,
				ociAuthGlobalArgs(cmd),
			)
			if exitCode != 0 {
				return &coreerrors.ExitCodeError{Code: exitCode}
			}
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVarP(&projectFolder, "project-folder", "p", ".", "Project folder.")
	f.StringArrayVarP(&featuresList, "features", "f", nil, "Features to test.")
	f.StringVar(&filter, "filter", "", "Filter scenarios.")
	f.BoolVar(&globalOnly, "global-scenarios-only", false, "Run only global scenarios.")
	f.BoolVar(&skipScenarios, "skip-scenarios", false, "Skip scenario tests.")
	f.BoolVar(&skipAuto, "skip-autogenerated", false, "Skip autogenerated tests.")
	f.BoolVar(&skipDupl, "skip-duplicated", false, "Skip duplicate tests.")
	f.BoolVar(&preserve, "preserve-test-containers", false, "Keep test containers.")
	f.StringVarP(&baseImage, "base-image", "i", "ubuntu:focal", "Base image.")
	f.StringVarP(&remoteUser, "remote-user", "u", "", "Remote user.")
	f.StringVar(&logLevel, "log-level", "info", "Log level.")
	f.BoolVarP(&quiet, "quiet", "q", false, "Quiet mode.")
	f.BoolVar(&permitRandomization, "permit-randomization", false, "Allow randomized test ordering.")

	return cmd
}

// --- Shared implementation ---

// publishCollection wires the real OCI registry and process stdout, then
// delegates to publishCollectionWith. Tests call publishCollectionWith directly
// with a fake oci.Registry and a capturing Output to drive the partial-publish
// error path hermetically.
func publishCollection(ctx context.Context, targetFolder, registry, namespace, collectionType, logLevelStr string) error {
	// TS parity: yargs demandOption → "Missing required argument: namespace"
	// (canonicalized by the harness), not cobra's "required flag(s) ... not set".
	if namespace == "" {
		return fmt.Errorf("Missing required argument: namespace")
	}
	// TS parity: --log-level is a yargs choice; reject out-of-range instead of
	// silently defaulting to Info (collectionCommonUtils/publish.ts:14).
	if err := validateEnum("log-level", logLevelStr, logLevels); err != nil {
		return err
	}
	logger := log.New(log.Options{
		Level:  log.ParseLevel(logLevelStr),
		Format: "text",
		Writer: os.Stderr,
	})
	reg := newOCIClient(ctx, logger)
	return publishCollectionWith(ctx, OSOutput(), reg, targetFolder, registry, namespace, collectionType, logLevelStr)
}

func publishCollectionWith(ctx context.Context, out Output, reg oci.Registry, targetFolder, registry, namespace, collectionType, logLevelStr string) error {
	logger := log.New(log.Options{
		Level:  log.ParseLevel(logLevelStr),
		Format: "text",
		Writer: os.Stderr,
	})

	// Package first. A failed MkdirTemp would otherwise leave tmpDir="" and scatter
	// the packaged artifacts into the current directory.
	tmpDir, err := os.MkdirTemp("", fmt.Sprintf("%s-output-", collectionType))
	if err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	if err := packageCollection(targetFolder, tmpDir, collectionType, true, logLevelStr); err != nil {
		// TS features publish adds this line when packaging failed (featuresCLI/publish.ts).
		if _, ok := errors.AsType[*coreerrors.ExitCodeError](err); ok && collectionType == "feature" {
			logger.Write("(!) ERR: Failed to fetch devcontainer-collection.json", log.LevelError)
		}
		return err
	}

	logger.Write(fmt.Sprintf("Publishing %ss from %s to %s/%s...", collectionType, targetFolder, registry, namespace), log.LevelInfo)

	// Read collection metadata
	collMetaPath := filepath.Join(tmpDir, "devcontainer-collection.json")
	collData, err := os.ReadFile(collMetaPath)
	if err != nil {
		return fmt.Errorf("read collection metadata: %w", err)
	}

	var collMeta map[string]any
	json.Unmarshal(collData, &collMeta)

	// Get items from collection. The ordered copy keeps each item's key order for
	// its dev.containers.metadata annotation, which TS writes as JSON.stringify(f).
	itemsKey := fmt.Sprintf("%ss", collectionType)
	items, _ := collMeta[itemsKey].([]any)
	orderedColl := orderedmap.New()
	_ = json.Unmarshal(collData, orderedColl)
	orderedItemsVal, _ := orderedColl.Get(itemsKey)
	orderedItems, _ := orderedItemsVal.([]any)

	ociClient := reg
	failed := func(msg string) error {
		logger.Write(msg, log.LevelError)
		return &coreerrors.ExitCodeError{Code: 1}
	}
	kind := strings.ToUpper(collectionType[:1]) + collectionType[1:]

	// publishOne is TS doPublishCommand: the published tags and digest, an empty
	// result when the version is already published, or an error that ends the
	// command (TS exits 1, before printing any result).
	publishOne := func(ref *oci.Ref, archivePath, version string, annotations map[string]string) (*oci.PushResult, error) {
		logger.Write("Fetching published versions...", log.LevelInfo)
		published, tagsErr := ociClient.GetPublishedTagsContext(ctx, ref)
		if tagsErr != nil && !oci.IsNotFound(tagsErr) {
			logger.Write(fmt.Sprintf("(!) ERR: Could not fetch published tags for '%s/%s' : %v ", ref.Namespace, ref.ID, tagsErr), log.LevelError)
			return nil, tagsErr
		}
		if slices.Contains(published, version) {
			logger.Write(fmt.Sprintf("(!) WARNING: Version %s already exists, skipping %s...", version, version), log.LevelWarning)
			return &oci.PushResult{}, nil
		}
		tags, _, tagErr := oci.SemanticTags(version, published)
		if tagErr != nil {
			return nil, failed(fmt.Sprintf("(!) ERR: Version %s is not a valid semantic version, skipping %s...", version, version))
		}
		logger.Write(fmt.Sprintf("Publishing tags: %s...", strings.Join(tags, ",")), log.LevelInfo)
		pushResult, err := ociClient.PushArtifact(ctx, ref, archivePath, tags, collectionType, annotations)
		if err != nil {
			logger.Write(fmt.Sprintf("(!) ERR: Failed to publish %s: '%s'", collectionType, ref.Resource), log.LevelError)
			return nil, err
		}
		logger.Write(fmt.Sprintf("Published %s: '%s'", collectionType, ref.ID), log.LevelInfo)
		return pushResult, nil
	}

	result := make(map[string]publishItemResult)
	for i, item := range items {
		meta, ok := item.(map[string]any)
		if !ok {
			continue
		}
		id, _ := meta["id"].(string)
		version, _ := meta["version"].(string)
		logger.Write(fmt.Sprintf("Processing %s: %s...", collectionType, id), log.LevelInfo)
		if version == "" {
			logger.Write(fmt.Sprintf("(!) WARNING: Version does not exist, skipping %s...", id), log.LevelWarning)
			continue
		}
		resource := fmt.Sprintf("%s/%s/%s", registry, namespace, id)
		ref, err := oci.ParseRef(resource)
		if err != nil {
			return failed(fmt.Sprintf("(!) Could not parse provided %s identifier: '%s'", kind, resource))
		}
		archivePath := filepath.Join(tmpDir, fmt.Sprintf("devcontainer-%s-%s.tgz", collectionType, id))
		var annotated any = meta
		if i < len(orderedItems) {
			annotated = orderedItems[i]
		}
		annotations := map[string]string{"dev.containers.metadata": string(jsStringifyCompact(annotated))}

		pushResult, err := publishOne(ref, archivePath, version, annotations)
		if err != nil {
			if _, ok := errors.AsType[*coreerrors.ExitCodeError](err); ok {
				return err
			}
			return failed(fmt.Sprintf("(!) ERR: Failed to publish '%s'", resource))
		}
		isPublished := pushResult.Digest != "" && len(pushResult.PublishedTags) > 0
		var this publishItemResult
		if isPublished {
			this = publishItemResult{PublishedTags: pushResult.PublishedTags, Digest: pushResult.Digest, Version: version}
		}

		// TS republishes a Feature under its legacyIds only when this version was
		// published, and reports the aliases it published inside the item.
		legacyIDs, _ := meta["legacyIds"].([]any)
		if collectionType == "feature" && isPublished && len(legacyIDs) > 0 {
			logger.Write(fmt.Sprintf("Processing legacyIds for '%s'...", id), log.LevelInfo)
			for _, lid := range legacyIDs {
				legacyID, _ := lid.(string)
				logger.Write(fmt.Sprintf("Processing legacyId: '%s'...", legacyID), log.LevelInfo)
				legacyResource := fmt.Sprintf("%s/%s/%s", registry, namespace, legacyID)
				legacyRef, err := oci.ParseRef(legacyResource)
				if err != nil {
					return failed(fmt.Sprintf("(!) Could not parse provided Feature identifier: '%s'", legacyResource))
				}
				lr, err := publishOne(legacyRef, archivePath, version, annotations)
				if err != nil {
					if _, ok := errors.AsType[*coreerrors.ExitCodeError](err); ok {
						return err
					}
					return failed(fmt.Sprintf("(!) ERR: Failed to publish '%s'", legacyResource))
				}
				if lr.Digest != "" && len(lr.PublishedTags) > 0 {
					this.PublishedLegacyIDs = append(this.PublishedLegacyIDs, legacyID)
				}
			}
		}
		result[id] = this
	}

	// Publish the collection metadata for the namespace (TS getCollectionRef +
	// doPublishMetadata) so the collection's items are discoverable.
	collRef, err := oci.ParseCollectionRef(registry, namespace)
	if err != nil {
		return failed(fmt.Sprintf("(!) Could not parse provided collection identifier with registry '%s' and namespace '%s'", registry, namespace))
	}
	logger.Write("Publishing collection metadata...", log.LevelInfo)
	if _, err := ociClient.PushCollectionMetadata(ctx, collRef.Ref(), collMetaPath); err != nil {
		logger.Write("(!) ERR: Failed to publish collection metadata: devcontainer-collection.json", log.LevelError)
		return failed(fmt.Sprintf("(!) ERR: Failed to publish '%s/%s'", collRef.Registry, collRef.Path))
	}
	logger.Write("Published collection metadata.", log.LevelInfo)

	data, _ := json.Marshal(result)
	fmt.Fprintln(out.Stdout(), string(data))
	return nil
}

// publishItemResult is one item of TS publish's result: empty when nothing was
// published (the version already existed), else the tags, digest and version,
// and for a Feature the legacy ids it was also published under.
type publishItemResult struct {
	PublishedTags      []string `json:"publishedTags,omitempty"`
	Digest             string   `json:"digest,omitempty"`
	Version            string   `json:"version,omitempty"`
	PublishedLegacyIDs []string `json:"publishedLegacyIds,omitempty"`
}

// README templates match the TS FEATURES_README_TEMPLATE / TEMPLATE_README_TEMPLATE
// byte-for-byte. They live as real files (leading newline, blank lines, double space
// before "Add" all preserved) so they stay readable and diffable instead of being
// buried in escaped Go string literals.
//
//go:embed templates/feature-readme.tmpl
var featuresReadmeTemplate string

//go:embed templates/template-readme.tmpl
var templatesReadmeTemplate string

// generateDocs is a faithful port of the TS _generateDocumentation: it iterates
// the DIRECT children of projectFolder (no auto-descent into src/), and for each
// child writes <child>/README.md from the metadata file, preserving option order.
func generateDocs(projectFolder, registry, namespace, githubOwner, githubRepo, collectionType, logLevelStr string) error {
	logger := log.New(log.Options{
		Level:  log.ParseLevel(logLevelStr),
		Format: "text",
		Writer: os.Stderr,
	})

	metadataFile := fmt.Sprintf("devcontainer-%s.json", collectionType)
	template := featuresReadmeTemplate
	if collectionType == "template" {
		template = templatesReadmeTemplate
	}

	// Resolve which sub-folders to document. Upstream #876: pointing at a single
	// collection item (a folder that directly holds devcontainer-<type>.json) used
	// to iterate that item's own files as if each were a collection member; and
	// non-directory siblings at the top level were treated as folders too. Handle
	// both: if the project folder itself is a collection item, document just it;
	// otherwise document only its sub-directories (never stray files).
	fi, statErr := os.Stat(projectFolder)
	if statErr != nil {
		return fmt.Errorf("read project folder: %w", statErr)
	}
	if !fi.IsDir() {
		return fmt.Errorf("project folder %q is not a directory", projectFolder)
	}

	var folders []string
	if _, err := os.Stat(filepath.Join(projectFolder, metadataFile)); err == nil {
		folders = []string{"."}
	} else {
		entries, err := os.ReadDir(projectFolder)
		if err != nil {
			return fmt.Errorf("read project folder: %w", err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".") || !entry.IsDir() {
				continue
			}
			folders = append(folders, entry.Name())
		}
	}

	basePathTrimmed := strings.TrimPrefix(projectFolder, "./")

	for _, f := range folders {
		readmePath := filepath.Join(projectFolder, f, "README.md")
		logger.Write(fmt.Sprintf("Generating %s...", readmePath), log.LevelInfo)

		jsonPath := filepath.Join(projectFolder, f, metadataFile)
		raw, readErr := os.ReadFile(jsonPath)
		if readErr != nil {
			logger.Write(fmt.Sprintf("(!) Warning: %s not found at path '%s'. Skipping...", metadataFile, jsonPath), log.LevelWarning)
			continue
		}

		std, stripErr := jsonc.StripComments(raw)
		if stripErr != nil {
			logger.Write(fmt.Sprintf("Failed to parse %s: %v", jsonPath, stripErr), log.LevelError)
			continue
		}
		var meta map[string]any
		if err := json.Unmarshal(std, &meta); err != nil {
			logger.Write(fmt.Sprintf("Failed to parse %s: %v", jsonPath, err), log.LevelError)
			continue
		}
		id, _ := meta["id"].(string)
		if id == "" {
			logger.Write(fmt.Sprintf("%s for '%s' does not contain an 'id'", metadataFile, f), log.LevelError)
			continue
		}

		name := id
		if n, _ := meta["name"].(string); n != "" {
			name = fmt.Sprintf("%s (%s)", n, id)
		}
		desc, _ := meta["description"].(string)

		version := "latest"
		if v, _ := meta["version"].(string); v != "" {
			version = strings.SplitN(v, ".", 2)[0]
		}

		notes := ""
		if n, e := os.ReadFile(filepath.Join(projectFolder, f, "NOTES.md")); e == nil {
			notes = string(n)
		}

		urlToConfig := metadataFile
		if githubOwner != "" && githubRepo != "" {
			urlToConfig = fmt.Sprintf("https://github.com/%s/%s/blob/main/%s/%s/%s", githubOwner, githubRepo, basePathTrimmed, f, metadataFile)
		}

		readme := renderDocsTemplate(template, map[string]string{
			"Id":             id,
			"Name":           name,
			"Description":    desc,
			"OptionsTable":   generateOptionsMarkdown(std, meta),
			"Notes":          notes,
			"RepoUrl":        urlToConfig,
			"Registry":       registry,
			"Namespace":      namespace,
			"Version":        version,
			"Customizations": generateCustomizationsMarkdown(meta),
		})

		if header := generateDocsHeader(meta); header != "" {
			readme = header + readme
		}

		os.Remove(readmePath)
		if err := os.WriteFile(readmePath, []byte(readme), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", readmePath, err)
		}
	}

	return nil
}

func renderDocsTemplate(template string, values map[string]string) string {
	pairs := make([]string, 0, len(values)*2)
	for name, value := range values {
		pairs = append(pairs, "#{"+name+"}", value)
	}
	return strings.NewReplacer(pairs...).Replace(template)
}

// generateOptionsMarkdown renders the options table with columns and row order
// matching TS: "| Options Id | Description | Type | Default Value |" in the JSON's
// insertion order. Returns "" only when the metadata has no "options" key.
func generateOptionsMarkdown(std []byte, meta map[string]any) string {
	optsVal, hasOptions := meta["options"]
	if !hasOptions {
		return ""
	}
	optsMap, _ := optsVal.(map[string]any)

	var optionsRaw json.RawMessage
	var top map[string]json.RawMessage
	if json.Unmarshal(std, &top) == nil {
		optionsRaw = top["options"]
	}

	var rows []string
	for _, k := range orderedObjectKeys(optionsRaw) {
		ov, _ := optsMap[k].(map[string]any)
		desc := jsTruthyString(ov["description"], "-")
		typ := jsTruthyString(ov["type"], "-")
		rows = append(rows, fmt.Sprintf("| %s | %s | %s | %s |", k, desc, typ, defaultCell(ov)))
	}
	return "## Options\n\n| Options Id | Description | Type | Default Value |\n|-----|-----|-----|-----|\n" + strings.Join(rows, "\n")
}

func generateCustomizationsMarkdown(meta map[string]any) string {
	cust, _ := meta["customizations"].(map[string]any)
	vscode, _ := cust["vscode"].(map[string]any)
	exts, _ := vscode["extensions"].([]any)
	if len(exts) == 0 {
		return ""
	}
	var lines []string
	for _, e := range exts {
		if s, ok := e.(string); ok {
			lines = append(lines, fmt.Sprintf("- `%s`", s))
		}
	}
	return "\n## Customizations\n\n### VS Code Extensions\n\n" + strings.Join(lines, "\n") + "\n"
}

func generateDocsHeader(meta map[string]any) string {
	deprecated, _ := meta["deprecated"].(bool)
	legacy, _ := meta["legacyIds"].([]any)
	if !deprecated && len(legacy) == 0 {
		return ""
	}
	h := "### **IMPORTANT NOTE**\n"
	if deprecated {
		h += "- **This Feature is deprecated, and will no longer receive any further updates/support.**\n"
	}
	if len(legacy) > 0 {
		ids := make([]string, 0, len(legacy))
		for _, l := range legacy {
			if s, ok := l.(string); ok {
				ids = append(ids, fmt.Sprintf("'%s'", s))
			}
		}
		h += fmt.Sprintf("- **Ids used to publish this Feature in the past - %s**\n", strings.Join(ids, ", "))
	}
	return h
}

// jsTruthyString mirrors JS `value || fallback` for string cells: an empty or
// non-string value falls back.
func jsTruthyString(v any, fallback string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return fallback
}

// defaultCell mirrors TS `val.default !== ” ? val.default : '-'`: an empty string
// becomes "-", an absent default renders "undefined", everything else prints as JS
// would interpolate it.
func defaultCell(ov map[string]any) string {
	d, ok := ov["default"]
	if !ok {
		return "undefined"
	}
	if s, isStr := d.(string); isStr {
		if s == "" {
			return "-"
		}
		return s
	}
	switch x := d.(type) {
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%v", x)
	}
}

// orderedObjectKeys returns the keys of a JSON object in source order.
func orderedObjectKeys(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	t, err := dec.Token()
	if err != nil {
		return nil
	}
	if d, ok := t.(json.Delim); !ok || d != '{' {
		return nil
	}
	var keys []string
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			break
		}
		if key, ok := kt.(string); ok {
			keys = append(keys, key)
		}
		if err := skipJSONValue(dec); err != nil {
			break
		}
	}
	return keys
}

func skipJSONValue(dec *json.Decoder) error {
	t, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := t.(json.Delim); ok && (d == '{' || d == '[') {
		for dec.More() {
			if d == '{' {
				if _, err := dec.Token(); err != nil { // key
					return err
				}
			}
			if err := skipJSONValue(dec); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil { // closing delim
			return err
		}
	}
	return nil
}

// createTarArchive creates a plain (uncompressed) tar archive from a directory,
// with entries rooted at "./" — matching the TS CLI (tar.create({cwd}, ["."]))
// and the `+tar` layer media type. Not byte-reproducible (tar headers embed
// mtimes), mirroring the TS CLI, which is likewise non-deterministic.
func createTarArchive(archivePath, sourceDir string) error {
	file, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()

	tw := tar.NewWriter(file)

	walkErr := filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(sourceDir, path)

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		// node-tar (the TS CLI) does not record user/group names, so leave them
		// empty for a closer byte match.
		header.Uname = ""
		header.Gname = ""
		// Root as "./", children as "./<rel>" with forward slashes; directories
		// keep a trailing slash.
		if relPath == "." {
			header.Name = "./"
		} else {
			header.Name = "./" + strings.ReplaceAll(relPath, "\\", "/")
			if info.IsDir() {
				header.Name += "/"
			}
		}

		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		_, err = io.Copy(tw, f)
		return err
	})
	if walkErr != nil {
		return walkErr
	}

	// Close flushes the tar footer/padding; a deferred Close would drop that
	// error and let us push a truncated archive to the registry as if it were
	// whole.
	if err := tw.Close(); err != nil {
		return fmt.Errorf("finalize tar: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", archivePath, err)
	}
	return nil
}
