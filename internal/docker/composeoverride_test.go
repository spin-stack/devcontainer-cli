package docker

import (
	"encoding/json"
	"strings"
	"testing"
)

// composeService extracts services[name] from a parsed compose override.
func composeService(t *testing.T, parsed map[string]interface{}, name string) map[string]interface{} {
	t.Helper()
	services, ok := parsed["services"].(map[string]interface{})
	if !ok {
		t.Fatal("missing services")
	}
	svc, ok := services[name].(map[string]interface{})
	if !ok {
		t.Fatalf("missing service %q", name)
	}
	return svc
}

func TestComposeOverride(t *testing.T) {
	tests := []struct {
		name  string
		build func(o *ComposeOverride)
		check func(t *testing.T, parsed map[string]interface{})
	}{
		{
			name: "image, privileged and capAdd",
			build: func(o *ComposeOverride) {
				svc := o.Service("app")
				svc.Image = "myimage:latest"
				svc.SetPrivileged(true)
				svc.CapAdd = []string{"SYS_PTRACE"}
			},
			check: func(t *testing.T, parsed map[string]interface{}) {
				app := composeService(t, parsed, "app")
				if app["image"] != "myimage:latest" {
					t.Errorf("image = %v", app["image"])
				}
				if app["privileged"] != true {
					t.Errorf("privileged = %v", app["privileged"])
				}
			},
		},
		{
			name: "environment escaping ($ and newline)",
			build: func(o *ComposeOverride) {
				svc := o.Service("app")
				svc.AddEnv("SIMPLE", "hello")
				svc.AddEnv("DOLLAR", "value with $dollar")
				svc.AddEnv("NEWLINE", "line1\nline2")
			},
			check: func(t *testing.T, parsed map[string]interface{}) {
				env := composeService(t, parsed, "app")["environment"].([]interface{})
				want := []string{"SIMPLE=hello", "DOLLAR=value with $$dollar", `NEWLINE=line1\nline2`}
				for i, w := range want {
					if env[i] != w {
						t.Errorf("env[%d] = %v, want %q", i, env[i], w)
					}
				}
			},
		},
		{
			name: "named volume",
			build: func(o *ComposeOverride) {
				o.Service("app").AddVolume("mydata:/data")
				o.AddVolume("mydata")
			},
			check: func(t *testing.T, parsed map[string]interface{}) {
				volumes, ok := parsed["volumes"].(map[string]interface{})
				if !ok {
					t.Fatal("missing top-level volumes")
				}
				if _, ok := volumes["mydata"]; !ok {
					t.Error("missing named volume 'mydata'")
				}
			},
		},
		{
			name: "build override",
			build: func(o *ComposeOverride) {
				o.Service("app").Build = &BuildOverride{
					Dockerfile: "/path/to/Dockerfile",
					Target:     "dev_stage",
					Args:       map[string]string{"VARIANT": "18"},
				}
			},
			check: func(t *testing.T, parsed map[string]interface{}) {
				build := composeService(t, parsed, "app")["build"].(map[string]interface{})
				if build["dockerfile"] != "/path/to/Dockerfile" {
					t.Errorf("dockerfile = %v", build["dockerfile"])
				}
				if build["target"] != "dev_stage" {
					t.Errorf("target = %v", build["target"])
				}
			},
		},
		{
			name: "entrypoint script",
			build: func(o *ComposeOverride) {
				svc := o.Service("app")
				script := BuildEntrypointScriptCompose([]string{"/usr/local/share/docker-init.sh"})
				svc.Entrypoint = []string{"/bin/sh", "-c", script, "-"}
				svc.Command = []string{}
			},
			check: func(t *testing.T, parsed map[string]interface{}) {
				ep := composeService(t, parsed, "app")["entrypoint"].([]interface{})
				if ep[0] != "/bin/sh" {
					t.Errorf("entrypoint[0] = %v", ep[0])
				}
				if ep[1] != "-c" {
					t.Errorf("entrypoint[1] = %v", ep[1])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := NewComposeOverride()
			tt.build(o)
			data, err := o.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			var parsed map[string]interface{}
			if err := json.Unmarshal(data, &parsed); err != nil {
				t.Fatal("invalid JSON:", err)
			}
			tt.check(t, parsed)
		})
	}
}

func TestBuildEntrypointScript(t *testing.T) {
	script := BuildEntrypointScript([]string{"/usr/local/share/docker-init.sh"})
	if script == "" {
		t.Error("empty script")
	}
	// Check structure
	lines := []string{
		"echo Container started",
		`trap "exit 0" 15`,
		"/usr/local/share/docker-init.sh",
		`exec "$@"`,
		`while sleep 1 & wait $!; do :; done`,
	}
	for _, line := range lines {
		if !contains(script, line) {
			t.Errorf("missing line: %s", line)
		}
	}
}

func TestBuildEntrypointScriptCompose(t *testing.T) {
	script := BuildEntrypointScriptCompose([]string{"/usr/local/share/docker-init.sh"})
	if strings.Contains(script, `\n`) {
		t.Fatalf("compose script must contain real newlines, got %q", script)
	}
	for _, line := range []string{
		"echo Container started",
		`trap "exit 0" 15`,
		"/usr/local/share/docker-init.sh",
		`exec "$$@"`,
		`while sleep 1 & wait $$!; do :; done`,
	} {
		if !contains(script, line) {
			t.Errorf("missing line: %s", line)
		}
	}
}

// contains helper already defined in dockerfile_test.go
