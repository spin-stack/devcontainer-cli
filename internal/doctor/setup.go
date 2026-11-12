package doctor

import (
	"context"
	"fmt"
	"strings"
)

const builderName = "devcontainer"

type Action struct {
	Name    string `json:"name"`
	Applied bool   `json:"applied"`
	Message string `json:"message"`
	Err     string `json:"error,omitempty"`
}

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
			actions = append(actions, Action{
				Name:    res.Name,
				Applied: false,
				Message: "manual action required: " + res.Remediation,
			})
		}
	}
	return actions
}

func ensureCacheBuilder(ctx context.Context, env *Env, dryRun bool) Action {
	a := Action{Name: "build-cache-export"}

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

func SetupSucceeded(actions []Action) bool {
	for _, a := range actions {
		if strings.TrimSpace(a.Err) != "" {
			return false
		}
	}
	return true
}
