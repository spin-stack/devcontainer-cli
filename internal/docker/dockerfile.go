package docker

import (
	"errors"
	"regexp"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
)

type Dockerfile struct {
	Preamble      Preamble
	Stages        []Stage
	StagesByLabel map[string]*Stage
}

type Preamble struct {
	Version      string
	Directives   map[string]string
	Instructions []Instruction
}

type Stage struct {
	From         From
	Instructions []Instruction
}

type From struct {
	Platform string
	Image    string
	Label    string
}

type Instruction struct {
	Instruction string
	Name        string
	Value       string
}

func ExtractDockerfile(content string) *Dockerfile {
	df := &Dockerfile{
		Preamble:      Preamble{Directives: map[string]string{}},
		StagesByLabel: map[string]*Stage{},
	}

	res, err := parser.Parse(strings.NewReader(content))
	if err != nil {
		return df
	}
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
// lowercases it, TS extractDockerfile keeps it, and looks stages up by it. On
// a FROM line BuildKit accepts (continuations joined), the FROM-line
// expression reads the label BuildKit names.
func stageLabel(s *instructions.Stage) string {
	if m := fromLineLabel.FindStringSubmatch(s.SourceCode); m != nil {
		return m[1]
	}
	return s.Name
}

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
	fromLineLabel = regexp.MustCompile(`(?i)FROM[` + jsSpace + `]+(?:--platform=[^` + jsSpace + `]+[` + jsSpace + `]+)?"?[^` + jsSpace + `]+"?(?:[` + jsSpace + `]+AS[` + jsSpace + `]+([^` + jsSpace + `]+))?`)
)

func EnsureFinalStageName(content, defaultName string) (stageName string, modifiedContent string, err error) {
	lines := findFromLines.FindAllStringIndex(content, -1) // mutate-exempt: any n < 0 is every match
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
	if m[2] != -1 { // mutate-exempt: m[2] and m[3] are both -1 exactly when the label group is unmatched, and a matched label is never empty
		return content[lineStart+m[2] : lineStart+m[3]], "", nil
	}
	pos := lineStart + m[1]
	return defaultName, content[:pos] + " AS " + defaultName + content[pos:], nil
}

func finalStageName(content string) string {
	res, err := parser.Parse(strings.NewReader(content))
	if err != nil {
		return ""
	}
	if stages, _, _ := instructions.Parse(res.AST, nil); len(stages) > 0 {
		return stages[len(stages)-1].Name
	}
	return ""
}
