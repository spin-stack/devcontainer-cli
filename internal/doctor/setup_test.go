package doctor

import (
	"context"
	"testing"
)

// cacheWarnReport is a report whose only problem is a fixable cache-export warn.
func cacheWarnReport() Report {
	return Report{
		Overall: StatusWarn,
		Results: []Result{
			{Name: "docker-daemon", Status: StatusOK},
			{Name: "build-cache-export", Status: StatusWarn, Fixable: true, Remediation: "run setup"},
			{Name: "compose-v2", Status: StatusWarn, Remediation: "install compose"},
		},
	}
}

func TestSetupCreatesBuilderWhenAbsent(t *testing.T) {
	sr := &scriptRunner{fn: func(args []string) ([]byte, []byte, int, error) {
		if has(args, "buildx") && has(args, "inspect") {
			return nil, []byte("no builder"), 1, nil // absent
		}
		if has(args, "buildx") && has(args, "create") {
			return []byte("devcontainer\n"), nil, 0, nil
		}
		return nil, nil, 0, nil
	}}
	env := &Env{Runner: sr}

	actions := Setup(context.Background(), env, cacheWarnReport(), false)
	if !SetupSucceeded(actions) {
		t.Fatalf("setup failed: %+v", actions)
	}

	var created bool
	for _, c := range sr.calls {
		if has(c, "create") && has(c, "--driver") && has(c, "docker-container") && has(c, "--use") {
			created = true
		}
	}
	if !created {
		t.Fatalf("expected a `buildx create --driver docker-container --use` call; calls=%v", sr.calls)
	}

	// The compose-v2 warn is not auto-fixable → surfaced as a manual action.
	if a := findAction(t, actions, "compose-v2"); a.Applied || a.Message == "" {
		t.Fatalf("compose-v2 action = %+v, want manual (not applied)", a)
	}
	if a := findAction(t, actions, "build-cache-export"); !a.Applied {
		t.Fatalf("build-cache-export not applied: %+v", a)
	}
}

func TestSetupSelectsExistingBuilder(t *testing.T) {
	sr := &scriptRunner{fn: func(args []string) ([]byte, []byte, int, error) {
		if has(args, "buildx") && has(args, "inspect") {
			return []byte("Name: devcontainer\nDriver: docker-container\n"), nil, 0, nil // present
		}
		return nil, nil, 0, nil
	}}
	env := &Env{Runner: sr}

	actions := Setup(context.Background(), env, cacheWarnReport(), false)
	var used, created bool
	for _, c := range sr.calls {
		if has(c, "use") {
			used = true
		}
		if has(c, "create") {
			created = true
		}
	}
	if created || !used {
		t.Fatalf("existing builder should be `use`d, not created; used=%v created=%v", used, created)
	}
	if !SetupSucceeded(actions) {
		t.Fatalf("setup failed: %+v", actions)
	}
}

func TestSetupDryRunChangesNothing(t *testing.T) {
	sr := &scriptRunner{fn: func(args []string) ([]byte, []byte, int, error) {
		if has(args, "inspect") {
			return nil, nil, 1, nil // absent
		}
		return nil, nil, 0, nil
	}}
	env := &Env{Runner: sr}

	actions := Setup(context.Background(), env, cacheWarnReport(), true)
	for _, c := range sr.calls {
		if has(c, "create") || has(c, "use") {
			t.Fatalf("dry-run must not mutate; saw call %v", c)
		}
	}
	if a := findAction(t, actions, "build-cache-export"); a.Applied {
		t.Fatalf("dry-run action marked applied: %+v", a)
	}
}

func TestSetupReportsBuilderFailure(t *testing.T) {
	sr := &scriptRunner{fn: func(args []string) ([]byte, []byte, int, error) {
		if has(args, "inspect") {
			return nil, nil, 1, nil
		}
		if has(args, "create") {
			return nil, []byte("permission denied\n"), 1, nil
		}
		return nil, nil, 0, nil
	}}
	env := &Env{Runner: sr}

	actions := Setup(context.Background(), env, cacheWarnReport(), false)
	if SetupSucceeded(actions) {
		t.Fatalf("expected setup to report failure; actions=%+v", actions)
	}
	if a := findAction(t, actions, "build-cache-export"); a.Err == "" {
		t.Fatalf("expected error on build-cache-export action: %+v", a)
	}
}

func TestSetupNothingToDoWhenHealthy(t *testing.T) {
	sr := &scriptRunner{fn: func([]string) ([]byte, []byte, int, error) { return nil, nil, 0, nil }}
	env := &Env{Runner: sr}
	healthy := Report{Overall: StatusOK, Results: []Result{{Name: "docker-daemon", Status: StatusOK}}}

	actions := Setup(context.Background(), env, healthy, false)
	if len(actions) != 0 {
		t.Fatalf("healthy host should need no actions, got %+v", actions)
	}
}

func findAction(t *testing.T, actions []Action, name string) Action {
	t.Helper()
	for _, a := range actions {
		if a.Name == name {
			return a
		}
	}
	t.Fatalf("no action named %q in %+v", name, actions)
	return Action{}
}
