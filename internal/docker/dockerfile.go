package docker

import (
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
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

	df.Stages = make([]Stage, 0, len(stages))
	for i := range stages {
		s := &stages[i]
		st := Stage{From: From{Platform: s.Platform, Image: trimQuotes(s.BaseName), Label: s.Name}}
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

// mapEnv is a shell.EnvGetter backed by a plain map.
type mapEnv map[string]string

func (m mapEnv) Get(k string) (string, bool) { v, ok := m[k]; return v, ok }
func (m mapEnv) Keys() []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

// FindBaseImage resolves the base image for a target stage (or the last stage),
// following FROM <stage> references to the root image. ${VAR} / ${VAR:+x} /
// ${VAR:-x} in the FROM line are expanded with BuildKit's shell lexer against the
// global (preamble) ARGs plus --build-arg overrides — the same env Docker uses to
// resolve a FROM line.
func FindBaseImage(df *Dockerfile, buildArgs map[string]string, target string) string {
	var stage *Stage
	if target != "" {
		stage = df.StagesByLabel[target]
	} else if len(df.Stages) > 0 {
		stage = &df.Stages[len(df.Stages)-1]
	}

	env := mapEnv{}
	for _, ins := range df.Preamble.Instructions {
		if ins.Instruction == "ARG" {
			v := ins.Value
			if ov, ok := buildArgs[ins.Name]; ok {
				v = ov
			}
			env[ins.Name] = v
		}
	}

	lex := shell.NewLex(df.escapeToken)
	seen := make(map[*Stage]bool)
	for stage != nil {
		if seen[stage] {
			return ""
		}
		seen[stage] = true

		image := stage.From.Image
		if out, _, err := lex.ProcessWord(image, env); err == nil {
			image = trimQuotes(out)
		}
		if next, ok := df.StagesByLabel[image]; ok {
			stage = next
		} else {
			return image
		}
	}
	return ""
}

// EnsureFinalStageName returns the name of the final build stage, adding an
// "AS <defaultName>" to its FROM line when it has none (so features can extend a
// known stage). The modified Dockerfile is returned only when a name was added;
// otherwise modifiedContent is empty.
func EnsureFinalStageName(content, defaultName string) (stageName string, modifiedContent string) {
	// Primary detection via BuildKit (continuation-safe). Trust a positive name;
	// on a parse error (e.g. a trailing inline comment the strict parser rejects
	// but the TS tolerates) or no name, fall through to the tolerant scan below.
	if res, err := parser.Parse(strings.NewReader(content)); err == nil {
		if stages, _, perr := instructions.Parse(res.AST, nil); perr == nil && len(stages) > 0 {
			if name := stages[len(stages)-1].Name; name != "" {
				return name, ""
			}
		}
	}

	// Tolerant fallback: locate the last line whose first token is FROM and inspect
	// it by hand (no regex). If it already has an "AS <label>" return it; otherwise
	// inject " AS <defaultName>" right after the base image, preserving the rest of
	// the line (trailing comment/whitespace).
	lineStart, fromStart, fromLine, found := 0, 0, "", false
	for _, l := range strings.SplitAfter(content, "\n") {
		if isFromLine(l) {
			fromStart = lineStart
			fromLine = strings.TrimRight(l, "\n")
			found = true
		}
		lineStart += len(l)
	}
	if !found {
		return defaultName, content
	}
	label, imageEnd, ok := parseFromLine(fromLine)
	if !ok {
		return defaultName, content
	}
	if label != "" {
		return label, ""
	}
	pos := fromStart + imageEnd
	return defaultName, content[:pos] + " AS " + defaultName + content[pos:]
}

func isFromLine(line string) bool {
	t := strings.TrimLeft(line, " \t")
	if len(t) < 4 || !strings.EqualFold(t[:4], "FROM") {
		return false
	}
	return len(t) == 4 || t[4] == ' ' || t[4] == '\t' || t[4] == '\n' || t[4] == '\r'
}

// parseFromLine tokenizes "FROM [--platform=..] <image> [AS <label>] ..." on a
// single line. It returns the label (empty if none) and the byte offset just past
// the image, so a caller can inject " AS name" there. ok is false when the line
// has no image.
func parseFromLine(line string) (label string, imageEnd int, ok bool) {
	i := 0
	readTok := func() (string, int) {
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		start := i
		for i < len(line) && line[i] != ' ' && line[i] != '\t' {
			i++
		}
		return line[start:i], i
	}
	if kw, _ := readTok(); !strings.EqualFold(kw, "FROM") {
		return "", 0, false
	}
	tok, end := readTok()
	if strings.HasPrefix(tok, "--") { // optional --platform flag
		tok, end = readTok()
	}
	if tok == "" {
		return "", 0, false
	}
	imageEnd = end
	if as, _ := readTok(); strings.EqualFold(as, "AS") {
		name, _ := readTok()
		return name, imageEnd, true
	}
	return "", imageEnd, true
}
