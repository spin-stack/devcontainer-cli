package docker

import (
	"fmt"
	"strings"
)

// DockerfileBuilder constructs valid Dockerfiles from typed instructions.
// All value escaping is handled internally.
type DockerfileBuilder struct {
	lines []string
}

// NewDockerfileBuilder creates a new builder.
func NewDockerfileBuilder() *DockerfileBuilder {
	return &DockerfileBuilder{}
}

// Arg adds an ARG instruction.
func (b *DockerfileBuilder) Arg(name, defaultValue string) {
	b.lines = append(b.lines, fmt.Sprintf("ARG %s=%s", name, defaultValue))
}

// FromClause is returned by From() to allow chaining .As() and .Platform().
type FromClause struct {
	b *DockerfileBuilder
	i int // index into b.lines
}

// From adds a FROM instruction. Chain with .As() or .Platform().
func (b *DockerfileBuilder) From(image string) *FromClause {
	idx := len(b.lines)
	b.lines = append(b.lines, fmt.Sprintf("FROM %s", image))
	return &FromClause{b: b, i: idx}
}

// As adds an AS clause to a FROM instruction.
func (f *FromClause) As(name string) *FromClause {
	f.b.lines[f.i] += " AS " + name
	return f
}

// Platform adds a --platform flag to a FROM instruction.
func (f *FromClause) Platform(p string) *FromClause {
	// Insert --platform before the image name
	line := f.b.lines[f.i]
	f.b.lines[f.i] = strings.Replace(line, "FROM ", fmt.Sprintf("FROM --platform=%s ", p), 1)
	return f
}

// User adds a USER instruction.
func (b *DockerfileBuilder) User(user string) {
	b.lines = append(b.lines, fmt.Sprintf("USER %s", user))
}

// Env adds an ENV instruction with proper escaping of the value.
// Escapes: \ → \\, " → \", $ → \$
func (b *DockerfileBuilder) Env(key, value string) {
	b.lines = append(b.lines, fmt.Sprintf("ENV %s=%s", key, escapeDockerfileValue(value)))
}

// EnvRaw adds an ENV instruction with a raw KEY=VALUE assignment (no escaping).
// Used for feature-generated env vars that are already formatted.
func (b *DockerfileBuilder) EnvRaw(assignment string) {
	b.lines = append(b.lines, "ENV "+assignment)
}

// CopyClause is returned by Copy() to allow chaining .From().
type CopyClause struct {
	b *DockerfileBuilder
	i int
}

// Copy adds a COPY instruction. Chain with .From() for multi-stage copies.
func (b *DockerfileBuilder) Copy(src, dst string) *CopyClause {
	idx := len(b.lines)
	b.lines = append(b.lines, fmt.Sprintf("COPY %s %s", src, dst))
	return &CopyClause{b: b, i: idx}
}

// From adds a --from= clause to a COPY instruction.
func (c *CopyClause) From(stage string) *CopyClause {
	line := c.b.lines[c.i]
	c.b.lines[c.i] = strings.Replace(line, "COPY ", fmt.Sprintf("COPY --from=%s ", stage), 1)
	return c
}

// Run adds a RUN instruction.
func (b *DockerfileBuilder) Run(cmd string) {
	b.lines = append(b.lines, "RUN "+cmd)
}

// RunWithMounts adds a RUN instruction carrying the given `--mount=...` flags
// (e.g. build secrets: `--mount=type=secret,id=X`). Falls back to a plain RUN
// when there are none.
func (b *DockerfileBuilder) RunWithMounts(mounts []string, cmd string) {
	if len(mounts) == 0 {
		b.Run(cmd)
		return
	}
	b.lines = append(b.lines, "RUN "+strings.Join(mounts, " ")+" "+cmd)
}

// Label adds a LABEL instruction with proper escaping.
// The value is double-quoted with \, ", and $ escaped.
func (b *DockerfileBuilder) Label(key, value string) {
	b.lines = append(b.lines, fmt.Sprintf("LABEL %s=%s", key, escapeDockerfileValue(value)))
}

// BlankLine adds an empty line for readability.
func (b *DockerfileBuilder) BlankLine() {
	b.lines = append(b.lines, "")
}

// Comment adds a comment line.
func (b *DockerfileBuilder) Comment(text string) {
	b.lines = append(b.lines, "# "+text)
}

// String returns the complete Dockerfile content.
func (b *DockerfileBuilder) String() string {
	return strings.Join(b.lines, "\n") + "\n"
}

// escapeDockerfileValue escapes a string for use in a Dockerfile double-quoted value.
// Handles: \ → \\, " → \", $ → \$
func escapeDockerfileValue(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, `$`, `\$`)
	return `"` + s + `"`
}
