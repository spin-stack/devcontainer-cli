package docker

import (
	"bytes"
	"context"

	"github.com/moby/moby/api/pkg/stdcopy"
	mobyclient "github.com/moby/moby/client"
)

// ExecInContainer runs `/bin/sh -c command` inside a container via the Docker
// exec API (no `docker exec` subprocess) and returns its stdout, stderr and exit
// code. It runs non-interactively (no TTY): stdout/stderr are demultiplexed with
// stdcopy, and the exit code is read from the exec inspect after the stream ends.
//
// user (docker exec -u) and env ("KEY=VALUE", docker exec -e) mirror how the
// lifecycle shell runs hooks and the userEnvProbe as the remote user.
func (e *EngineClient) ExecInContainer(ctx context.Context, containerID, user string, env []string, command string) (stdout, stderr string, exitCode int, err error) {
	created, err := e.API.ExecCreate(ctx, containerID, mobyclient.ExecCreateOptions{
		User:         user,
		Env:          env,
		Cmd:          []string{"/bin/sh", "-c", command},
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return "", "", -1, err
	}

	attach, err := e.API.ExecAttach(ctx, created.ID, mobyclient.ExecAttachOptions{})
	if err != nil {
		return "", "", -1, err
	}
	defer attach.Close()

	var outBuf, errBuf bytes.Buffer
	// Non-TTY exec output is a multiplexed stdout/stderr frame stream.
	if _, copyErr := stdcopy.StdCopy(&outBuf, &errBuf, attach.Reader); copyErr != nil {
		return outBuf.String(), errBuf.String(), -1, copyErr
	}

	inspect, err := e.API.ExecInspect(ctx, created.ID, mobyclient.ExecInspectOptions{})
	if err != nil {
		return outBuf.String(), errBuf.String(), -1, err
	}
	return outBuf.String(), errBuf.String(), inspect.ExitCode, nil
}
