package cli

import (
	"archive/tar"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/jsonc"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
	"github.com/devcontainers/cli/internal/pfs"
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
			return publishCollection(resolvePath(target), registry, namespace, "feature", logLevel)
		},
	}

	cmd.Flags().StringVarP(&registry, "registry", "r", "ghcr.io", "OCI registry.")
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "Collection namespace.")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "Log level.")
	cmd.MarkFlagRequired("namespace")

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
			return publishCollection(resolvePath(target), registry, namespace, "template", logLevel)
		},
	}

	cmd.Flags().StringVarP(&registry, "registry", "r", "ghcr.io", "OCI registry.")
	cmd.Flags().StringVarP(&namespace, "namespace", "n", "", "Collection namespace.")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "Log level.")
	cmd.MarkFlagRequired("namespace")

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
		Use:   "generate-docs",
		Short: "Generate documentation",
		RunE: func(cmd *cobra.Command, args []string) error {
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
		Use:   "generate-docs",
		Short: "Generate documentation",
		RunE: func(cmd *cobra.Command, args []string) error {
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
				return fmt.Errorf("--global-scenarios-only and --features cannot be used together")
			}
			if globalOnly && skipScenarios {
				return fmt.Errorf("--global-scenarios-only and --skip-scenarios cannot be used together")
			}

			target := projectFolder
			if len(args) > 0 && args[0] != "." {
				target = args[0]
			}

			logger := log.New(log.Options{
				Level:  log.MapLogLevel(logLevel),
				Format: "text",
				Writer: os.Stderr,
			})

			exitCode := runFeaturesTestCommand(
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
	logger := log.New(log.Options{
		Level:  log.MapLogLevel(logLevelStr),
		Format: "text",
		Writer: os.Stderr,
	})

	if forceClean {
		pfs.Remove(outputDir, true)
	}
	pfs.MkdirAll(outputDir)

	srcDir := filepath.Join(targetFolder, "src")
	if !pfs.IsDir(srcDir) {
		srcDir = targetFolder
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("read source directory: %w", err)
	}

	metadataFile := fmt.Sprintf("devcontainer-%s.json", collectionType)
	var collection []map[string]interface{}

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
		var meta map[string]interface{}
		jsonc.Unmarshal(data, &meta)

		// Create tarball
		archiveName := fmt.Sprintf("devcontainer-%s-%s.tgz", collectionType, entry.Name())
		archivePath := filepath.Join(outputDir, archiveName)
		if err := createTarArchive(archivePath, featureDir); err != nil {
			return fmt.Errorf("package %s: %w", entry.Name(), err)
		}

		logger.Write(fmt.Sprintf("Packaged %s → %s", entry.Name(), archiveName), log.LevelInfo)
		collection = append(collection, meta)
	}

	// Write collection metadata. sourceInformation marks the CLI as the
	// producer, matching the TS CLI's devcontainer-collection.json.
	collMeta := map[string]interface{}{
		"sourceInformation":                map[string]interface{}{"source": "devcontainer-cli"},
		fmt.Sprintf("%ss", collectionType): collection,
	}
	collData, _ := json.MarshalIndent(collMeta, "", "  ")
	collPath := filepath.Join(outputDir, "devcontainer-collection.json")
	os.WriteFile(collPath, collData, 0644)

	logger.Write(fmt.Sprintf("Packaged %d %s(s) to %s", len(collection), collectionType, outputDir), log.LevelInfo)
	return nil
}

