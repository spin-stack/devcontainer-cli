package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/core/log"
	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/oci"
	"github.com/spf13/cobra"
)

func newOutdatedCmd() *cobra.Command {
	var (
		workspaceFolder string
		configPath      string
		outputFormat    string
		logLevel        string
		logFormat       string
	)

	cmd := &cobra.Command{
		Use:   "outdated",
		Short: "Show current and available versions",
		RunE: func(cmd *cobra.Command, args []string) error {
			// 0.88: workspace-folder defaults to cwd when not provided.
			if workspaceFolder == "" {
				workspaceFolder, _ = os.Getwd()
			}
			return runOutdated(workspaceFolder, configPath, outputFormat, logLevel, logFormat)
		},
	}

	f := cmd.Flags()
	f.StringVar(&workspaceFolder, "workspace-folder", "", "Workspace folder path.")
	f.StringVar(&configPath, "config", "", "devcontainer.json path.")
	f.StringVar(&outputFormat, "output-format", "text", "Output format.")
	f.StringVar(&logLevel, "log-level", "info", "Log level.")
	f.StringVar(&logFormat, "log-format", "text", "Log format.")
	f.String("user-data-folder", "", "")
	f.Int("terminal-columns", 0, "")
	f.Int("terminal-rows", 0, "")

	addLogFileFlags(cmd)
	return cmd
}

type outdatedEntry struct {
	Current string `json:"current"`
	Wanted  string `json:"wanted"`
	Latest  string `json:"latest"`
}

func runOutdated(workspaceFolder, configPath, outputFormat, logLevelStr, logFormatStr string) error {
	logger := log.New(log.Options{
		Level:  log.MapLogLevel(logLevelStr),
		Format: logFormatStr,
		Writer: os.Stderr,
	})

	ws := resolvePath(workspaceFolder)
	cp := ""
	if configPath != "" {
		cp = resolvePath(configPath)
	}

	loadResult, err := config.LoadDevContainerConfig(ws, cp, "")
	if err != nil {
		return err
	}

	cfg := loadResult.Config
	if cfg.Features == nil || len(cfg.Features) == 0 {
		if outputFormat == "json" {
			fmt.Fprintln(os.Stdout, `{"features":{}}`)
		} else {
			fmt.Println("No features configured.")
		}
		return nil
	}

	ociClient := oci.NewClient(logger, osEnvMap())

	result := make(map[string]outdatedEntry)
	var featureIDs []string

	for id := range cfg.Features {
		resolvedID, _ := features.ResolveFeatureID(id, false)
		featureIDs = append(featureIDs, id)

		ref, err := oci.ParseRef(resolvedID)
		if err != nil {
			continue
		}

		// Current version from the tag used
		current := ref.Tag
		if current == "" || current == "latest" {
			current = "-"
		}

		// Fetch all published tags
		tags, err := ociClient.GetPublishedTags(ref)
		if err != nil || len(tags) == 0 {
			result[features.StripVersionFromFeatureID(id)] = outdatedEntry{
				Current: current, Wanted: "-", Latest: "-",
			}
			continue
		}

		// Filter to semver-only tags and sort
		var versions []*semver.Version
		for _, t := range tags {
			v, err := semver.NewVersion(t)
			if err == nil {
				versions = append(versions, v)
			}
		}
		sort.Sort(semver.Collection(versions))

		latest := "-"
		wanted := "-"
		if len(versions) > 0 {
			latest = versions[len(versions)-1].Original()

			// Wanted: highest version matching the current major
			if current != "-" {
				currentV, err := semver.NewVersion(current)
				if err == nil {
					constraint, _ := semver.NewConstraint(fmt.Sprintf("^%d", currentV.Major()))
					for i := len(versions) - 1; i >= 0; i-- {
						if constraint.Check(versions[i]) {
							wanted = versions[i].Original()
							break
						}
					}
				}
			}
		}

		result[features.StripVersionFromFeatureID(id)] = outdatedEntry{
			Current: current, Wanted: wanted, Latest: latest,
		}
	}

	if outputFormat == "json" {
		out := map[string]interface{}{"features": result}
		data, _ := json.MarshalIndent(out, "", "  ")
		fmt.Fprintln(os.Stdout, string(data))
	} else {
		// Text table
		sort.Strings(featureIDs)
		fmt.Printf("%-50s %-12s %-12s %-12s\n", "Feature", "Current", "Wanted", "Latest")
		for _, id := range featureIDs {
			key := features.StripVersionFromFeatureID(id)
			e := result[key]
			// Shorten display ID
			display := key
			if parts := strings.Split(display, "/"); len(parts) > 2 {
				display = strings.Join(parts[len(parts)-2:], "/")
			}
			fmt.Printf("%-50s %-12s %-12s %-12s\n", display, e.Current, e.Wanted, e.Latest)
		}
	}

	return nil
}

