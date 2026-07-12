package docker

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/moby/buildkit/frontend/dockerfile/instructions"
	"github.com/moby/buildkit/frontend/dockerfile/parser"
)

// Dockerfile represents a parsed Dockerfile.
type Dockerfile struct {
	Preamble      Preamble
	Stages        []Stage
	StagesByLabel map[string]*Stage
}

// Preamble is the content before any FROM statement.
type Preamble struct {
	Version      string // from # syntax=docker/dockerfile:X.Y
	Directives   map[string]string
	Instructions []Instruction
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

var (
	// Used only by EnsureFinalStageName's string injection (not semantic parsing).
	findFromLines = regexp.MustCompile(`(?mi)^(\s*FROM.*)`)
	parseFromLine = regexp.MustCompile(`(?i)FROM\s+(?:--platform=\S+\s+)?("?[^\s]+"?)(?:\s+AS\s+(\S+))?`)
	// Used by replaceVariables for ${VAR} / ${VAR:+x} / ${VAR:-x} expansion.
	argExpression = regexp.MustCompile(`\$\{?([a-zA-Z0-9_]+)(?::([+-])([^}]+))?\}?`)
	// SupportsBuildContexts: is the syntax directive a docker/dockerfile frontend,
	// and its version tag (group 1, empty when no tag)?
	dockerfileSyntaxRe = regexp.MustCompile(`(?i)^(?:docker\.io/)?docker/dockerfile(?::(\S+))?`)
	numVersionRe       = regexp.MustCompile(`^\d+(\.\d+){0,2}`)
)

// ExtractDockerfile parses a Dockerfile using BuildKit's real parser (which
// handles line continuations, comments, heredocs and escapes correctly, unlike a
// line-anchored regex) and projects it into the internal structs the resolution
// logic (FindBaseImage/findUserStatement) already operates on. Quote handling is
// reproduced (surrounding quotes trimmed) so that resolution behaves as before.
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
		if m := dockerfileSyntaxRe.FindStringSubmatch(syntax); m != nil {
			if m[1] != "" {
				df.Preamble.Version = m[1]
			} else {
				df.Preamble.Version = "latest"
			}
		}
	}

	stages, metaArgs, err := instructions.Parse(res.AST, nil)
	if err != nil {
		return df
	}

	// Preamble (global) ARGs, declared before the first FROM.
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
		st := Stage{From: From{
			Platform: s.Platform,
			Image:    trimQuotes(s.BaseName),
			Label:    s.Name,
		}}
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

// trimQuotes removes a single layer of surrounding single/double quotes, matching
// how the previous regex parser normalized image names and ARG/ENV values.
func trimQuotes(s string) string { return strings.Trim(s, `"'`) }

func derefTrim(p *string) string {
	if p == nil {
		return ""
	}
	return trimQuotes(*p)
}

// FindBaseImage resolves the base image for a target stage (or last stage if target is empty).
func FindBaseImage(df *Dockerfile, buildArgs map[string]string, target string) string {
	var stage *Stage
	if target != "" {
		stage = df.StagesByLabel[target]
	} else if len(df.Stages) > 0 {
		stage = &df.Stages[len(df.Stages)-1]
	}

	seen := make(map[*Stage]bool)
	for stage != nil {
		if seen[stage] {
			return ""
		}
		seen[stage] = true

		image := replaceVariables(df, buildArgs, map[string]string{}, stage.From.Image, &df.Preamble, len(df.Preamble.Instructions))
		if next, ok := df.StagesByLabel[image]; ok {
			stage = next
		} else {
			return image
		}
	}
	return ""
}

