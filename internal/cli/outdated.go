package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/jsonc"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
	"github.com/spf13/cobra"
)

// bareVersionRe matches a bare semver-ish target: x / x.y / x.y.z
// (TS: /^\d+(\.\d+(\.\d+)?)?$/ in upgradeCommand.ts).
var bareVersionRe = regexp.MustCompile(`^\d+(\.\d+(\.\d+)?)?$`)

// lastVersionDelimiter matches the trailing ":tag" or "@digest" of a feature id
// (TS getFeatureIdWithoutVersion: /[:@][^/]*$/).
var lastVersionDelimiter = regexp.MustCompile(`[:@][^/]*$`)

// getFeatureIdWithoutVersion strips the trailing version tag or digest from a
// user feature id, leaving the full id path (matches TS getFeatureIdWithoutVersion).
func getFeatureIdWithoutVersion(featureID string) string {
	if loc := lastVersionDelimiter.FindStringIndex(featureID); loc != nil {
		return featureID[:loc[0]]
	}
	return featureID
}

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
	if len(cfg.Features) == 0 {
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
		// Text table. Rows follow the order features were declared in the config
		// (TS reorders resolved.features back to config order), and the Feature
		// column shows the full user id without its version tag/digest. Formatting
		// mirrors npm's text-table (min column widths, 2-space separator, left
		// aligned, trailing whitespace stripped per row).
		rows := [][]string{{"Feature", "Current", "Wanted", "Latest"}}
		for _, id := range orderedFeatureKeys(cfg.ConfigFilePath, cfg.Features) {
			e, ok := result[id]
			if !ok {
				continue
			}
			rows = append(rows, []string{
				getFeatureIdWithoutVersion(id),
				dashIfEmpty(e.Current),
				dashIfEmpty(e.Wanted),
				dashIfEmpty(e.Latest),
			})
		}
		fmt.Fprintln(out.Stdout(), textTable(rows))
	}

	return nil
}

// dashIfEmpty renders an empty column value as "-" (TS maps undefined → '-').
func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// orderedFeatureKeys returns the feature ids in the order they appear in the
// devcontainer.json (Go maps are unordered, so the raw file is re-read). Any key
// present in features but not recovered from the file is appended sorted, so the
// caller never silently drops a feature.
func orderedFeatureKeys(configFilePath string, features map[string]interface{}) []string {
	ordered, _ := featureKeyOrderFromFile(configFilePath)

	seen := make(map[string]bool, len(ordered))
	result := make([]string, 0, len(features))
	for _, k := range ordered {
		if _, ok := features[k]; ok && !seen[k] {
			seen[k] = true
			result = append(result, k)
		}
	}
	if len(result) < len(features) {
		var rest []string
		for k := range features {
			if !seen[k] {
				rest = append(rest, k)
			}
		}
		sort.Strings(rest)
		result = append(result, rest...)
	}
	return result
}

// featureKeyOrderFromFile parses configFilePath and returns the keys of its
// top-level "features" object in document order.
func featureKeyOrderFromFile(configFilePath string) ([]string, error) {
	if configFilePath == "" {
		return nil, nil
	}
	data, err := os.ReadFile(configFilePath)
	if err != nil {
		return nil, err
	}
	std, err := jsonc.StripComments(data)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(std))
	// Opening brace of the top-level object.
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := keyTok.(string)
		if key != "features" {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil, err
			}
			continue
		}
		// "features" value must be an object; read its keys in order.
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		if d, ok := tok.(json.Delim); !ok || d != '{' {
			return nil, nil
		}
		var keys []string
		for dec.More() {
			featTok, err := dec.Token()
			if err != nil {
				return nil, err
			}
			if fk, ok := featTok.(string); ok {
				keys = append(keys, fk)
			}
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil, err
			}
		}
		return keys, nil
	}
	return nil, nil
}

// textTable formats rows the way npm's text-table does: each column padded to
// its max cell width, cells separated by two spaces, left-aligned, with trailing
// whitespace trimmed from each line.
func textTable(rows [][]string) string {
	var widths []int
	for _, row := range rows {
		for i, cell := range row {
			if i >= len(widths) {
				widths = append(widths, 0)
			}
			if len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}

	lines := make([]string, 0, len(rows))
	for _, row := range rows {
		var b strings.Builder
		for i, cell := range row {
			if i > 0 {
				b.WriteString("  ")
			}
			b.WriteString(cell)
			if pad := widths[i] - len(cell); pad > 0 {
				b.WriteString(strings.Repeat(" ", pad))
			}
		}
		lines = append(lines, strings.TrimRight(b.String(), " "))
	}
	return strings.Join(lines, "\n")
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
		feature         string
		targetVersion   string
	)

	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade lockfile",
		RunE: func(cmd *cobra.Command, args []string) error {
			// --feature/--target-version are the dependabot-oriented flags: they
			// must be supplied together, and --target-version must be a bare
			// x / x.y / x.y.z version. TS validates this in yargs .check() before
			// the handler runs (upgradeCommand.ts), so exit 1 with the same text.
			if (feature != "") != (targetVersion != "") {
				return fmt.Errorf("The '--target-version' and '--feature' flag must be used together.")
			}
			if targetVersion != "" && !bareVersionRe.MatchString(targetVersion) {
				return fmt.Errorf("Invalid version '%s'.  Must be in the form of 'x', 'x.y', or 'x.y.z'", targetVersion)
			}

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
	f.StringVarP(&feature, "feature", "f", "", "")
	f.StringVarP(&targetVersion, "target-version", "v", "", "")
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
