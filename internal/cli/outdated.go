package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/devcontainers/cli/internal/config"
	coreerrors "github.com/devcontainers/cli/internal/errors"
	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/jsonc"
	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
	"github.com/devcontainers/cli/internal/stringify"
	"github.com/spf13/cobra"
)

// bareVersion is a bare semver-ish target: x / x.y / x.y.z
// (TS: /^\d+(\.\d+(\.\d+)?)?$/ in upgradeCommand.ts).
var bareVersion = regexp.MustCompile(`^\d+(\.\d+(\.\d+)?)?$`)

// isBareVersion reports whether s is a bare semver-ish target.
func isBareVersion(s string) bool { return bareVersion.MatchString(s) }

// getFeatureIdWithoutVersion strips the trailing ":tag" or "@digest" from a user
// feature id, leaving the full id path (TS getFeatureIdWithoutVersion, /[:@][^/]*$/).
// The delimiter is the first ':' or '@' after the last '/', so a registry port
// (localhost:5000/...) is preserved and a digest '@' wins over a later ':'.
func getFeatureIdWithoutVersion(featureID string) string {
	start := strings.LastIndexByte(featureID, '/') + 1
	for i := start; i < len(featureID); i++ {
		if featureID[i] == ':' || featureID[i] == '@' {
			return featureID[:i]
		}
	}
	return featureID
}

func newOutdatedCmd() *cobra.Command {
	var opts outdatedOpts

	cmd := &cobra.Command{
		Use:   "outdated",
		Short: "Show current and available versions",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateTerminalImplications(opts.terminalColumns, opts.terminalRows); err != nil {
				return err
			}
			// 0.88: workspace-folder defaults to cwd when not provided.
			if opts.workspaceFolder == "" {
				opts.workspaceFolder, _ = os.Getwd()
			}
			return runOutdated(cmd.Context(), outputFor(cmd), opts)
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.workspaceFolder, "workspace-folder", "", "Workspace folder path.")
	f.StringVar(&opts.configPath, "config", "", "devcontainer.json path.")
	f.StringVar(&opts.outputFormat, "output-format", "text", "Output format.")
	f.StringVar(&opts.logLevel, "log-level", "info", "Log level.")
	f.StringVar(&opts.logFormat, "log-format", "text", "Log format.")
	f.String("user-data-folder", "", "")
	f.Var(&opts.terminalColumns, "terminal-columns", "")
	f.Var(&opts.terminalRows, "terminal-rows", "")

	addLogFileFlags(cmd, &opts.logFile, &opts.terminalLogFile)
	return cmd
}

