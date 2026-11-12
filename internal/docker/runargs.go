package docker

import (
	"fmt"
	"strings"

	"github.com/moby/moby/api/types/mount"
)

func ParseMountSpec(spec string) (mount.Mount, error) {
	m := mount.Mount{}
	for part := range strings.SplitSeq(spec, ",") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			if k == "readonly" || k == "ro" {
				m.ReadOnly = true
			}
			continue
		}
		switch k {
		case "type":
			m.Type = mount.Type(v)
		case "source", "src":
			m.Source = v
		case "target", "destination", "dst":
			m.Target = v
		case "readonly", "ro":
			m.ReadOnly = v == "" || v == "true" || v == "1"
		case "consistency":
			m.Consistency = mount.Consistency(v)
		}
	}
	if m.Target == "" {
		return m, fmt.Errorf("mount requires a target/destination")
	}
	if m.Type == "" {
		m.Type = mount.TypeBind
	}
	return m, nil
}

func CreateContainerArgs(
	imageName string,
	labels []string,
	env []string,
	mounts []mount.Mount,
	user string,
	entrypoint []string,
	cmd []string,
	capAdd []string,
	securityOpt []string,
	privileged bool,
	init *bool,
	runArgs []string,
) []string {
	args := []string{"create"}

	for _, l := range labels {
		args = append(args, "-l", l)
	}
	for _, m := range mounts {
		parts := []string{fmt.Sprintf("type=%s", m.Type)}
		if m.Source != "" {
			parts = append(parts, fmt.Sprintf("source=%s", m.Source))
		}
		if m.Target != "" {
			parts = append(parts, fmt.Sprintf("target=%s", m.Target))
		}
		if m.ReadOnly {
			parts = append(parts, "readonly")
		}
		if m.Consistency != "" {
			parts = append(parts, fmt.Sprintf("consistency=%s", m.Consistency))
		}
		args = append(args, "--mount", strings.Join(parts, ","))
	}
	for _, e := range env {
		args = append(args, "-e", e)
	}
	for _, c := range capAdd {
		args = append(args, "--cap-add", c)
	}
	for _, s := range securityOpt {
		args = append(args, "--security-opt", s)
	}
	if privileged {
		args = append(args, "--privileged")
	}
	if init != nil && *init {
		args = append(args, "--init")
	}
	if user != "" {
		args = append(args, "-u", user)
	}

	args = append(args, runArgs...)

	if len(entrypoint) > 0 {
		args = append(args, "--entrypoint", entrypoint[0])
	}

	args = append(args, imageName)
	args = append(args, cmd...)

	return args
}
