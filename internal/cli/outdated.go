package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"golang.org/x/term"
)

var bareVersion = regexp.MustCompile(`^\d+(\.\d+(\.\d+)?)?$`)

func isBareVersion(s string) bool { return bareVersion.MatchString(s) }

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
	Current     string `json:"current,omitempty"`
	Wanted      string `json:"wanted,omitempty"`
	WantedMajor string `json:"wantedMajor,omitempty"`
	Latest      string `json:"latest,omitempty"`
	LatestMajor string `json:"latestMajor,omitempty"`
}

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

var outdatedHeader = []string{"Feature", "Current", "Wanted", "Latest"}

func writeOutdatedJSON(w io.Writer, payload map[string]any) {
	data, _ := stringify.JSON(payload, outdatedIndent(w))
	fmt.Fprintln(w, string(data))
}

func outdatedIndent(w io.Writer) string {
	if f, ok := w.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return "  "
	}
	return ""
}

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
			writeOutdatedJSON(out.Stdout(), map[string]any{"features": map[string]any{}})
		} else {
			fmt.Fprintln(out.Stdout(), textTable([][]string{outdatedHeader}))
		}
		return nil
	}

	ociClient := newOCIClient(ctx, logger)

	lockfile, _, err := features.ReadLockfile(cfg.ConfigFilePath)
	if err != nil {
		logger.Write(errorStack(err), log.LevelDebug)
		return &coreerrors.ExitCodeError{Code: 1, Err: err}
	}

	result := make(map[string]outdatedEntry)

	for id := range cfg.Features {
		ref, err := oci.ParseRef(id)
		if err != nil {
			continue
		}
		versions := resolvePublishedVersions(ociClient, ref)

		var latest string
		if len(versions) > 0 {
			latest = versions[len(versions)-1].Original()
		}
		wanted := highestSatisfyingTag(versions, ref.Tag)

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
		fields := make(map[string]any, len(result))
		for id, e := range result {
			fields[id] = e
		}
		ordered := orderedObject{fields: fields, order: orderedFeatureKeys(cfg.ConfigFilePath, cfg.Features)}
		writeOutdatedJSON(out.Stdout(), map[string]any{"features": ordered})
	} else {
		rows := [][]string{outdatedHeader}
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

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

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
			if err := validateEnum("log-level", logLevel, upgradeLogLevels); err != nil {
				return err
			}
			if (feature != "") != (targetVersion != "") {
				return fmt.Errorf("The '--target-version' and '--feature' flag must be used together.")
			}
			if targetVersion != "" && !isBareVersion(targetVersion) {
				return fmt.Errorf("Invalid version '%s'.  Must be in the form of 'x', 'x.y', or 'x.y.z'", targetVersion)
			}

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
				return &coreerrors.ContainerError{Description: fmt.Sprintf("Dev container config (%s) not found.", cp)}
			}
			if err != nil {
				return err
			}

			logger := log.New(log.Options{
				Version: cliVersion(),
				Level:   log.ParseLevel(logLevel),
				Format:  "text",
				Writer:  logDst,
			})

			cfg := loadResult.Config

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

			lockfilePath := cfg.ConfigFilePath

			res, err := fetchFeatureSetsWithOrder(cmd.Context(), logger, cfg.Features, cfg.FeatureOrder, cfg.FeatureOptionOrder, localFeatureRoots{filepath.Dir(lockfilePath), ws}, nil, nil)
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

			if err := os.WriteFile(features.LockfilePath(lockfilePath), nil, 0o666); err != nil {
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
	_ = f.MarkHidden("feature")
	_ = f.MarkHidden("target-version")
	_ = dockerPath
	_ = composePath

	addLogFileFlags(cmd, &logFile, &terminalLogFile)
	return cmd
}

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

func errorStack(err error) string {
	if lerr, ok := errors.AsType[*features.LockfileError](err); ok {
		return lerr.Stack()
	}
	return err.Error()
}