// outdatedOpts holds the flags for the `outdated` command.
type outdatedOpts struct {
	workspaceFolder string
	configPath      string
	outputFormat    string
	logLevel        string
	logFormat       string
	logFile         string
	terminalLogFile string
	terminalColumns givenInt
	terminalRows    givenInt
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
	for _, version := range slices.Backward(versions) {
		if constraint.Check(version) {
			return version.Original()
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

func runOutdated(ctx context.Context, out Output, opts outdatedOpts) error {
	logDst, closeLog, logErr := logWriter(opts.logFile, opts.terminalLogFile)
	if logErr != nil {
		return fmt.Errorf("open log file: %w", logErr)
	}
	defer closeLog()

	logger := log.New(log.Options{
		Level:  log.ParseLevel(opts.logLevel),
		Format: opts.logFormat,
		Writer: logDst,
	})

	ws := resolvePath(opts.workspaceFolder)
	cp := ""
	if opts.configPath != "" {
		cp = resolvePath(opts.configPath)
	}

	loadResult, err := config.LoadDevContainerConfig(ws, cp, "")
	if err != nil {
		return err
	}

	cfg := loadResult.Config
	if len(cfg.Features) == 0 {
		if opts.outputFormat == "json" {
			fmt.Fprintln(out.Stdout(), `{"features":{}}`)
		} else {
			fmt.Fprintln(out.Stdout(), "No features configured.")
		}
		return nil
	}

	ociClient := newOCIClient(ctx, logger)

	// Lockfile pins the concrete "current" version when present (matches TS
	// loadVersionInfo: current = lockfileVersion || wanted). One that cannot be
	// read fails the command as TS outdated's catch does: the error's stack
	// logged at debug level, nothing on stdout, exit code 1.
	lockfile, _, err := features.ReadLockfile(cfg.ConfigFilePath)
	if err != nil {
		logger.Write(errorStack(err), log.LevelDebug)
		return &coreerrors.ExitCodeError{Code: 1, Err: err}
	}

	result := make(map[string]outdatedEntry)

	for id := range cfg.Features {
		resolvedID, _ := features.ResolveID(id, false)

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

	if opts.outputFormat == "json" {
		payload := map[string]any{"features": result}
		data, _ := stringify.JSON(payload, "  ")
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
func orderedFeatureKeys(configFilePath string, features map[string]any) []string {
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

// upgradeLogLevels are the choices TS featuresUpgradeOptions declares for
// --log-level, which, unlike the other commands', include error.
var upgradeLogLevels = []string{"error", "info", "debug", "trace"}

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
			// --log-level is a yargs choice, validated before .check() runs.
			if err := validateEnum("log-level", logLevel, upgradeLogLevels); err != nil {
				return err
			}
			// --feature/--target-version are the dependabot-oriented flags: they
			// must be supplied together, and --target-version must be a bare
			// x / x.y / x.y.z version. TS validates this in yargs .check() before
			// the handler runs (upgradeCommand.ts), so exit 1 with the same text.
			if (feature != "") != (targetVersion != "") {
				return fmt.Errorf("The '--target-version' and '--feature' flag must be used together.")
			}
			if targetVersion != "" && !isBareVersion(targetVersion) {
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

			loadResult, err := config.LoadDevContainerConfig(ws, cp, "")
			if errors.Is(err, fs.ErrNotExist) {
				// A --config file that does not exist is TS getConfig's "not found".
				return &coreerrors.ContainerError{Description: fmt.Sprintf("Dev container config (%s) not found.", cp)}
			}
			if err != nil {
				return err
			}

			// TS writes its log header with the first event, which every run
			// that gets past loading the configuration logs (at trace level
			// at least), and none that fails to load it.
			logger := log.New(log.Options{
				Version: cliVersion(),
				Level:   log.ParseLevel(logLevel),
				Format:  "text",
				Writer:  logDst,
			})

			cfg := loadResult.Config

			// --feature/--target-version first pin the Feature's version in
			// devcontainer.json, with or without --dry-run, and the lockfile
			// is then generated from the configuration read again.
			if feature != "" {
				logger.Write(fmt.Sprintf("Updating '%s' to '%s' in devcontainer.json", feature, targetVersion), log.LevelInfo)
				if err := updateFeatureVersionInConfig(logger, cfg, feature, targetVersion); err != nil {
					return err
				}
				if loadResult, err = config.LoadDevContainerConfig(ws, cp, ""); err != nil {
					return err
				}
				cfg = loadResult.Config
			}

			// The lockfile sits next to the configuration, whose path a loaded
			// configuration always carries.
			lockfilePath := cfg.ConfigFilePath

			// The Features are resolved as build resolves them, without the
			// lockfile (TS readFeaturesConfig: generateFeaturesConfig with
			// noLockfile): dependencies included, and a Feature that cannot be
			// resolved fails the command. A configuration without Features has
			// no Features config, which TS reports as a failure rather than
			// writing an empty lockfile.
			res, err := fetchFeatureSetsWithOrder(cmd.Context(), logger, cfg.Features, filepath.Dir(lockfilePath), false, nil, nil)
			if err != nil {
				return err
			}
			if res == nil {
				return &coreerrors.ContainerError{Description: "Failed to update lockfile"}
			}
			lf := features.GenerateLockfile(&features.Config{FeatureSets: res.FeatureSets}, nil)

			if dryRun {
				data, _ := stringify.JSON(lf, "  ")
				fmt.Fprintln(outputFor(cmd).Stdout(), string(data))
				return nil
			}

			// TS truncates any existing lockfile before writing it
			// (upgradeCommand.ts), so the new one is always written in its
			// canonical form, even over one that differs only cosmetically.
			if err := os.WriteFile(features.LockfilePath(lockfilePath), nil, 0o644); err != nil {
				return err
			}
			return features.WriteLockfile(lockfilePath, lf, features.WriteOptions{})
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

// updateFeatureVersionInConfig pins the configured Feature that is feature,
// whatever its version, to targetVersion in the configuration file, as TS
// updateFeatureVersionInConfig does: the first Feature key, in the order the
// file lists them, whose id without its version is feature's is replaced,
// read as a regular expression, everywhere in the file's text by
// "<feature without version>:<targetVersion>".
func updateFeatureVersionInConfig(logger log.Logger, cfg *config.DevContainer, feature, targetVersion string) error {
	configPath := cfg.ConfigFilePath
	if cfg.Features == nil {
		logger.Write(fmt.Sprintf("No Features found in '%s'.", configPath), log.LevelDebug)
		return nil
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	previous := string(data)
	updated := previous

	target := getFeatureIdWithoutVersion(feature)
	for _, userFeatureID := range orderedFeatureKeys(configPath, cfg.Features) {
		if getFeatureIdWithoutVersion(userFeatureID) != target {
			continue
		}
		re, err := regexp.Compile(userFeatureID)
		if err != nil {
			return err
		}
		// TS String.replace reads '$' patterns in the replacement, but a
		// replacement holding '$' comes from a key holding '$', which as a
		// pattern only matches at the very end of the text, where a JSON
		// document has its closing brace: it is replaced literally.
		updated = re.ReplaceAllLiteralString(updated, target+":"+targetVersion)
		break
	}

	logger.Write(updated, log.LevelTrace)
	if updated == previous {
		logger.Write(fmt.Sprintf("No changes to config file: %s\n", configPath), log.LevelTrace)
		return nil
	}
	logger.Write(fmt.Sprintf("Updating config file: '%s'", configPath), log.LevelInfo)
	return os.WriteFile(configPath, []byte(updated), 0o644)
}

// errorStack is how the reference logs an error it catches (err.stack, frames
// left out): a lockfile error as the JavaScript error TS throws, any other
// with its message.
func errorStack(err error) string {
	if lerr, ok := errors.AsType[*features.LockfileError](err); ok {
		return lerr.Stack()
	}
	return err.Error()
}
