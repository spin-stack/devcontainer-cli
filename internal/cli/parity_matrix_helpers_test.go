package cli

import (
	"context"
	"strings"
	"testing"
)

func TestParityReport_CountsEveryOutcome(t *testing.T) {
	report := newParityReport([]parityCase{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}})
	report.record("a", parityMatched)
	report.record("b", parityInconclusive)
	report.record("c", paritySkippedDocker)

	snapshot := report.snapshot()
	if len(snapshot[parityMatched]) != 1 || len(snapshot[parityInconclusive]) != 1 ||
		len(snapshot[paritySkippedDocker]) != 1 || len(snapshot[parityNotSelected]) != 1 {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	formatted := formatParityReport(snapshot)
	for _, want := range []string{"matched:", "failed:", "skipped-docker:", "skipped-network:", "inconclusive:", "not-selected:"} {
		if !strings.Contains(formatted, want) {
			t.Errorf("report missing %q:\n%s", want, formatted)
		}
	}
}

func TestStrictParityError(t *testing.T) {
	if err := strictParityError(map[parityOutcome][]string{parityMatched: {"ok"}}); err != nil {
		t.Fatalf("matched report rejected: %v", err)
	}
	err := strictParityError(map[parityOutcome][]string{parityInconclusive: {"case-b", "case-a"}})
	if err == nil || !strings.Contains(err.Error(), "2 inconclusive") {
		t.Fatalf("strictParityError = %v", err)
	}
}

func TestMatchesFilter_NetworkOnly(t *testing.T) {
	t.Setenv("PARITY_NETWORK_ONLY", "true")
	t.Setenv("PARITY_LANE", "all")
	if matchesFilter(parityCase{ID: "local", Lane: "contract"}) {
		t.Fatal("non-network case selected by network-only filter")
	}
	if !matchesFilter(parityCase{ID: "remote", Lane: "runtime", NetworkRequired: true}) {
		t.Fatal("network case was not selected")
	}
}

func TestNormalizeOutput_ExtractsEmbeddedJSON(t *testing.T) {
	raw := "[2026-04-17T02:07:14.225Z] @devcontainers/cli 0.74.0\n" +
		`{"outcome":"error","message":"boom","description":"boom"}`

	got := normalizeOutput(raw)
	want := `{"description":"boom","message":"boom","outcome":"error"}`
	if got != want {
		t.Fatalf("normalizeOutput() = %q, want %q", got, want)
	}
}

func TestExtractErrorReason_UsesEmbeddedJSONErrorEnvelope(t *testing.T) {
	stdout := "[2026-04-17T02:07:14.225Z] @devcontainers/cli 0.74.0\n" +
		`{"outcome":"error","message":"Invalid value \"broken\" for --buildkit. Choose from: auto, never","description":"Invalid value \"broken\" for --buildkit. Choose from: auto, never"}`

	got := extractErrorReason(stdout, "")
	want := "invalid-choice|flag=buildkit|value=broken|choices=auto,never"
	if got != want {
		t.Fatalf("extractErrorReason() = %q, want %q", got, want)
	}
}

func TestExtractErrorReason_NormalizesImplications(t *testing.T) {
	stderr := "Implications failed:\n terminal-columns -> terminal-rows\n"
	got := extractErrorReason("", stderr)
	want := "implications|terminal-columns -> terminal-rows"
	if got != want {
		t.Fatalf("extractErrorReason() = %q, want %q", got, want)
	}
}

func TestClassifyParitySide_Timeout(t *testing.T) {
	got := classifyParitySide(context.DeadlineExceeded, "", "", 0)
	if !got.Skip || got.Reason != "timed out" {
		t.Fatalf("classifyParitySide(timeout) = %+v", got)
	}
}

func TestClassifyParitySide_InfraError(t *testing.T) {
	got := classifyParitySide(nil, "", "docker buildx failed to solve", 1)
	if !got.Skip || !got.Infra {
		t.Fatalf("classifyParitySide(infra) = %+v", got)
	}
}

func TestNormalizeOutput_NormalizesParitySideSuffix(t *testing.T) {
	ts := `{"imageName":["parity-build.labels-success-ts"],"outcome":"success"}`
	goOut := `{"imageName":["parity-build.labels-success-go"],"outcome":"success"}`

	if normalizeOutput(ts) != normalizeOutput(goOut) {
		t.Fatalf("normalizeOutput() did not normalize parity side suffix")
	}
}

func TestExtractCLIResultEnv(t *testing.T) {
	raw := `{"containerId":"abc123","composeProjectName":"proj","imageName":["img1","img2"],"outcome":"success"}`
	got := extractCLIResultEnv(raw)

	if got["CONTAINER_ID"] != "abc123" {
		t.Fatalf("CONTAINER_ID = %q", got["CONTAINER_ID"])
	}
	if got["COMPOSE_PROJECT_NAME"] != "proj" {
		t.Fatalf("COMPOSE_PROJECT_NAME = %q", got["COMPOSE_PROJECT_NAME"])
	}
	if got["IMAGE_NAME"] != "img1" || got["IMAGE_NAME_1"] != "img1" || got["IMAGE_NAME_2"] != "img2" {
		t.Fatalf("image env = %#v", got)
	}
}

func TestExtractCLIResultEnv_EmbeddedJSON(t *testing.T) {
	raw := "[2026-04-17T02:07:14.225Z] banner\n" +
		`{"imageName":"demo","outcome":"success"}`
	got := extractCLIResultEnv(raw)

	if got["IMAGE_NAME"] != "demo" {
		t.Fatalf("IMAGE_NAME = %q", got["IMAGE_NAME"])
	}
}
