package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
)

// fakeEngine is a Docker Engine API server holding a few containers, so stop
// and down can run against an engine without Docker. It records the calls the
// command makes.
type fakeEngine struct {
	t          *testing.T
	mu         sync.Mutex
	containers map[string]map[string]string // id -> labels; nil labels: inspect has no Config
	stopFails  bool
	calls      []string
}

var fakeEnginePath = regexp.MustCompile(`^(?:/v[0-9.]+)?(/.*)$`)

// startFakeEngine serves the fake engine and points DOCKER_HOST at it.
func startFakeEngine(t *testing.T, containers map[string]map[string]string) *fakeEngine {
	t.Helper()
	e := &fakeEngine{t: t, containers: containers}
	srv := httptest.NewServer(http.HandlerFunc(e.serve))
	t.Cleanup(srv.Close)
	t.Setenv("DOCKER_HOST", "tcp://"+srv.Listener.Addr().String())
	t.Setenv("DOCKER_API_VERSION", "1.47")
	t.Setenv("DOCKER_TLS_VERIFY", "")
	t.Setenv("DOCKER_CERT_PATH", "")
	return e
}

func (e *fakeEngine) record(call string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls = append(e.calls, call)
}

func (e *fakeEngine) recorded() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.calls)
}

func (e *fakeEngine) serve(w http.ResponseWriter, r *http.Request) {
	path := fakeEnginePath.FindStringSubmatch(r.URL.Path)[1]
	w.Header().Set("Content-Type", "application/json")
	switch {
	case path == "/_ping":
		w.Header().Set("Api-Version", "1.47")
		fmt.Fprint(w, "OK")
	case r.Method == http.MethodGet && path == "/containers/json":
		var filters map[string]map[string]bool
		if f := r.URL.Query().Get("filters"); f != "" {
			if err := json.Unmarshal([]byte(f), &filters); err != nil {
				e.t.Errorf("filters %q: %v", f, err)
			}
		}
		var want []string
		for l := range filters["label"] {
			want = append(want, l)
		}
		slices.Sort(want)
		e.record("list " + strings.Join(want, ","))
		items := []map[string]string{}
		for id, labels := range e.containers {
			if hasLabels(labels, want) {
				items = append(items, map[string]string{"Id": id})
			}
		}
		json.NewEncoder(w).Encode(items)
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/json"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/json")
		labels, ok := e.containers[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintf(w, `{"message":"No such container: %s"}`, id)
			return
		}
		body := map[string]any{"Id": id}
		if labels != nil {
			body["Config"] = map[string]any{"Labels": labels}
		}
		json.NewEncoder(w).Encode(body)
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/stop"):
		e.record("stop " + strings.TrimSuffix(strings.TrimPrefix(path, "/containers/"), "/stop"))
		if e.stopFails {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"message":"cannot stop"}`)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodDelete:
		e.record("remove " + strings.TrimPrefix(path, "/containers/"))
		w.WriteHeader(http.StatusNoContent)
	default:
		e.t.Errorf("unexpected engine call %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotImplemented)
	}
}

func hasLabels(labels map[string]string, want []string) bool {
	for _, l := range want {
		k, v, _ := strings.Cut(l, "=")
		if got, ok := labels[k]; !ok || got != v {
			return false
		}
	}
	return true
}

// fakeDockerCLI writes a docker CLI that records its arguments and answers
// `compose version` (unless composeMissing) and fails `compose ... stop|down`
// with composeExit. It returns the CLI's path and a reader of what it ran.
func fakeDockerCLI(t *testing.T, composeMissing bool, composeExit int) (string, func() []string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls")
	versionExit := 0
	if composeMissing {
		versionExit = 1
	}
	script := fmt.Sprintf(`#!/bin/sh
echo "$*" >> %q
case "$*" in
  "compose version --short") [ %d -eq 0 ] && echo 2.24.0; exit %d ;;
  compose*) echo "compose said no" >&2; exit %d ;;
