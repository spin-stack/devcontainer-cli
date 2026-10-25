package docker

import (
	"errors"
	"regexp"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
)

// Dockerfile is a parsed Dockerfile projected from BuildKit's AST into the small
// shape the CLI needs (base-image resolution, final-stage naming).
type Dockerfile struct {
	Preamble      Preamble
	Stages        []Stage
	StagesByLabel map[string]*Stage
	escapeToken   rune
}

// Preamble is the content before any FROM statement.
type Preamble struct {
	Version      string // tag from # syntax=docker/dockerfile:X.Y (or "latest")
	Directives   map[string]string
	Instructions []Instruction // the global ARGs
}

// Stage is a single FROM ... block.
type Stage struct {
	From         From
	Instructions []Instruction
}

// From is a parsed FROM statement.
type From struct {
	Platform string
	Image    string
	Label    string
}

// Instruction is a parsed ARG, ENV, or USER statement.
type Instruction struct {
	Instruction string // "ARG", "ENV", "USER"
	Name        string
	Value       string
}

// ExtractDockerfile parses a Dockerfile with BuildKit's real parser (line
// continuations, comments, heredocs and escapes handled correctly) and projects
// it into the internal structs. Surrounding quotes are trimmed from images and
// ARG/ENV values so downstream comparisons see the unquoted form.
func ExtractDockerfile(content string) *Dockerfile {
	df := &Dockerfile{
		Preamble:      Preamble{Directives: map[string]string{}},
		StagesByLabel: map[string]*Stage{},
		escapeToken:   '\\',
	}

	res, err := parser.Parse(strings.NewReader(content))
	if err != nil {
		return df
	}
	df.escapeToken = res.EscapeToken
	if syntax, _, _, ok := parser.DetectSyntax([]byte(content)); ok {
		df.Preamble.Directives["syntax"] = syntax
		df.Preamble.Version = syntaxVersion(syntax)
	}

	stages, metaArgs, err := instructions.Parse(res.AST, nil)
	if err != nil {
		return df
	}

	for _, ma := range metaArgs {
		for _, kv := range ma.Args {
			df.Preamble.Instructions = append(df.Preamble.Instructions, Instruction{
				Instruction: "ARG", Name: kv.Key, Value: derefTrim(kv.Value),
			})
		}
	}

	for i := range stages {
		s := &stages[i]
		st := Stage{From: From{Platform: s.Platform, Image: trimQuotes(s.BaseName), Label: stageLabel(s)}}
		for _, cmd := range s.Commands {
			switch c := cmd.(type) {
			case *instructions.EnvCommand:
				for _, kv := range c.Env {
					st.Instructions = append(st.Instructions, Instruction{Instruction: "ENV", Name: kv.Key, Value: trimQuotes(kv.Value)})
				}
			case *instructions.ArgCommand:
				for _, kv := range c.Args {
					st.Instructions = append(st.Instructions, Instruction{Instruction: "ARG", Name: kv.Key, Value: derefTrim(kv.Value)})
				}
			case *instructions.UserCommand:
				st.Instructions = append(st.Instructions, Instruction{Instruction: "USER", Name: c.User})
			case *instructions.LabelCommand:
				for _, kv := range c.Labels {
					st.Instructions = append(st.Instructions, Instruction{Instruction: "LABEL", Name: kv.Key, Value: trimQuotes(kv.Value)})
				}
			}
		}
		df.Stages = append(df.Stages, st)
	}
	for i := range df.Stages {
		if df.Stages[i].From.Label != "" {
			df.StagesByLabel[df.Stages[i].From.Label] = &df.Stages[i]
		}
	}
	return df
}

// stageLabel is the stage's name as the FROM line spells it: BuildKit
// lowercases it, TS extractDockerfile keeps it, and looks stages up by it.
func stageLabel(s *instructions.Stage) string {
	if m := fromLineLabel.FindStringSubmatch(s.SourceCode); m != nil && strings.EqualFold(m[1], s.Name) {
		return m[1]
	}
	return s.Name
}

// StageLabels returns the LABEL key/value pairs declared in the named build
// stage (or the final stage when name is ""), with later labels overriding
// earlier ones. Used to recover a user's `LABEL devcontainer.metadata` from the
// Dockerfile so the CLI-stamped metadata label does not clobber it.
func (d *Dockerfile) StageLabels(name string) map[string]string {
	var st *Stage
	if name != "" {
		st = d.StagesByLabel[name]
	}
	if st == nil && len(d.Stages) > 0 {
		st = &d.Stages[len(d.Stages)-1]
	}
	if st == nil {
		return nil
	}
	labels := map[string]string{}
	for _, ins := range st.Instructions {
		if ins.Instruction == "LABEL" {
			labels[ins.Name] = ins.Value
		}
	}
	return labels
}