// findUserStatement finds the last USER statement in the target stage or its base stages.
func findUserStatement(df *Dockerfile, buildArgs, baseImageEnv map[string]string, target string) string {
	var stage *Stage
	if target != "" {
		stage = df.StagesByLabel[target]
	} else if len(df.Stages) > 0 {
		stage = &df.Stages[len(df.Stages)-1]
	}

	seen := make(map[*Stage]bool)
	for stage != nil {
		if seen[stage] {
			return ""
		}
		seen[stage] = true

		// Find last USER in this stage
		for i := len(stage.Instructions) - 1; i >= 0; i-- {
			if stage.Instructions[i].Instruction == "USER" {
				val := replaceVariables(df, buildArgs, baseImageEnv, stage.Instructions[i].Name, stage, i)
				if val != "" {
					return val
				}
			}
		}

		// Look in parent stage
		image := replaceVariables(df, buildArgs, baseImageEnv, stage.From.Image, &df.Preamble, len(df.Preamble.Instructions))
		stage = df.StagesByLabel[image]
	}
	return ""
}

// EnsureFinalStageName adds an AS label to the last FROM if missing.
func EnsureFinalStageName(content, defaultName string) (stageName string, modifiedContent string) {
	// Primary detection via BuildKit's parser: it correctly reports the final
	// stage's name even across line continuations, where a line-anchored regex
	// mis-reads the FROM. Only trust a POSITIVE name here; on a parse error (e.g. a
	// trailing inline comment the strict parser rejects but the TS tolerates) or no
	// name, fall through to the lenient regex path below.
	if res, err := parser.Parse(strings.NewReader(content)); err == nil {
		if stages, _, perr := instructions.Parse(res.AST, nil); perr == nil && len(stages) > 0 {
			if name := stages[len(stages)-1].Name; name != "" {
				return name, "" // already named — no modification
			}
		}
	}

	// Fallback (tolerant, matching the TS regex): find the last FROM line, and if
	// it already carries an "AS <label>" return it; otherwise inject " AS <name>"
	// after the base image, preserving the rest of the line (trailing comment).
	matches := findFromLines.FindAllStringIndex(content, -1)
	if len(matches) == 0 {
		return defaultName, content
	}
	lastMatch := matches[len(matches)-1]
	lastFromLine := content[lastMatch[0]:lastMatch[1]]
	m := parseFromLine.FindStringSubmatch(lastFromLine)
	if m == nil {
		return defaultName, content
	}
	if m[2] != "" {
		return m[2], "" // already labeled
	}
	matchedPart := m[0]
	insertPos := lastMatch[0] + strings.Index(lastFromLine, matchedPart) + len(matchedPart)
	return defaultName, content[:insertPos] + " AS " + defaultName + content[insertPos:]
}

// SupportsBuildContexts checks if the Dockerfile syntax supports --build-context.
// Returns true, false, or "unknown" (as bool + string).
func SupportsBuildContexts(df *Dockerfile) (supported bool, unknown bool) {
	syntax, ok := df.Preamble.Directives["syntax"]
	if !ok {
		return false, false // no syntax directive
	}
	m := dockerfileSyntaxRe.FindStringSubmatch(syntax)
	if m == nil {
		return false, true // a syntax directive, but not docker/dockerfile → unknown
	}
	numVersion := numVersionRe.FindString(m[1]) // m[1] is the tag ("" when no tag)
	if numVersion == "" {
		return true, false // "latest", "labs", no specific tag → assume yes
	}
	// TS uses semver.intersects(numVersion, ">=1.4"): a partial tag like "1" is a
	// RANGE (1.x, i.e. the floating latest 1.x), NOT the exact 1.0.0, so it
	// intersects >=1.4 and supports build contexts. Parsing "1" as 1.0.0 (exact)
	// would wrongly report false.
	return intersectsAtLeast14(numVersion), false
}

// intersectsAtLeast14 reports whether the version range implied by a partial or
// full semver tag overlaps [1.4.0, ∞) — replicating node-semver's
// intersects(numVersion, ">=1.4"). A 1-part "N" spans [N, N+1); a 2-part "N.M"
// spans [N.M, N.M+1); a 3-part tag is exact.
func intersectsAtLeast14(numVersion string) bool {
	parts := strings.Split(numVersion, ".")
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	major := atoi(parts[0])
	switch len(parts) {
	case 1:
		return major >= 1
	case 2:
		return major > 1 || (major == 1 && atoi(parts[1]) >= 4)
	default:
		if major != 1 {
			return major > 1
		}
		return atoi(parts[1]) >= 4
	}
}

