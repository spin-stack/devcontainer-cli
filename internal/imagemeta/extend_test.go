package imagemeta

import (
	"strings"
	"testing"

	"github.com/devcontainers/cli/internal/features"
)

func TestGenerateExtendImageBuild(t *testing.T) {
	tests := []struct {
		name          string
		baseImage     string
		featureSets   []*features.Set
		metadata      []Entry
		containerUser string
		remoteUser    string
		useBuildKit   bool
		check         func(t *testing.T, info *ExtendImageBuildInfo)
	}{
		{
			name:          "NoFeatures",
			baseImage:     "ubuntu:22.04",
			featureSets:   nil,
			metadata:      []Entry{{ID: "test", RemoteUser: "vscode"}},
			containerUser: "vscode",
			remoteUser:    "vscode",
			useBuildKit:   false,
			check: func(t *testing.T, info *ExtendImageBuildInfo) {
				if info.OverrideTarget != "dev_containers_target_stage" {
					t.Errorf("target = %q", info.OverrideTarget)
				}
				if info.BuildArgs["_DEV_CONTAINERS_BASE_IMAGE"] != "ubuntu:22.04" {
					t.Errorf("build arg = %q", info.BuildArgs["_DEV_CONTAINERS_BASE_IMAGE"])
				}
				if !strings.Contains(info.DockerfileContent, "devcontainer.metadata") {
					t.Error("should contain metadata label")
				}
				if !strings.Contains(info.DockerfileContent, "dev_containers_target_stage") {
					t.Error("should contain target stage")
				}
			},
		},
		{
			name:      "WithFeatures",
			baseImage: "ubuntu:22.04",
			featureSets: []*features.Set{
				{
					SourceInfo: &features.OCISource{UserID: "ghcr.io/devcontainers/features/go:1"},
					Features:   []features.Feature{{ID: "go", Version: "1.21", Value: true}},
				},
				{
					SourceInfo: &features.OCISource{UserID: "ghcr.io/devcontainers/features/node:1"},
					Features:   []features.Feature{{ID: "node", Version: "18", Value: "18"}},
				},
			},
			metadata:      []Entry{{ID: "go"}, {ID: "node"}},
			containerUser: "vscode",
			remoteUser:    "vscode",
			useBuildKit:   true,
			check: func(t *testing.T, info *ExtendImageBuildInfo) {
				df := info.DockerfileContent

				// Should have install scripts for both features
				if !strings.Contains(df, "_dev_container_feature_0/install.sh") {
					t.Error("missing feature 0 install")
				}
				if !strings.Contains(df, "_dev_container_feature_1/install.sh") {
					t.Error("missing feature 1 install")
				}

				// Should have USER root
				if !strings.Contains(df, "USER root") {
					t.Error("should switch to root for install")
				}

				// Should restore user
				if !strings.Contains(df, "USER vscode") {
					t.Error("should restore user after install")
				}

				// Build-only env vars should be scoped to RUN, not persisted as final ENV.
				if strings.Contains(df, "\nENV _CONTAINER_USER=") {
					t.Error("should not persist _CONTAINER_USER as ENV")
				}
				if strings.Contains(df, "\nENV _REMOTE_USER=") {
					t.Error("should not persist _REMOTE_USER as ENV")
				}
				if !strings.Contains(df, "_CONTAINER_USER='vscode' _REMOTE_USER='vscode'") {
					t.Error("missing scoped feature install env vars")
				}
				// upstream #331: _REMOTE_USER_HOME must be resolved per-RUN from
				// /etc/passwd (not omitted), so common-utils-created users resolve.
				if !strings.Contains(df, `_REMOTE_USER_HOME="$(`) || !strings.Contains(df, "getent passwd 'vscode'") {
					t.Errorf("missing per-RUN _REMOTE_USER_HOME getent resolution:\n%s", df)
				}

				// Should have metadata label
				if !strings.Contains(df, "devcontainer.metadata") {
					t.Error("missing metadata label")
				}

				// BuildKit context
				if _, ok := info.BuildKitContexts["dev_containers_feature_content_source"]; !ok {
					t.Error("missing BuildKit context")
				}
			},
		},
		{
			name: "FeatureEnvScopedToInstall",
			// Regression: feature option env vars (and _REMOTE_USER/_CONTAINER_USER)
			// must apply to install.sh, not to the preceding chmod. In POSIX sh,
			// `KEY=v chmod ... && install.sh` scopes KEY to chmod only, so the options
			// never reach install.sh.
			baseImage: "ubuntu:22.04",
			featureSets: []*features.Set{
				{
					SourceInfo: &features.OCISource{UserID: "ghcr.io/devcontainers/features/go:1"},
					Features:   []features.Feature{{ID: "go", Version: "1.21", Value: true}},
				},
			},
			metadata:      []Entry{{ID: "go"}},
			containerUser: "vscode",
			remoteUser:    "node",
			useBuildKit:   true,
			check: func(t *testing.T, info *ExtendImageBuildInfo) {
				df := info.DockerfileContent

				// Env assignments must sit between "&&" and install.sh (applied to install.sh).
				if !strings.Contains(df, "&& _CONTAINER_USER='vscode' _REMOTE_USER='node'") {
					t.Errorf("feature env not applied to install.sh:\n%s", df)
				}
				// And must NOT prefix the chmod (the original bug).
				if strings.Contains(df, "_REMOTE_USER='node' chmod") {
					t.Errorf("feature env wrongly scoped to chmod:\n%s", df)
				}
			},
		},
		{
			name:      "RootUser",
			baseImage: "ubuntu",
			featureSets: []*features.Set{
				{
					SourceInfo: &features.OCISource{UserID: "feat"},
					Features:   []features.Feature{{ID: "feat"}},
				},
			},
			metadata:      nil,
			containerUser: "root",
			remoteUser:    "root",
			useBuildKit:   false,
			check: func(t *testing.T, info *ExtendImageBuildInfo) {
				df := info.DockerfileContent

				// Should NOT have USER root → USER root (redundant)
				// Already root, so no "USER root" restore needed
				lines := strings.Split(df, "\n")
				userCount := 0
				for _, l := range lines {
					if strings.HasPrefix(strings.TrimSpace(l), "USER ") {
						userCount++
					}
				}
				// Should have exactly 1 USER root (for the install), and NOT restore
				if userCount != 1 {
					t.Errorf("expected 1 USER statement, got %d", userCount)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := GenerateExtendImageBuild(tt.baseImage, tt.featureSets, tt.metadata, tt.containerUser, tt.remoteUser, tt.useBuildKit, nil)
			tt.check(t, info)
		})
	}
}

func TestGetSafeID(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"go", "GO"},
		{"node", "NODE"},
		{"azure-cli", "AZURE_CLI"},
		{"ghcr.io/devcontainers/features/go", "GHCR_IO_DEVCONTAINERS_FEATURES_GO"},
		{"123feature", "_FEATURE"},
		{"my_feature", "MY_FEATURE"},
	}
	for _, tt := range tests {
		got := safeID(tt.input)
		if got != tt.want {
			t.Errorf("safeID(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestGenerateFeatureBuildEnvVars(t *testing.T) {
	feat := features.Feature{
		ID:    "go",
		Value: true,
		ContainerEnv: map[string]string{
			"GOPATH": "/go",
		},
	}

	envs := generateFeatureBuildEnvVars(feat, "vscode", "node")

	foundBuildArg := false
	foundGopath := false
	foundContainerUser := false
	for _, e := range envs {
		if strings.HasPrefix(e, "_BUILD_ARG_GO=") {
			foundBuildArg = true
		}
		if strings.HasPrefix(e, "GOPATH=") {
			foundGopath = true
		}
		if strings.HasPrefix(e, "_CONTAINER_USER=") {
			foundContainerUser = true
		}
	}
	if !foundBuildArg {
		t.Error("missing _BUILD_ARG_GO")
	}
	if !foundContainerUser {
		t.Error("missing _CONTAINER_USER")
	}
	// containerEnv must NOT be inlined into the build env vars — it is emitted as
	// an ENV instruction before the install RUN so Docker expands ${VAR} refs.
	if foundGopath {
		t.Error("containerEnv (GOPATH) should not be in the inline build env vars")
	}
	// ...it is emitted via the persistent-ENV path instead.
	persist := generatePersistentContainerEnvVars(feat)
	foundGopathEnv := false
	for _, e := range persist {
		if strings.HasPrefix(e, "GOPATH=") {
			foundGopathEnv = true
		}
	}
	if !foundGopathEnv {
		t.Error("containerEnv (GOPATH) should be emitted as a persistent ENV")
	}
}

func TestGenerateFeatureBuildEnvVars_MapValueNormalizesToTrue(t *testing.T) {
	feat := features.Feature{
		ID: "hello",
		Value: map[string]interface{}{
			"greeting": "howdy",
		},
		Options: map[string]interface{}{
			"greeting": map[string]interface{}{
				"type":    "string",
				"default": "hello",
			},
		},
		UserOptions: map[string]interface{}{
			"greeting": "howdy",
		},
	}

	envs := generateFeatureBuildEnvVars(feat, "vscode", "node")
	all := strings.Join(envs, "\n")

	if !strings.Contains(all, "_BUILD_ARG_HELLO='true'") {
		t.Fatalf("expected normalized main build arg, got %q", all)
	}
	if !strings.Contains(all, "GREETING='howdy'") {
		t.Fatalf("expected option env var, got %q", all)
	}
	if strings.Contains(all, `{"greeting":"howdy"}`) {
		t.Fatalf("unexpected raw JSON map in env vars: %q", all)
	}
}

func TestGeneratePersistentContainerEnvVars(t *testing.T) {
	envs := generatePersistentContainerEnvVars(features.Feature{
		ContainerEnv: map[string]string{
			"DOCKER_BUILDKIT": "1",
		},
	})

	if len(envs) != 1 {
		t.Fatalf("envs len = %d, want 1", len(envs))
	}
	if envs[0] != `DOCKER_BUILDKIT="1"` {
		t.Fatalf("envs[0] = %q", envs[0])
	}
}

// TestExtend_SyntaxDirectiveForBuildContexts verifies the fix: when the feature
// build uses --build-context (useBuildKitContexts), the generated Dockerfile must
// declare a frontend that supports build contexts (docker/dockerfile >= 1.4) as
// its first line, or the build fails on an older default frontend.
func TestExtend_SyntaxDirectiveForBuildContexts(t *testing.T) {
	fss := []*features.Set{{
		SourceInfo: &features.OCISource{ID: "go"},
		Features:   []features.Feature{{ID: "go", Version: "1.21", Value: true}},
	}}

	withCtx := GenerateExtendImageBuild("base:img", fss, nil, "root", "root", true, nil)
	full := withCtx.DockerfilePrefixContent + withCtx.DockerfileContent
	if !strings.HasPrefix(full, "# syntax=docker/dockerfile:1.4\n") {
		t.Errorf("build-context build must start with the syntax directive, got:\n%s", full)
	}
	if _, ok := withCtx.BuildKitContexts["dev_containers_feature_content_source"]; !ok {
		t.Error("expected the feature-content build context to be declared")
	}

	// Without build contexts, no syntax directive is forced.
	noCtx := GenerateExtendImageBuild("base:img", fss, nil, "root", "root", false, nil)
	if strings.Contains(noCtx.DockerfilePrefixContent, "syntax=") {
		t.Errorf("non-context build must not force a syntax directive: %q", noCtx.DockerfilePrefixContent)
	}
}