esac
`, logPath, versionExit, versionExit, composeExit)
	cli := filepath.Join(dir, "docker")
	if err := os.WriteFile(cli, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return cli, func() []string {
		b, err := os.ReadFile(logPath)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			t.Fatal(err)
		}
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
}

func runStopOrDownForTest(t *testing.T, opts stopOpts, remove bool) map[string]any {
	t.Helper()
	var out captureOutput
	_ = runStopOrDown(context.Background(), &out, opts, remove)
	var got map[string]any
	if err := json.Unmarshal(out.out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not one JSON result: %v\n%s", err, out.out.String())
	}
	return got
}

func assertResult(t *testing.T, got map[string]any, want map[string]any) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v (result %v)", k, got[k], v, got)
		}
	}
}

func TestStopWithoutATargetStopsTheCurrentDirectorysContainer(t *testing.T) {
	ws := t.TempDir()
	t.Chdir(ws)
	engine := startFakeEngine(t, map[string]map[string]string{
		"c1": {"devcontainer.local_folder": ws},
	})
	cli, composeCalls := fakeDockerCLI(t, false, 0)

	got := runStopOrDownForTest(t, stopOpts{dockerPath: cli}, false)

	assertResult(t, got, map[string]any{"outcome": "success", "containerId": "c1", "result": "stopped"})
	if calls := engine.recorded(); !slices.Contains(calls, "stop c1") || slices.Contains(calls, "remove c1") {
		t.Errorf("engine calls = %v, want c1 stopped and not removed", calls)
	}
	if calls := composeCalls(); calls != nil {
		t.Errorf("docker CLI ran %v for a container outside Compose", calls)
	}
}

func TestStopGivenAWorkspaceLooksForItsContainerNotTheCurrentDirectorys(t *testing.T) {
	ws := t.TempDir()
	t.Chdir(t.TempDir())
	startFakeEngine(t, map[string]map[string]string{
		"c1": {"devcontainer.local_folder": ws},
	})
	cli, _ := fakeDockerCLI(t, false, 0)

	got := runStopOrDownForTest(t, stopOpts{workspaceFolder: ws, dockerPath: cli}, false)

	assertResult(t, got, map[string]any{"outcome": "success", "containerId": "c1", "result": "stopped"})
}

func TestStopWithNoContainerSucceedsWithoutDoingAnything(t *testing.T) {
	ws := t.TempDir()
	engine := startFakeEngine(t, map[string]map[string]string{
		"other": {"devcontainer.local_folder": "/elsewhere"},
	})
	cli, _ := fakeDockerCLI(t, false, 0)

	got := runStopOrDownForTest(t, stopOpts{workspaceFolder: ws, dockerPath: cli}, true)

	assertResult(t, got, map[string]any{"outcome": "success", "result": "no-container-found"})
	if _, ok := got["containerId"]; ok {
		t.Errorf("result names a container: %v", got)
	}
	for _, c := range engine.recorded() {
		if !strings.HasPrefix(c, "list ") {
			t.Errorf("engine call %q with no container to act on", c)
		}
	}
}

func TestDownStopsAndRemovesTheContainer(t *testing.T) {
	engine := startFakeEngine(t, map[string]map[string]string{"c1": {}})
	cli, _ := fakeDockerCLI(t, false, 0)

	got := runStopOrDownForTest(t, stopOpts{containerID: "c1", dockerPath: cli}, true)

	assertResult(t, got, map[string]any{"outcome": "success", "containerId": "c1", "result": "removed"})
	if calls := engine.recorded(); !slices.Equal(calls, []string{"stop c1", "remove c1"}) {
		t.Errorf("engine calls = %v, want stop then remove", calls)
	}
}

func TestStopOrDownReportsAStopTheEngineRefused(t *testing.T) {
	for _, tc := range []struct {
		remove bool
		action string
	}{{false, "stopped"}, {true, "removed"}} {
		t.Run(tc.action, func(t *testing.T) {
			engine := startFakeEngine(t, map[string]map[string]string{"c1": {}})
			engine.stopFails = true
			cli, _ := fakeDockerCLI(t, false, 0)

			got := runStopOrDownForTest(t, stopOpts{containerID: "c1", dockerPath: cli}, tc.remove)

			assertResult(t, got, map[string]any{"outcome": "error"})
			if msg, _ := got["message"].(string); !strings.HasPrefix(msg, tc.action+": ") || !strings.Contains(msg, "cannot stop") {
				t.Errorf("message = %q, want the engine's refusal after %q", msg, tc.action)
			}
			if calls := engine.recorded(); slices.Contains(calls, "remove c1") {
				t.Errorf("engine calls = %v: a container that did not stop was removed", calls)
			}
		})
	}
}

func TestStopOfAContainerWithoutConfigStopsItDirectly(t *testing.T) {
	engine := startFakeEngine(t, map[string]map[string]string{"c1": nil})
	cli, composeCalls := fakeDockerCLI(t, false, 0)

	got := runStopOrDownForTest(t, stopOpts{containerID: "c1", dockerPath: cli}, false)

	assertResult(t, got, map[string]any{"outcome": "success", "containerId": "c1", "result": "stopped"})
	if calls := engine.recorded(); !slices.Equal(calls, []string{"stop c1"}) {
		t.Errorf("engine calls = %v, want c1 stopped", calls)
	}
	if calls := composeCalls(); calls != nil {
		t.Errorf("docker CLI ran %v", calls)
	}
}

func TestStopOrDownOfAComposeServiceActsOnTheWholeProject(t *testing.T) {
	for _, tc := range []struct {
		name          string
		remove        bool
		removeVolumes bool
		want          string
		action        string
	}{
		{"stop", false, false, "compose --project-name proj stop", "stopped"},
		{"down", true, false, "compose --project-name proj down", "removed"},
		{"down --remove-volumes", true, true, "compose --project-name proj down --volumes", "removed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := startFakeEngine(t, map[string]map[string]string{
				"c1": {"com.docker.compose.project": "proj"},
			})
			cli, composeCalls := fakeDockerCLI(t, false, 0)

			got := runStopOrDownForTest(t, stopOpts{containerID: "c1", dockerPath: cli, removeVolumes: tc.removeVolumes}, tc.remove)

			assertResult(t, got, map[string]any{"outcome": "success", "containerId": "c1", "result": tc.action})
			if calls := composeCalls(); !slices.Contains(calls, tc.want) {
				t.Errorf("docker CLI ran %v, want %q", calls, tc.want)
			}
			for _, c := range engine.recorded() {
				if strings.HasPrefix(c, "stop ") || strings.HasPrefix(c, "remove ") {
					t.Errorf("engine call %q: Compose owns the project's containers", c)
				}
			}
		})
	}
}

func TestStopOfAComposeServiceReportsWhatComposeRefused(t *testing.T) {
	startFakeEngine(t, map[string]map[string]string{"c1": {"com.docker.compose.project": "proj"}})
	cli, _ := fakeDockerCLI(t, false, 3)

	got := runStopOrDownForTest(t, stopOpts{containerID: "c1", dockerPath: cli}, false)

	assertResult(t, got, map[string]any{"outcome": "error"})
	if msg, _ := got["message"].(string); !strings.HasPrefix(msg, "stopped: ") || !strings.Contains(msg, "compose said no") {
		t.Errorf("message = %q, want Compose's refusal", msg)
	}
}

func TestStopOfAComposeServiceWithoutComposeV2IsAnError(t *testing.T) {
	startFakeEngine(t, map[string]map[string]string{"c1": {"com.docker.compose.project": "proj"}})
	cli, _ := fakeDockerCLI(t, true, 0)

	got := runStopOrDownForTest(t, stopOpts{containerID: "c1", dockerPath: cli}, false)

	assertResult(t, got, map[string]any{"outcome": "error", "message": "'docker compose' (v2) not found"})
}

func TestStopWithAnUnusableDockerHostIsAnError(t *testing.T) {
	t.Setenv("DOCKER_HOST", "not a docker host")

	got := runStopOrDownForTest(t, stopOpts{containerID: "c1"}, false)

	assertResult(t, got, map[string]any{"outcome": "error"})
	if msg, _ := got["message"].(string); !strings.HasPrefix(msg, "Docker engine: ") {
		t.Errorf("message = %q, want the engine client's error", msg)
	}
}
