package docker

import (
	"regexp"
	"strings"
)

// argumentExpression is TS dockerfileUtils.ts argumentExpression.
var argumentExpression = regexp.MustCompile(`\$\{?([a-zA-Z0-9_]+)(:(-|\+)([^}]+))?\}?`)

// dfScope is a TS Stage or the preamble ({ from?, instructions }).
type dfScope struct {
	from         *From
	instructions []Instruction
}

// Resolver ports the variable resolution of TS dockerfileUtils.ts
// (replaceVariables / findValue), which findBaseImage and findUserStatement use.
type Resolver struct {
	preamble  *dfScope
	stages    []*dfScope
	byLabel   map[string]*dfScope
	buildArgs map[string]string
	platform  map[string]string
}

// NewResolver resolves df's variables with the --build-arg values and the
// automatic BUILD*/TARGET* platform ARGs (TS globalBuildxPlatformArgs).
func NewResolver(df *Dockerfile, buildArgs, platformArgs map[string]string) *Resolver {
	r := &Resolver{
		preamble:  &dfScope{instructions: df.Preamble.Instructions},
		byLabel:   map[string]*dfScope{},
		buildArgs: buildArgs,
		platform:  platformArgs,
	}
	for i := range df.Stages {
		s := &dfScope{from: &df.Stages[i].From, instructions: df.Stages[i].Instructions}
		r.stages = append(r.stages, s)
		if s.from.Label != "" {
			r.byLabel[s.from.Label] = s
		}
	}
	return r
}

func (r *Resolver) startStage(target string) *dfScope {
	if target != "" {
		return r.byLabel[target]
	}
	if len(r.stages) == 0 {
		return nil
	}
	return r.stages[len(r.stages)-1]
}

// fromImage resolves a stage's FROM image in the global scope.
func (r *Resolver) fromImage(s *dfScope, baseEnv map[string]string) string {
	return r.replace(baseEnv, s.from.Image, r.preamble, len(r.preamble.instructions))
}

// FindBaseImage is TS findBaseImage: follow FROM <stage> to the first image
// that is not a stage. The base image's env is not available in FROM.
func (r *Resolver) FindBaseImage(target string) string {
	seen := map[*dfScope]bool{}
	for s := r.startStage(target); s != nil; {
		if seen[s] {
			return ""
		}
		seen[s] = true
		image := r.fromImage(s, nil)
		next, ok := r.byLabel[image]
		if !ok {
			return image
		}
		s = next
	}
	return ""
}

// FindUserStatement is TS findUserStatement: the last USER of the target stage
// (or of the stage it is built FROM), with its variables resolved.
func (r *Resolver) FindUserStatement(baseEnv map[string]string, target string) string {
	seen := map[*dfScope]bool{}
	for s := r.startStage(target); s != nil; {
		if seen[s] {
			return ""
		}
		seen[s] = true
		for i := len(s.instructions) - 1; i >= 0; i-- {
			if s.instructions[i].Instruction == "USER" {
				return r.replace(baseEnv, s.instructions[i].Name, s, i)
			}
		}
		s = r.byLabel[r.fromImage(s, baseEnv)]
	}
	return ""
}

// replace is TS replaceVariables.
func (r *Resolver) replace(baseEnv map[string]string, str string, s *dfScope, before int) string {
	var b strings.Builder
	last := 0
	for _, m := range argumentExpression.FindAllStringSubmatchIndex(str, -1) { // mutate-exempt: any n < 0 is every match
		b.WriteString(str[last:m[0]])
		last = m[1]
		value := r.findValue(baseEnv, str[m[2]:m[3]], s, before)
		varExp := m[4]    // mutate-exempt: m[4] and m[5] are both -1 exactly when the :-word/:+word group is unmatched
		if varExp != -1 { // ${var:-word} / ${var:+word}
			option, word := str[m[6]], str[m[8]:m[9]] // the option is one character, - or +
			isSet := value != ""
			if (option == '-' && !isSet) || (option == '+' && isSet) {
				value = word
			}
			value = trimOneQuote(value)
		}
		b.WriteString(value)
	}
	b.WriteString(str[last:])
	return b.String()
}

// trimOneQuote strips one leading and one trailing quote, as TS getExpressionValue does.
func trimOneQuote(s string) string {
	if s != "" && (s[0] == '"' || s[0] == '\'') {
		s = s[1:]
	}
	if s != "" && (s[len(s)-1] == '"' || s[len(s)-1] == '\'') {
		s = s[:len(s)-1]
	}
	return s
}

// findValue is TS findValue. An ARG without a default (and no build arg) does
// not define the variable, so the lookup continues outward.
func (r *Resolver) findValue(baseEnv map[string]string, variable string, s *dfScope, before int) string {
	considerArg := true
	seen := map[*dfScope]bool{}
	for {
		if seen[s] {
			return ""
		}
		seen[s] = true
		for i := min(before, len(s.instructions)) - 1; i >= 0; i-- {
			ins := s.instructions[i]
			if ins.Name != variable {
				continue
			}
			if ins.Instruction == "ENV" {
				return r.replace(baseEnv, ins.Value, s, i)
			}
			if ins.Instruction == "ARG" && considerArg {
				if v, ok := r.buildArgs[ins.Name]; ok {
					return r.replace(baseEnv, v, s, i)
				}
				if ins.Value != "" {
					return r.replace(baseEnv, ins.Value, s, i)
				}
			}
		}
		if s.from == nil {
			if v, ok := baseEnv[variable]; ok {
				return v
			}
			return r.platform[variable]
		}
		image := r.fromImage(s, baseEnv)
		next, ok := r.byLabel[image]
		if !ok {
			next = r.preamble
		}
		s = next
		before = len(s.instructions)
		considerArg = s == r.preamble
	}
}
