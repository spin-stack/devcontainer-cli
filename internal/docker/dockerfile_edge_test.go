package docker

import (
	"testing"
)

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