func newUpgradeCmd() *cobra.Command {
	var (
		workspaceFolder string
		configPath      string
		dockerPath      string
		composePath     string
		logLevel        string
		dryRun          bool
	)

	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade lockfile",
		RunE: func(cmd *cobra.Command, args []string) error {
			// 0.88: workspace-folder defaults to cwd when not provided.
			if workspaceFolder == "" {
				workspaceFolder, _ = os.Getwd()
			}

			ws := resolvePath(workspaceFolder)
			cp := ""
			if configPath != "" {
				cp = resolvePath(configPath)
			}

			logger := log.New(log.Options{
				Level:  log.MapLogLevel(logLevel),
				Format: "text",
				Writer: os.Stderr,
			})

			loadResult, err := config.LoadDevContainerConfig(ws, cp, "")
			if err != nil {
				return err
			}

			cfg := loadResult.Config

			// Read existing lockfile
			lockfilePath := cfg.ConfigFilePath
			if lockfilePath == "" {
				lockfilePath = filepath.Join(ws, "devcontainer.json")
			}

			// Generate new lockfile from current features config
			// This is a simplified version — the full implementation
			// would resolve all features via OCI and compute digests.
			ociClient := oci.NewClient(logger, osEnvMap())

			featureSets := resolveFeatureSets(cfg, ociClient, logger)
			lf := features.GenerateLockfile(&features.FeaturesConfig{FeatureSets: featureSets}, nil)

			if dryRun {
				data, _ := json.MarshalIndent(lf, "", "  ")
				fmt.Fprintln(os.Stdout, string(data))
				return nil
			}

			err = features.WriteLockfile(lockfilePath, lf, false, true)
			if err != nil {
				return err
			}

			logger.Write("Lockfile updated.", log.LevelInfo)
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&workspaceFolder, "workspace-folder", "", "Workspace folder.")
	f.StringVar(&configPath, "config", "", "devcontainer.json path.")
	f.StringVar(&dockerPath, "docker-path", "docker", "Docker path.")
	f.StringVar(&composePath, "docker-compose-path", "docker-compose", "Compose path.")
	f.StringVar(&logLevel, "log-level", "info", "Log level.")
	f.BoolVar(&dryRun, "dry-run", false, "Write to stdout.")
	f.String("feature", "", "")
	f.String("target-version", "", "")
	_ = dockerPath
	_ = composePath

	addLogFileFlags(cmd)
	return cmd
}

func resolveFeatureSets(cfg *config.DevContainerConfig, ociClient *oci.Client, logger log.Log) []*features.FeatureSet {
	if cfg.Features == nil {
		return nil
	}

	var sets []*features.FeatureSet
	for id, opts := range cfg.Features {
		resolvedID, _ := features.ResolveFeatureID(id, false)
		ref, err := oci.ParseRef(resolvedID)
		if err != nil {
			continue
		}

		manifest, err := ociClient.FetchManifest(ref, "")
		if err != nil {
			continue
		}

		set := &features.FeatureSet{
			SourceInfo: &features.OCISource{
				Registry:       ref.Registry,
				Namespace:      ref.Namespace,
				ID:             ref.ID,
				Resource:       ref.Resource,
				Tag:            ref.Tag,
				ManifestDigest: manifest.ContentDigest,
				UserID:         id,
			},
			Features:       []features.Feature{{ID: ref.ID, Version: ref.Tag, Value: opts}},
			ComputedDigest: manifest.ContentDigest,
		}
		sets = append(sets, set)
	}
	return sets
}