// --- Variable replacement (matching TS dockerfileUtils.ts) ---

type instructionHolder interface {
	getFrom() *From
	getInstructions() []Instruction
}

// Stage implements instructionHolder
func (s *Stage) getFrom() *From                 { return &s.From }
func (s *Stage) getInstructions() []Instruction { return s.Instructions }

// Preamble implements instructionHolder
func (p *Preamble) getFrom() *From                 { return nil }
func (p *Preamble) getInstructions() []Instruction { return p.Instructions }

func replaceVariables(df *Dockerfile, buildArgs, baseImageEnv map[string]string, str string, stage instructionHolder, beforeIdx int) string {
	allMatches := argExpression.FindAllStringSubmatchIndex(str, -1)

	type replacement struct {
		begin, end int
		value      string
	}

	var replacements []replacement
	for _, loc := range allMatches {
		fullMatch := str[loc[0]:loc[1]]
		variable := str[loc[2]:loc[3]]

		isVarExp := loc[4] >= 0
		var option, word string
		if isVarExp {
			option = str[loc[4]:loc[5]]
			word = str[loc[6]:loc[7]]
		}

		value := findValue(df, buildArgs, baseImageEnv, variable, stage, beforeIdx)
		if isVarExp {
			isSet := value != ""
			value = getExpressionValue(option, isSet, word, value)
		}

		_ = fullMatch
		replacements = append(replacements, replacement{begin: loc[0], end: loc[1], value: value})
	}

	// Apply in reverse order to preserve indices
	for i := len(replacements) - 1; i >= 0; i-- {
		r := replacements[i]
		str = str[:r.begin] + r.value + str[r.end:]
	}
	return str
}

func getExpressionValue(option string, isSet bool, word, value string) string {
	word = strings.Trim(word, `"'`)
	switch option {
	case "-":
		if isSet {
			return value
		}
		return word
	case "+":
		if isSet {
			return word
		}
		return value
	}
	return value
}

func findValue(df *Dockerfile, buildArgs, baseImageEnv map[string]string, variable string, stage instructionHolder, beforeIdx int) string {
	considerArg := true
	seen := make(map[instructionHolder]bool)

	for {
		if seen[stage] {
			return ""
		}
		seen[stage] = true

		instrs := stage.getInstructions()
		limit := beforeIdx
		if limit > len(instrs) {
			limit = len(instrs)
		}

		for i := limit - 1; i >= 0; i-- {
			instr := instrs[i]
			if instr.Name != variable {
				continue
			}
			if instr.Instruction == "ENV" {
				return replaceVariables(df, buildArgs, baseImageEnv, instr.Value, stage, i)
			}
			if instr.Instruction == "ARG" && considerArg {
				if override, ok := buildArgs[instr.Name]; ok {
					return replaceVariables(df, buildArgs, baseImageEnv, override, stage, i)
				}
				if instr.Value != "" {
					return replaceVariables(df, buildArgs, baseImageEnv, instr.Value, stage, i)
				}
				// An unbound ARG (no value, no build-arg override) is NOT a
				// definition: TS treats its value as undefined, which its match
				// predicate excludes. Keep scanning so a preceding ENV of the same
				// name wins instead of being shadowed by an empty "".
				continue
			}
		}

		from := stage.getFrom()
		if from == nil {
			// We're in the preamble with no parent
			if val, ok := baseImageEnv[variable]; ok {
				return val
			}
			return ""
		}

		image := replaceVariables(df, buildArgs, baseImageEnv, from.Image, &df.Preamble, len(df.Preamble.Instructions))
		if next, ok := df.StagesByLabel[image]; ok {
			stage = next
			beforeIdx = len(next.Instructions)
			considerArg = false
		} else {
			stage = &df.Preamble
			beforeIdx = len(df.Preamble.Instructions)
			considerArg = true
		}
	}
}
