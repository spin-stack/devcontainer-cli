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
	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/log"
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
		logFile         string
		terminalLogFile string
	)

	cmd := &cobra.Command{
		Use:   "outdated",
		Short: "Show current and available versions",
		RunE: func(cmd *cobra.Command, args []string) error {
			// 0.88: workspace-folder defaults to cwd when not provided.
			if workspaceFolder == "" {
				workspaceFolder, _ = os.Getwd()
			}
			return runOutdated(outputFor(cmd), workspaceFolder, configPath, outputFormat, logLevel, logFormat, logFile, terminalLogFile)
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

	addLogFileFlags(cmd, &logFile, &terminalLogFile)
	return cmd
}

type outdatedEntry struct {
	Current     string `json:"current"`
	Wanted      string `json:"wanted"`
	WantedMajor string `json:"wantedMajor,omitempty"`
	Latest      string `json:"latest"`
	LatestMajor string `json:"latestMajor,omitempty"`
}

// highestSatisfyingTag returns the newest published version a feature tag resolves
// to: for "latest"/empty the newest overall, for a version tag (e.g. "2") the newest
// within that major. versions must be sorted ascending. "" if none match.
func highestSatisfyingTag(versions []*semver.Version, tag string) string {
	if len(versions) == 0 {
		return ""
	}
	if tag == "" || tag == "latest" {
		return versions[len(versions)-1].Original()
	}
	tagV, err := semver.NewVersion(tag)
	if err != nil {
		return ""
	}
	constraint, err := semver.NewConstraint(fmt.Sprintf("^%d", tagV.Major()))
	if err != nil {
		return ""
	}
	for i := len(versions) - 1; i >= 0; i-- {
		if constraint.Check(versions[i]) {
			return versions[i].Original()
		}
	}
	return ""
}

// resolvePublishedVersions fetches and returns a feature ref's published semver
// tags, sorted ascending.
func resolvePublishedVersions(ociClient oci.Registry, ref *oci.Ref) []*semver.Version {
	tags, err := ociClient.GetPublishedTags(ref)
	if err != nil {
		return nil
	}
	var versions []*semver.Version
	for _, t := range tags {
		if v, err := semver.NewVersion(t); err == nil {
			versions = append(versions, v)
		}
	}
	sort.Sort(semver.Collection(versions))
	return versions
}

// majorOf returns the major version of a semver string, or "" if unparseable.
func majorOf(v string) string {
	if v == "" {
		return ""
	}
	parsed, err := semver.NewVersion(v)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d", parsed.Major())
}

func runOutdated(out Output, workspaceFolder, configPath, outputFormat, logLevelStr, logFormatStr, logFile, terminalLogFile string) error {
	logDst, closeLog, logErr := logWriter(logFile, terminalLogFile)
	if logErr != nil {
		return fmt.Errorf("open log file: %w", logErr)
	}
	defer closeLog()

	logger := log.New(log.Options{
		Level:  log.MapLogLevel(logLevelStr),
		Format: logFormatStr,
		Writer: logDst,
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
			fmt.Fprintln(out.Stdout(), `{"features":{}}`)
		} else {
			fmt.Fprintln(out.Stdout(), "No features configured.")
		}
		return nil
	}

	ociClient := oci.NewClient(logger, osEnvMap())

	// Lockfile pins the concrete "current" version when present (matches TS
	// loadVersionInfo: current = lockfileVersion || wanted).
	lockfile, _, _ := features.ReadLockfile(cfg.ConfigFilePath)

	result := make(map[string]outdatedEntry)

	for id := range cfg.Features {
		resolvedID, _ := features.ResolveFeatureID(id, false)

		ref, err := oci.ParseRef(resolvedID)
		if err != nil {
			// Not a versionable OCI feature (local path, etc.) — TS omits these.
			continue
		}
		// Published versions. Features with no versions are omitted (as TS does).
		versions := resolvePublishedVersions(ociClient, ref)
		if len(versions) == 0 {
			continue
		}

		latest := versions[len(versions)-1].Original()
		wanted := highestSatisfyingTag(versions, ref.Tag)

		// current = lockfile version if pinned, else the resolved wanted version.
		current := wanted
		if lockfile != nil {
			if e, ok := lockfile.Features[id]; ok && e.Version != "" {
				current = e.Version
			}
		}

		result[id] = outdatedEntry{
			Current:     current,
			Wanted:      wanted,
			WantedMajor: majorOf(wanted),
			Latest:      latest,
			LatestMajor: majorOf(latest),
		}
	}

	if outputFormat == "json" {
		payload := map[string]interface{}{"features": result}
		data, _ := json.MarshalIndent(payload, "", "  ")
		fmt.Fprintln(out.Stdout(), string(data))
	} else {
		// Text table
		ids := make([]string, 0, len(result))
		for id := range result {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		fmt.Fprintf(out.Stdout(), "%-50s %-12s %-12s %-12s\n", "Feature", "Current", "Wanted", "Latest")
		for _, id := range ids {
			e := result[id]
			// Shorten display ID
			display := id
			if parts := strings.Split(display, "/"); len(parts) > 2 {
				display = strings.Join(parts[len(parts)-2:], "/")
			}
			fmt.Fprintf(out.Stdout(), "%-50s %-12s %-12s %-12s\n", display, e.Current, e.Wanted, e.Latest)
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
		logFile         string
		terminalLogFile string
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

			logDst, closeLog, logErr := logWriter(logFile, terminalLogFile)
			if logErr != nil {
				return fmt.Errorf("open log file: %w", logErr)
			}
			defer closeLog()

			logger := log.New(log.Options{
				Level:  log.MapLogLevel(logLevel),
				Format: "text",
				Writer: logDst,
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
				fmt.Fprintln(outputFor(cmd).Stdout(), string(data))
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
	f.StringP("feature", "f", "", "")
	f.StringP("target-version", "v", "", "")
	_ = f.MarkHidden("feature")        // hidden aliased flag (TS parity: alias -f)
	_ = f.MarkHidden("target-version") // hidden aliased flag (TS parity: alias -v)
	_ = dockerPath
	_ = composePath

	addLogFileFlags(cmd, &logFile, &terminalLogFile)
	return cmd
}

func resolveFeatureSets(cfg *config.DevContainerConfig, ociClient oci.Registry, logger log.Log) []*features.FeatureSet {
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

		// The lockfile records the concrete feature version, not the tag. Prefer the
		// manifest's dev.containers.metadata annotation; if absent (some features
		// don't publish it), resolve the tag to its newest published version — both
		// match the version TS stores.
		version := ref.Tag
		if manifest.Manifest != nil {
			if meta := manifest.Manifest.Annotations["dev.containers.metadata"]; meta != "" {
				var m struct {
					Version string `json:"version"`
				}
				if json.Unmarshal([]byte(meta), &m) == nil && m.Version != "" {
					version = m.Version
				}
			}
		}
		if version == ref.Tag {
			if v := highestSatisfyingTag(resolvePublishedVersions(ociClient, ref), ref.Tag); v != "" {
				version = v
			}
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
			Features:       []features.Feature{{ID: ref.ID, Version: version, Value: opts}},
			ComputedDigest: manifest.ContentDigest,
		}
		sets = append(sets, set)
	}
	return sets
}
