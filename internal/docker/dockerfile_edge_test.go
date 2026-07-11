package docker

import (
	"testing"
)

// TestFindBaseImage_EnvVariable checks that a two-stage Dockerfile whose second
// stage is `FROM $BASE` parses into two stages (ENV in a stage does not feed FROM
// resolution). Kept standalone — it asserts stage parsing, not a base-image value,
// so it doesn't fit the TestFindBaseImage table.
func TestFindBaseImage_EnvVariable(t *testing.T) {
	dockerfile := `FROM debian
ENV BASE=custom-image
FROM $BASE
`
	df := ExtractDockerfile(dockerfile)
	if len(df.Stages) != 2 {
		t.Fatalf("stages = %d", len(df.Stages))
	}
}

func TestBuildArgs_ExtraArgs(t *testing.T) {
	c := NewClient("docker", nil, nil)
	args := c.buildArgs(BuildOptions{
		UseBuildx:   true,
		Dockerfile:  "Dockerfile",
		ContextPath: ".",
		ExtraArgs:   []string{"--build-context", "features=./features"},
	})

	found := false
	for i, a := range args {
		if a == "--build-context" && i+1 < len(args) && args[i+1] == "features=./features" {
			found = true
		}
	}
	if !found {
		t.Error("extra args not passed through")
	}
}
