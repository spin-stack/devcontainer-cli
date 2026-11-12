package cli

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var mountSpecPattern = regexp.MustCompile(`^type=(bind|volume),source=([^,]+),target=([^,]+)(?:,external=(true|false))?$`)

func validateIDLabels(labels []string) error {
	for _, l := range labels {
		if !strings.Contains(l, "=") || strings.HasPrefix(l, "=") || strings.HasSuffix(l, "=") {
			return fmt.Errorf("Unmatched argument format: id-label must match <name>=<value>")
		}
	}
	return nil
}

func validateRemoteEnvs(envs []string) error {
	for _, e := range envs {
		if !strings.Contains(e, "=") || strings.HasPrefix(e, "=") {
			return fmt.Errorf("Unmatched argument format: remote-env must match <name>=<value>")
		}
	}
	return nil
}

func validateMounts(mounts []string) error {
	for _, m := range mounts {
		if !mountSpecPattern.MatchString(m) {
			return fmt.Errorf("Unmatched argument format: mount must match type=<bind|volume>,source=<source>,target=<target>[,external=<true|false>]")
		}
	}
	return nil
}

func validateTerminalImplications(columns, rows givenInt) error {
	if columns.given && !rows.given {
		return fmt.Errorf("Implications failed:\n terminal-columns -> terminal-rows")
	}
	if rows.given && !columns.given {
		return fmt.Errorf("Implications failed:\n terminal-rows -> terminal-columns")
	}
	return nil
}

type givenInt struct {
	value int
	given bool
}

func (g *givenInt) Set(s string) error {
	v, err := strconv.ParseInt(s, 0, strconv.IntSize)
	if err != nil {
		return err
	}
	g.value, g.given = int(v), true
	return nil
}

func (g *givenInt) String() string { return strconv.Itoa(g.value) }

func (g *givenInt) Type() string { return "int" }

var (
	logLevels  = []string{"info", "debug", "trace"}
	logFormats = []string{"text", "json"}
)

type enumFlag struct {
	flag, value string
	choices     []string
}

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
