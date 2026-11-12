package docker

import (
	"bytes"
	"context"

	"github.com/moby/moby/api/pkg/stdcopy"
	mobyclient "github.com/moby/moby/client"
)

func (e *EngineClient) ExecInContainer(ctx context.Context, containerID, user string, env []string, command string) (stdout, stderr string, exitCode int, err error) {
	const failed = -1 // mutate-exempt: the exit code is unread whenever err is non-nil (lifecycle.ShellServer discards it)
	created, err := e.API.ExecCreate(ctx, containerID, mobyclient.ExecCreateOptions{
		User:         user,
		Env:          env,
		Cmd:          []string{"/bin/sh", "-c", command},
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return "", "", failed, err
	}

	attach, err := e.API.ExecAttach(ctx, created.ID, mobyclient.ExecAttachOptions{})
	if err != nil {
		return "", "", failed, err
	}
	defer attach.Close()

	var outBuf, errBuf bytes.Buffer
	// Non-TTY exec output is a multiplexed stdout/stderr frame stream.
	if _, copyErr := stdcopy.StdCopy(&outBuf, &errBuf, attach.Reader); copyErr != nil {
		return outBuf.String(), errBuf.String(), failed, copyErr
	}

	inspect, err := e.API.ExecInspect(ctx, created.ID, mobyclient.ExecInspectOptions{})
	if err != nil {
		return outBuf.String(), errBuf.String(), failed, err
	}
	return outBuf.String(), errBuf.String(), inspect.ExitCode, nil
}
