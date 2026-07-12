package cli

import (
	"archive/tar"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/jsonc"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
	"github.com/devcontainers/cli/internal/pfs"
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
		Use:   "package [target]",
		Short: "Package Features",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := "."
			if len(args) > 0 {
				target = args[0]
			}
			return packageCollection(resolvePath(target), resolvePath(outputFolder), "feature", forceClean, logLevel)
		},
	}

	cmd.Flags().StringVarP(&outputFolder, "output-folder", "o", "./output", "Output directory.")
	cmd.Flags().BoolVarP(&forceClean, "force-clean-output-folder", "f", false, "Clean output folder first.")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "Log level.")

	return cmd
}

// --- Features publish ---

func realFeaturesPublishCmd() *cobra.Command {
	var (
		registry  string
		namespace string
		logLevel  string
	)

	cmd := &cobra.Command{
		Use:   "publish [target]",
		Short: "Package and publish Features",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := "."
			if len(args) > 0 {
				target = args[0]
			}
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
		Use:   "publish [target]",
		Short: "Package and publish templates",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := "."
			if len(args) > 0 {
				target = args[0]
			}
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

func packageCollection(targetFolder, outputDir, collectionType string, forceClean bool, logLevelStr string) error {
	// TS parity: --log-level is a yargs choice; reject out-of-range instead of
	// silently defaulting to Info (collectionCommonUtils/package.ts:18).
	if err := validateEnum("log-level", logLevelStr, []string{"info", "debug", "trace"}); err != nil {
		return err
	}
	logger := log.New(log.Options{
		Level:  log.ParseLevel(logLevelStr),
		Format: "text",
		Writer: os.Stderr,
	})

	if forceClean {
		pfs.Remove(outputDir, true)
	}
	pfs.MkdirAll(outputDir)

	metadataFile := fmt.Sprintf("devcontainer-%s.json", collectionType)

	// TS dispatch (prepPackageCommand → isSingle = isLocalFile(target/devcontainer-<type>.json)):
	// when the target folder itself holds the metadata file it is a SINGLE
	// Feature/Template, so package it directly (packageSingleFeatureOrTemplate)
	// instead of scanning for sub-folders — a single-feature folder has none, which
	// would otherwise yield an empty collection and a wrong "Failed to package".
	if singleMeta := filepath.Join(targetFolder, metadataFile); pfs.IsFile(singleMeta) {
		return packageSingleItem(targetFolder, outputDir, collectionType, singleMeta, logger)
	}

	srcDir := filepath.Join(targetFolder, "src")
	if !pfs.IsDir(srcDir) {
		srcDir = targetFolder
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("read source directory: %w", err)
	}

	// orderedmap preserves each metadata file's key order so the emitted
	// collection matches the TS CLI (which spreads the parsed JSON verbatim) rather
	// than the alphabetical order a plain Go map would produce.
	var collection []*orderedmap.OrderedMap

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		featureDir := filepath.Join(srcDir, entry.Name())
		metaPath := filepath.Join(featureDir, metadataFile)
		if !pfs.IsFile(metaPath) {
			continue
		}

		data, _ := os.ReadFile(metaPath)
		std, stripErr := jsonc.StripComments(data)
		if stripErr != nil {
			continue
		}
		meta := orderedmap.New()
		if err := json.Unmarshal(std, meta); err != nil {
			continue
		}
		// TS skips (with an error) any feature/template missing id, version or name.
		if !hasNonEmptyString(meta, "id") || !hasNonEmptyString(meta, "version") || !hasNonEmptyString(meta, "name") {
			logger.Write(fmt.Sprintf("%s '%s' is missing one of the following required properties in its %s: 'id', 'version', 'name'.", collectionType, entry.Name(), metadataFile), log.LevelError)
			continue
		}

		// Create tarball
		archiveName := fmt.Sprintf("devcontainer-%s-%s.tgz", collectionType, entry.Name())
		archivePath := filepath.Join(outputDir, archiveName)
		if err := createTarArchive(archivePath, featureDir); err != nil {
			return fmt.Errorf("package %s: %w", entry.Name(), err)
		}

		logger.Write(fmt.Sprintf("Packaged %s → %s", entry.Name(), archiveName), log.LevelInfo)
		collection = append(collection, meta)
	}

	if len(collection) == 0 {
		logger.Write(fmt.Sprintf("Failed to package %ss", collectionType), log.LevelError)
		return &coreerrors.ExitCodeError{Code: 1}
	}

	writeCollectionMetadata(outputDir, collectionType, collection)

	logger.Write(fmt.Sprintf("Packaged %d %s(s) to %s", len(collection), collectionType, outputDir), log.LevelInfo)
	return nil
}

// packageSingleItem packages a lone Feature/Template folder (one that directly
// contains devcontainer-<type>.json + install.sh), mirroring TS's
// packageSingleFeatureOrTemplate: it emits devcontainer-<type>-<id>.tgz plus a
// one-element devcontainer-collection.json.
func packageSingleItem(targetFolder, outputDir, collectionType, metaPath string, logger log.Logger) error {
	metadataFile := fmt.Sprintf("devcontainer-%s.json", collectionType)

	data, _ := os.ReadFile(metaPath)
	std, stripErr := jsonc.StripComments(data)
	meta := orderedmap.New()
	if stripErr != nil || json.Unmarshal(std, meta) != nil {
		logger.Write(fmt.Sprintf("Failed to package %ss", collectionType), log.LevelError)
		return &coreerrors.ExitCodeError{Code: 1}
	}
	// TS skips (with an error) a single feature/template missing id, version or name.
	// The message omits the folder name (unlike the collection path).
	if !hasNonEmptyString(meta, "id") || !hasNonEmptyString(meta, "version") || !hasNonEmptyString(meta, "name") {
		logger.Write(fmt.Sprintf("%s is missing one of the following required properties in its %s: 'id', 'version', 'name'.", collectionType, metadataFile), log.LevelError)
		return &coreerrors.ExitCodeError{Code: 1}
	}

	idVal, _ := meta.Get("id")
	id, _ := idVal.(string)

	archiveName := fmt.Sprintf("devcontainer-%s-%s.tgz", collectionType, id)
	archivePath := filepath.Join(outputDir, archiveName)
	if err := createTarArchive(archivePath, targetFolder); err != nil {
		return fmt.Errorf("package %s: %w", id, err)
	}
	logger.Write(fmt.Sprintf("Packaged %s '%s'", collectionType, id), log.LevelInfo)

	writeCollectionMetadata(outputDir, collectionType, []*orderedmap.OrderedMap{meta})

	logger.Write(fmt.Sprintf("Packaged %d %s(s) to %s", 1, collectionType, outputDir), log.LevelInfo)
	return nil
}

// writeCollectionMetadata writes devcontainer-collection.json: sourceInformation
// first, then the features/templates array — JSON.stringify(collection, null, 4)
// in TS, so 4-space indentation.
func writeCollectionMetadata(outputDir, collectionType string, collection []*orderedmap.OrderedMap) {
	collMeta := orderedmap.New()
	collMeta.Set("sourceInformation", map[string]string{"source": "devcontainer-cli"})
	collMeta.Set(fmt.Sprintf("%ss", collectionType), collection)
	collData, _ := json.MarshalIndent(collMeta, "", "    ")
	collPath := filepath.Join(outputDir, "devcontainer-collection.json")
	os.WriteFile(collPath, collData, 0644)
}

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
	if err := validateEnum("log-level", logLevelStr, []string{"info", "debug", "trace"}); err != nil {
		return err
	}
	logger := log.New(log.Options{
		Level:  log.ParseLevel(logLevelStr),
		Format: "text",
		Writer: os.Stderr,
	})
	reg := oci.NewClient(logger, osEnvMap())
	return publishCollectionWith(ctx, OSOutput(), reg, targetFolder, registry, namespace, collectionType, logLevelStr)
}