func publishCollection(targetFolder, registry, namespace, collectionType, logLevelStr string) error {
	logger := log.New(log.Options{
		Level:  log.MapLogLevel(logLevelStr),
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

	ociClient := oci.NewClient(logger, osEnvMap())

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
		// Empty on first publish (repository does not exist yet).
		published, _ := ociClient.GetPublishedTags(ref)
		tags, skip, tagErr := oci.GetSemanticTags(version, published)
		if tagErr != nil {
			logger.Write(fmt.Sprintf("(!) ERR: %v, skipping...", tagErr), log.LevelError)
			failures++
			return nil, false
		}
		if skip {
			logger.Write(fmt.Sprintf("(!) WARNING: Version %s already exists, skipping %s...", version, resource), log.LevelWarning)
			return &oci.PushResult{PublishedTags: []string{}}, true
		}
		pushResult, err := ociClient.PushArtifact(ref, archivePath, tags, collectionType, annotations)
		if err != nil {
			logger.Write(fmt.Sprintf("(!) ERR: Failed to publish %s: %v", resource, err), log.LevelError)
			failures++
			return nil, false
		}
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
		if _, err := ociClient.PushCollectionMetadata(collRef, collMetaPath); err != nil {
			logger.Write(fmt.Sprintf("(!) ERR: Failed to publish collection metadata: %v", err), log.LevelError)
			failures++
		}
	}

	out, _ := json.Marshal(result)
	fmt.Fprintln(os.Stdout, string(out))

	if failures > 0 {
		return fmt.Errorf("%d %s publish operation(s) failed", failures, collectionType)
	}
	return nil
}

func generateDocs(projectFolder, registry, namespace, githubOwner, githubRepo, collectionType, logLevelStr string) error {
	logger := log.New(log.Options{
		Level:  log.MapLogLevel(logLevelStr),
		Format: "text",
		Writer: os.Stderr,
	})

	srcDir := filepath.Join(projectFolder, "src")
	if !pfs.IsDir(srcDir) {
		srcDir = projectFolder
	}

	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return fmt.Errorf("read source: %w", err)
	}

	metadataFile := fmt.Sprintf("devcontainer-%s.json", collectionType)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		metaPath := filepath.Join(srcDir, entry.Name(), metadataFile)
		if !pfs.IsFile(metaPath) {
			continue
		}

		data, _ := os.ReadFile(metaPath)
		var meta map[string]interface{}
		jsonc.Unmarshal(data, &meta)

		name, _ := meta["name"].(string)
		if name == "" {
			name = entry.Name()
		}
		desc, _ := meta["description"].(string)
		id, _ := meta["id"].(string)
		if id == "" {
			id = entry.Name()
		}

		var readme strings.Builder
		readme.WriteString(fmt.Sprintf("\n# %s (%s)\n\n", name, id))
		readme.WriteString(fmt.Sprintf("%s\n\n", desc))

		// Installation snippet
		if registry != "" && namespace != "" {
			readme.WriteString("## Usage\n\n")
			readme.WriteString("```json\n")
			readme.WriteString(fmt.Sprintf("\"features\": {\n    \"%s/%s/%s:1\": {}\n}\n", registry, namespace, id))
			readme.WriteString("```\n\n")
		}

		// Options table
		if options, ok := meta["options"].(map[string]interface{}); ok && len(options) > 0 {
			readme.WriteString("## Options\n\n")
			readme.WriteString("| Option | Type | Default | Description |\n")
			readme.WriteString("|--------|------|---------|-------------|\n")
			for optName, optVal := range options {
				optMap, _ := optVal.(map[string]interface{})
				optType, _ := optMap["type"].(string)
				optDefault := fmt.Sprintf("%v", optMap["default"])
				optDesc, _ := optMap["description"].(string)
				readme.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n", optName, optType, optDefault, optDesc))
			}
			readme.WriteString("\n")
		}

		// Source link
		if githubOwner != "" && githubRepo != "" {
			readme.WriteString(fmt.Sprintf("---\n\n_Note: This file was auto-generated. See [%s/%s](https://github.com/%s/%s) for source._\n", githubOwner, githubRepo, githubOwner, githubRepo))
		}

		readmePath := filepath.Join(srcDir, entry.Name(), "README.md")
		os.WriteFile(readmePath, []byte(readme.String()), 0644)
		logger.Write(fmt.Sprintf("Generated docs for %s", entry.Name()), log.LevelInfo)
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
	defer tw.Close()

	return filepath.Walk(sourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, _ := filepath.Rel(sourceDir, path)

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
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
}

func mustJSON(v interface{}) []byte {
	data, _ := json.Marshal(v)
	return data
}
