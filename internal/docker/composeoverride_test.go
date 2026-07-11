package docker

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestComposeOverride_Basic(t *testing.T) {
	o := NewComposeOverride()
	svc := o.Service("app")
	svc.Image = "myimage:latest"
	svc.SetPrivileged(true)
	svc.CapAdd = []string{"SYS_PTRACE"}

	data, err := o.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}

	// Parse back
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal("invalid JSON:", err)
	}

	services := parsed["services"].(map[string]interface{})
	app := services["app"].(map[string]interface{})

	if app["image"] != "myimage:latest" {
		t.Errorf("image = %v", app["image"])
	}
	if app["privileged"] != true {
		t.Errorf("privileged = %v", app["privileged"])
	}
}

func TestComposeOverride_Env(t *testing.T) {
	o := NewComposeOverride()
	svc := o.Service("app")
	svc.AddEnv("SIMPLE", "hello")
	svc.AddEnv("DOLLAR", "value with $dollar")
	svc.AddEnv("NEWLINE", "line1\nline2")

	data, err := o.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]interface{}
	json.Unmarshal(data, &parsed)
	services := parsed["services"].(map[string]interface{})
	app := services["app"].(map[string]interface{})
	env := app["environment"].([]interface{})

	if env[0] != "SIMPLE=hello" {
		t.Errorf("env[0] = %v", env[0])
	}
	if env[1] != "DOLLAR=value with $$dollar" {
		t.Errorf("env[1] = %v, want escaped $$", env[1])
	}
	if env[2] != `NEWLINE=line1\nline2` {
		t.Errorf("env[2] = %v, want escaped \\n", env[2])
	}
}

func TestComposeOverride_Volumes(t *testing.T) {
	o := NewComposeOverride()
	svc := o.Service("app")
	svc.AddVolume("mydata:/data")
	o.AddVolume("mydata")

	data, err := o.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]interface{}
	json.Unmarshal(data, &parsed)

	if _, ok := parsed["volumes"]; !ok {
		t.Error("missing top-level volumes")
	}
	volumes := parsed["volumes"].(map[string]interface{})
	if _, ok := volumes["mydata"]; !ok {
		t.Error("missing named volume 'mydata'")
	}
}

func TestComposeOverride_Build(t *testing.T) {
	o := NewComposeOverride()
	svc := o.Service("app")
	svc.Build = &BuildOverride{
		Dockerfile: "/path/to/Dockerfile",
		Target:     "dev_stage",
		Args:       map[string]string{"VARIANT": "18"},
	}

	data, err := o.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]interface{}
	json.Unmarshal(data, &parsed)
	services := parsed["services"].(map[string]interface{})
	app := services["app"].(map[string]interface{})
	build := app["build"].(map[string]interface{})

	if build["dockerfile"] != "/path/to/Dockerfile" {
		t.Errorf("dockerfile = %v", build["dockerfile"])
	}
	if build["target"] != "dev_stage" {
		t.Errorf("target = %v", build["target"])
	}
}

func TestComposeOverride_Entrypoint(t *testing.T) {
	o := NewComposeOverride()
	svc := o.Service("app")
	script := BuildEntrypointScriptCompose([]string{"/usr/local/share/docker-init.sh"})
	svc.Entrypoint = []string{"/bin/sh", "-c", script, "-"}
	svc.Command = []string{}

	data, err := o.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}

	var parsed map[string]interface{}
	json.Unmarshal(data, &parsed)
	services := parsed["services"].(map[string]interface{})
	app := services["app"].(map[string]interface{})

	ep := app["entrypoint"].([]interface{})
	if ep[0] != "/bin/sh" {
		t.Errorf("entrypoint[0] = %v", ep[0])
	}
	if ep[1] != "-c" {
		t.Errorf("entrypoint[1] = %v", ep[1])
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