// syntaxVersion extracts the tag of a docker/dockerfile syntax directive
// ("docker/dockerfile:1.4" -> "1.4", no tag -> "latest", other frontend -> "").
func syntaxVersion(syntax string) string {
	s := strings.TrimPrefix(syntax, "docker.io/")
	if !strings.HasPrefix(s, "docker/dockerfile") {
		return ""
	}
	rest := strings.TrimPrefix(s, "docker/dockerfile")
	if strings.HasPrefix(rest, ":") {
		if tag := strings.Fields(rest[1:]); len(tag) > 0 {
			return tag[0]
		}
	}
	return "latest"
}

func trimQuotes(s string) string { return strings.Trim(s, `"'`) }

func derefTrim(p *string) string {
	if p == nil {
		return ""
	}
	return trimQuotes(*p)
}

// FindBaseImage is TS findBaseImage: the image the target stage (or the last
// one) is built from, with the --build-arg values and the automatic BUILD*/
// TARGET* platform ARGs (platformArgs, TS globalBuildxPlatformArgs), following
// FROM <stage> to the first image that is no stage; "" when the target is
// unknown or the stages form a cycle.
func FindBaseImage(df *Dockerfile, buildArgs map[string]string, target string, platformArgs map[string]string) string {
	return NewResolver(df, buildArgs, platformArgs).FindBaseImage(target)
}

// jsSpace is the JavaScript regular expression class \s (Go's \s lacks \v and
// the Unicode spaces), so the ports of TS regular expressions match the same text.
const jsSpace = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

var (
	// findFromLines is TS dockerfileUtils' /^(?<line>\s*FROM.*)/gmi; JS's . stops
	// at \r, \u2028 and \u2029 as well as \n.
	findFromLines = regexp.MustCompile(`(?mi)^[` + jsSpace + `]*FROM[^\n\r\x{2028}\x{2029}]*`)
	// fromLineLabel is TS dockerfileUtils' parseFromLine:
	// /FROM\s+(?<platform>--platform=\S+\s+)?(?<image>"?[^\s]+"?)(\s+AS\s+(?<label>[^\s]+))?/i.
	fromLineLabel = regexp.MustCompile(`(?i)FROM[` + jsSpace + `]+(?:--platform=[^` + jsSpace + `]+[` + jsSpace + `]+)?"?[^` + jsSpace + `]+"?(?:[` + jsSpace + `]+AS[` + jsSpace + `]+([^` + jsSpace + `]+))?`)
)

// EnsureFinalStageName is TS ensureDockerfileHasFinalStageName: the name of the
// final build stage, adding " AS <defaultName>" to the last FROM line when it
// has none (so features can extend a known stage). The modified Dockerfile is
// returned only when a name was added; otherwise modifiedContent is empty. A
// Dockerfile without a FROM line, or whose last FROM line does not parse, is
// TS's error.
//
// One deliberate divergence (docs/DIVERGENCES.md): when the last FROM line is
// continued with a backslash, TS writes " AS <defaultName>" after the
// backslash, which no builder accepts; this reads the stage's name with
// BuildKit instead when it has one.
func EnsureFinalStageName(content, defaultName string) (stageName string, modifiedContent string, err error) {
	lines := findFromLines.FindAllStringIndex(content, -1)
	if len(lines) == 0 {
		return "", "", errors.New("Error parsing Dockerfile: Dockerfile contains no FROM instructions")
	}
	lineStart, lineEnd := lines[len(lines)-1][0], lines[len(lines)-1][1]
	if strings.HasSuffix(strings.TrimRight(content[lineStart:lineEnd], " \t"), `\`) {
		if name := finalStageName(content); name != "" {
			return name, "", nil
		}
	}
	m := fromLineLabel.FindStringSubmatchIndex(content[lineStart:lineEnd])
	if m == nil {
		return "", "", errors.New("Error parsing Dockerfile: failed to parse final FROM line")
	}
	if m[2] != -1 { // the label group, when it matched, is never empty
		return content[lineStart+m[2] : lineStart+m[3]], "", nil
	}
	pos := lineStart + m[1]
	return defaultName, content[:pos] + " AS " + defaultName + content[pos:], nil
}

// finalStageName is the name BuildKit reads for the last stage, "" when it has
// none or the Dockerfile does not parse.
func finalStageName(content string) string {
	res, err := parser.Parse(strings.NewReader(content))
	if err != nil {
		return ""
	}
	stages, _, err := instructions.Parse(res.AST, nil)
	if err != nil || len(stages) == 0 {
		return ""
	}
	return stages[len(stages)-1].Name
}
