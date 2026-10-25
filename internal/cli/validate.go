package cli

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var mountSpecPattern = regexp.MustCompile(`^type=(bind|volume),source=([^,]+),target=([^,]+)(?:,external=(true|false))?$`)

// validateIDLabels checks that all --id-label values match <name>=<value> format.
func validateIDLabels(labels []string) error {
	for _, l := range labels {
		if !strings.Contains(l, "=") || strings.HasPrefix(l, "=") || strings.HasSuffix(l, "=") {
			return fmt.Errorf("Unmatched argument format: id-label must match <name>=<value>")
		}
	}
	return nil
}

// validateRemoteEnvs checks that all --remote-env values match <name>=<value> format.
func validateRemoteEnvs(envs []string) error {
	for _, e := range envs {
		if !strings.Contains(e, "=") || strings.HasPrefix(e, "=") {
			return fmt.Errorf("Unmatched argument format: remote-env must match <name>=<value>")
		}
	}
	return nil
}

// validateMounts checks that all --mount values match the same format enforced by TS.
func validateMounts(mounts []string) error {
	for _, m := range mounts {
		if !mountSpecPattern.MatchString(m) {
			return fmt.Errorf("Unmatched argument format: mount must match type=<bind|volume>,source=<source>,target=<target>[,external=<true|false>]")
		}
	}
	return nil
}

// validateTerminalImplications checks bidirectional implications between
// terminal-columns and terminal-rows (matches yargs .implies()).
func validateTerminalImplications(columns, rows int) error {
	if columns > 0 && rows == 0 {
		return fmt.Errorf("Implications failed:\n terminal-columns -> terminal-rows")
	}
	if rows > 0 && columns == 0 {
		return fmt.Errorf("Implications failed:\n terminal-rows -> terminal-columns")
	}
	return nil
}

// validateEnum checks that a flag value is one of the allowed choices.
// logLevels and logFormats are the choices TS yargs declares for --log-level
// and --log-format.
var (
	logLevels  = []string{"info", "debug", "trace"}
	logFormats = []string{"text", "json"}
)

// enumFlag is a flag value that must be one of choices.
type enumFlag struct {
	flag, value string
	choices     []string
}

// validateEnums returns the error for the first flag whose value is not one of
// its choices, in the order given (yargs reports the first invalid choice).
func validateEnums(flags ...enumFlag) error {
	for _, f := range flags {
		if err := validateEnum(f.flag, f.value, f.choices); err != nil {
			return err
		}
	}
	return nil
}

func validateEnum(flagName, value string, choices []string) error {
	if slices.Contains(choices, value) {
		return nil
	}
	return fmt.Errorf("Invalid value %q for --%s. Choose from: %s", value, flagName, strings.Join(choices, ", "))
}
