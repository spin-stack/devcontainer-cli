package doctor

import (
	"context"
	"fmt"
	"strings"
)

// builderName is the buildx builder `setup` creates to enable build-cache
// export without touching /etc/docker/daemon.json (no sudo required).
const builderName = "devcontainer"

// Action is the outcome of one remediation step taken by Setup.
type Action struct {
	Name string `json:"name"`
	// Applied is true when Setup changed system state (or would have, in dry-run).
	Applied bool   `json:"applied"`
	Message string `json:"message"`
	// Err holds a failure message when a remediation was attempted but failed.
	Err string `json:"error,omitempty"`
}

// Setup applies the automatic remediations for the fixable checks in report.
// When dryRun is true it reports what it would do without changing anything.
// Non-fixable checks (missing buildx/compose, low disk) are surfaced as
// manual-action messages rather than being applied.
func Setup(ctx context.Context, env *Env, report Report, dryRun bool) []Action {
	if env == nil {
		env = &Env{}
	}
	var actions []Action
	for _, res := range report.Results {
		switch {
		case res.Status == StatusOK:
			continue
		case res.Name == "build-cache-export" && res.Fixable:
			actions = append(actions, ensureCacheBuilder(ctx, env, dryRun))
		default:
			// Not auto-remediable (needs a package manager / sudo / free disk).
			actions = append(actions, Action{
				Name:    res.Name,
				Applied: false,
				Message: "manual action required: " + res.Remediation,
			})
		}
	}
	return actions
}

// ensureCacheBuilder creates and selects a docker-container buildx builder so
// cache export / --output / cross-platform builds work, without requiring the
// containerd image store (which needs a daemon config change + restart).
func ensureCacheBuilder(ctx context.Context, env *Env, dryRun bool) Action {
	a := Action{Name: "build-cache-export"}

	// Already present? `buildx inspect <name>` exits 0 when it exists.
	if _, _, code, err := env.runner().Run(ctx, nil, env.dockerPath(), "buildx", "inspect", builderName); err == nil && code == 0 {
		a.Message = fmt.Sprintf("buildx builder %q already exists; selecting it as the default", builderName)
		if dryRun {
			return a
		}
		if _, stderr, code, err := env.runner().Run(ctx, nil, env.dockerPath(), "buildx", "use", builderName); err != nil || code != 0 {
			a.Err = builderErr(err, stderr)
			return a
		}
		a.Applied = true
		return a
	}

	a.Message = fmt.Sprintf("create docker-container buildx builder %q and select it as the default", builderName)
	if dryRun {
		return a
	}
	_, stderr, code, err := env.runner().Run(ctx, nil, env.dockerPath(),
		"buildx", "create", "--name", builderName, "--driver", "docker-container", "--use", "--bootstrap")
	if err != nil || code != 0 {
		a.Err = builderErr(err, stderr)
		return a
	}
	a.Applied = true
	a.Message = fmt.Sprintf("created and selected docker-container buildx builder %q", builderName)
	return a
}

func builderErr(err error, stderr []byte) string {
	if msg := firstLine(string(stderr)); msg != "" {
		return msg
	}
	if err != nil {
		return err.Error()
	}
	return "command failed"
}

// SetupSucceeded reports whether every attempted remediation either applied or
// required no change (i.e. no Action carries an error).
func SetupSucceeded(actions []Action) bool {
	for _, a := range actions {
		if strings.TrimSpace(a.Err) != "" {
			return false
		}
	}
	return true
}