func publishCollectionWith(ctx context.Context, out Output, reg oci.Registry, targetFolder, registry, namespace, collectionType, logLevelStr string) error {
	logger := log.New(log.Options{
		Level:  log.ParseLevel(logLevelStr),
		Format: "text",
		Writer: os.Stderr,
	})

	// Package first
	tmpDir, _ := os.MkdirTemp("", fmt.Sprintf("%s-output-", collectionType))
	defer os.RemoveAll(tmpDir)

	if err := packageCollection(targetFolder, tmpDir, collectionType, true, logLevelStr); err != nil {
		return err
	}

	logger.Write(fmt.Sprintf("Publishing %ss from %s to %s/%s...", collectionType, targetFolder, registry, namespace), log.LevelInfo)

	// Read collection metadata
	collMetaPath := filepath.Join(tmpDir, "devcontainer-collection.json")
	collData, err := os.ReadFile(collMetaPath)
	if err != nil {
		return fmt.Errorf("read collection metadata: %w", err)
	}

	var collMeta map[string]interface{}
	json.Unmarshal(collData, &collMeta)

	// Get items from collection
	itemsKey := fmt.Sprintf("%ss", collectionType)
	items, _ := collMeta[itemsKey].([]interface{})

	ociClient := reg

	result := make(map[string]interface{})
	failures := 0

	// publishOne pushes an item under the forward-only semantic tags derived from
	// what is already published, skipping when the exact version already exists.
	publishOne := func(resource, archivePath, version string, annotations map[string]string) (*oci.PushResult, bool) {
		ref, err := oci.ParseRef(resource)
		if err != nil {
			logger.Write(fmt.Sprintf("(!) ERR: could not parse %q: %v", resource, err), log.LevelError)
			failures++
			return nil, false
		}
		// The repository is empty on first publish (404). Any OTHER error — auth,
		// network, a 5xx — must abort: treating it as "no tags yet" would republish
		// mobile aliases (latest, 1, 1.2) off an empty list and could clobber them.
		published, tagsErr := ociClient.GetPublishedTagsContext(ctx, ref)
		if tagsErr != nil && !oci.IsNotFound(tagsErr) {
			logger.Write(fmt.Sprintf("(!) ERR: could not list existing tags for %q: %v", resource, tagsErr), log.LevelError)
			failures++
			return nil, false
		}
		tags, skip, tagErr := oci.SemanticTags(version, published)
		if tagErr != nil {
			logger.Write(fmt.Sprintf("(!) ERR: %v, skipping...", tagErr), log.LevelError)
			failures++
			return nil, false
		}
		if skip {
			logger.Write(fmt.Sprintf("(!) WARNING: Version %s already exists, skipping %s...", version, resource), log.LevelWarning)
			return &oci.PushResult{PublishedTags: []string{}}, true
		}
		pushResult, err := ociClient.PushArtifact(ctx, ref, archivePath, tags, collectionType, annotations)
		if err != nil {
			logger.Write(fmt.Sprintf("(!) ERR: Failed to publish %s: %v", resource, err), log.LevelError)
			failures++
			return nil, false
		}
		pushResult.Version = version // TS reports the published version in the result
		return pushResult, true
	}

	for _, item := range items {
		meta, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := meta["id"].(string)
		version, _ := meta["version"].(string)
		if id == "" || version == "" {
			logger.Write(fmt.Sprintf("Skipping %s with no id or version", collectionType), log.LevelWarning)
			continue
		}

		archiveName := fmt.Sprintf("devcontainer-%s-%s.tgz", collectionType, id)
		archivePath := filepath.Join(tmpDir, archiveName)
		annotations := map[string]string{
			"dev.containers.metadata": string(mustJSON(meta)),
		}

		if pushResult, ok := publishOne(fmt.Sprintf("%s/%s/%s", registry, namespace, id), archivePath, version, annotations); ok {
			result[id] = pushResult
		}

		// Republish under legacyIds (aliases), matching TS behavior.
		if collectionType == "feature" {
			if legacyIds, ok := meta["legacyIds"].([]interface{}); ok {
				for _, lid := range legacyIds {
					legacyID, ok := lid.(string)
					if !ok || legacyID == "" {
						continue
					}
					logger.Write(fmt.Sprintf("Publishing legacy alias %s for %s...", legacyID, id), log.LevelInfo)
					if lr, ok := publishOne(fmt.Sprintf("%s/%s/%s", registry, namespace, legacyID), archivePath, version, annotations); ok {
						result[legacyID] = lr
					}
				}
			}
		}
	}

	// Publish the collection metadata for the namespace so the collection's
	// items are discoverable (containers.dev, `devcontainer outdated`).
	collectionResource := fmt.Sprintf("%s/%s", registry, namespace)
	if collRef, refErr := oci.ParseRef(collectionResource); refErr == nil {
		if _, err := ociClient.PushCollectionMetadata(ctx, collRef, collMetaPath); err != nil {
			logger.Write(fmt.Sprintf("(!) ERR: Failed to publish collection metadata: %v", err), log.LevelError)
			failures++
		}
	}

	data, _ := json.Marshal(result)
	fmt.Fprintln(out.Stdout(), string(data))

	if failures > 0 {
		return fmt.Errorf("%d %s publish operation(s) failed", failures, collectionType)
	}
	return nil
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
		var meta map[string]interface{}
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

		readme := template
		readme = strings.Replace(readme, "#{Id}", id, 1)
		readme = strings.Replace(readme, "#{Name}", name, 1)
		readme = strings.Replace(readme, "#{Description}", desc, 1)
		readme = strings.Replace(readme, "#{OptionsTable}", generateOptionsMarkdown(std, meta), 1)
		readme = strings.Replace(readme, "#{Notes}", notes, 1)
		readme = strings.Replace(readme, "#{RepoUrl}", urlToConfig, 1)
		readme = strings.Replace(readme, "#{Registry}", registry, 1)
		readme = strings.Replace(readme, "#{Namespace}", namespace, 1)
		readme = strings.Replace(readme, "#{Version}", version, 1)
		readme = strings.Replace(readme, "#{Customizations}", generateCustomizationsMarkdown(meta), 1)

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

// generateOptionsMarkdown renders the options table with columns and row order
// matching TS: "| Options Id | Description | Type | Default Value |" in the JSON's
// insertion order. Returns "" only when the metadata has no "options" key.
func generateOptionsMarkdown(std []byte, meta map[string]interface{}) string {
	optsVal, hasOptions := meta["options"]
	if !hasOptions {
		return ""
	}
	optsMap, _ := optsVal.(map[string]interface{})

	var optionsRaw json.RawMessage
	var top map[string]json.RawMessage
	if json.Unmarshal(std, &top) == nil {
		optionsRaw = top["options"]
	}

	var rows []string
	for _, k := range orderedObjectKeys(optionsRaw) {
		ov, _ := optsMap[k].(map[string]interface{})
		desc := jsTruthyString(ov["description"], "-")
		typ := jsTruthyString(ov["type"], "-")
		rows = append(rows, fmt.Sprintf("| %s | %s | %s | %s |", k, desc, typ, defaultCell(ov)))
	}
	return "## Options\n\n| Options Id | Description | Type | Default Value |\n|-----|-----|-----|-----|\n" + strings.Join(rows, "\n")
}

func generateCustomizationsMarkdown(meta map[string]interface{}) string {
	cust, _ := meta["customizations"].(map[string]interface{})
	vscode, _ := cust["vscode"].(map[string]interface{})
	exts, _ := vscode["extensions"].([]interface{})
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

func generateDocsHeader(meta map[string]interface{}) string {
	deprecated, _ := meta["deprecated"].(bool)
	legacy, _ := meta["legacyIds"].([]interface{})
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

// hasNonEmptyString reports whether an orderedmap has a non-empty string at key,
// mirroring the JS truthiness check (`!metadata.id`).
func hasNonEmptyString(m *orderedmap.OrderedMap, key string) bool {
	v, ok := m.Get(key)
	if !ok {
		return false
	}
	s, ok := v.(string)
	return ok && s != ""
}

// jsTruthyString mirrors JS `value || fallback` for string cells: an empty or
// non-string value falls back.
func jsTruthyString(v interface{}, fallback string) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return fallback
}

// defaultCell mirrors TS `val.default !== ” ? val.default : '-'`: an empty string
// becomes "-", an absent default renders "undefined", everything else prints as JS
// would interpolate it.
func defaultCell(ov map[string]interface{}) string {
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
		if x {
			return "true"
		}
		return "false"
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

func mustJSON(v interface{}) []byte {
	data, _ := json.Marshal(v)
	return data
}
