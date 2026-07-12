package docker

import (
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
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
	fromStatementRe = regexp.MustCompile(`(?mi)^\s*FROM\s+(?:--platform=(\S+)\s+)?("?[^\s]+"?)(?:\s+AS\s+(\S+))?`)
	fromLineRe      = regexp.MustCompile(`(?mi)^[\t ]*FROM\b`)
	argEnvUser      = regexp.MustCompile(`(?mi)^\s*(?P<instr>ARG|ENV|USER)\s+(?P<name>[^\s=]+)(?:[ =]+(?:"(?P<v1>[^"]+)"|(?P<v2>\S+)))?`)
	directiveRe     = regexp.MustCompile(`^\s*#\s*(\S+)\s*=\s*(.+)`)
	findFromLines   = regexp.MustCompile(`(?mi)^(\s*FROM.*)`)
	parseFromLine   = regexp.MustCompile(`(?i)FROM\s+(?:--platform=\S+\s+)?("?[^\s]+"?)(?:\s+AS\s+(\S+))?`)
	argExpression   = regexp.MustCompile(`\$\{?([a-zA-Z0-9_]+)(?::([+-])([^}]+))?\}?`)
)

// ExtractDockerfile parses a Dockerfile string into structured form.
func ExtractDockerfile(content string) *Dockerfile {
	// Split content at FROM lines (Go regexp doesn't support lookahead,
	// so we find FROM positions and split manually).
	locs := fromLineRe.FindAllStringIndex(content, -1)

	var preambleStr string
	var stageStrs []string

	if len(locs) == 0 {
		preambleStr = content
	} else {
		preambleStr = content[:locs[0][0]]
		for i, loc := range locs {
			end := len(content)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			stageStrs = append(stageStrs, content[loc[0]:end])
		}
	}

	stages := make([]Stage, 0, len(stageStrs))
	stagesByLabel := make(map[string]*Stage)

	for _, s := range stageStrs {
		stage := Stage{
			From:         parseFrom(s),
			Instructions: extractInstructions(s),
		}
		stages = append(stages, stage)
		if stage.From.Label != "" {
			stagesByLabel[stage.From.Label] = &stages[len(stages)-1]
		}
	}

	directives := extractDirectives(preambleStr)
	var version string
	if syntax, ok := directives["syntax"]; ok {
		versionRe := regexp.MustCompile(`(?i)^(?:docker\.io/)?docker/dockerfile(?::(\S+))?`)
		if m := versionRe.FindStringSubmatch(syntax); m != nil {
			if m[1] != "" {
				version = m[1]
			} else {
				version = "latest"
			}
		}
	}

	return &Dockerfile{
		Preamble: Preamble{
			Version:      version,
			Directives:   directives,
			Instructions: extractInstructions(preambleStr),
		},
		Stages:        stages,
		StagesByLabel: stagesByLabel,
	}
}

func parseFrom(stageStr string) From {
	m := fromStatementRe.FindStringSubmatch(stageStr)
	if m == nil {
		return From{Image: "unknown"}
	}
	image := strings.Trim(m[2], `"'`)
	return From{
		Platform: m[1],
		Image:    image,
		Label:    m[3],
	}
}

func extractDirectives(preamble string) map[string]string {
	directives := make(map[string]string)
	for _, line := range strings.Split(preamble, "\n") {
		m := directiveRe.FindStringSubmatch(line)
		if m != nil {
			if _, exists := directives[m[1]]; !exists {
				directives[m[1]] = strings.TrimSpace(m[2])
			}
		} else {
			break
		}
	}
	return directives
}

func extractInstructions(stageStr string) []Instruction {
	matches := argEnvUser.FindAllStringSubmatch(stageStr, -1)
	result := make([]Instruction, 0, len(matches))
	for _, m := range matches {
		instr := strings.ToUpper(m[1])
		name := m[2]
		value := m[3] // quoted value
		if value == "" {
			value = m[4] // unquoted value
		}
		result = append(result, Instruction{
			Instruction: instr,
			Name:        name,
			Value:       value,
		})
	}
	return result
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

	// Already has a label
	if m[2] != "" {
		return m[2], ""
	}

	// Insert AS label after the FROM ... image part
	matchedPart := m[0]
	insertPos := lastMatch[0] + strings.Index(lastFromLine, matchedPart) + len(matchedPart)

	modifiedContent = content[:insertPos] + " AS " + defaultName + content[insertPos:]
	return defaultName, modifiedContent
}

// SupportsBuildContexts checks if the Dockerfile syntax supports --build-context.
// Returns true, false, or "unknown" (as bool + string).
func SupportsBuildContexts(df *Dockerfile) (supported bool, unknown bool) {
	version := df.Preamble.Version
	if version == "" {
		if _, hasSyntax := df.Preamble.Directives["syntax"]; hasSyntax {
			return false, true // has syntax directive but not docker/dockerfile
		}
		return false, false
	}

	numVersionRe := regexp.MustCompile(`^\d+(\.\d+){0,2}`)
	numVersion := numVersionRe.FindString(version)
	if numVersion == "" {
		return true, false // "latest", "labs", no specific tag → assume yes
	}

	constraint, err := semver.NewConstraint(">= 1.4")
	if err != nil {
		return false, false
	}
	v, err := semver.NewVersion(numVersion)
	if err != nil {
		return false, false
	}
	return constraint.Check(v), false
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
				val := instr.Value
				if override, ok := buildArgs[instr.Name]; ok {
					val = override
				}
				if val != "" {
					return replaceVariables(df, buildArgs, baseImageEnv, val, stage, i)
				}
				return ""
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
