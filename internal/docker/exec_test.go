package docker

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"testing"

	mobyclient "github.com/moby/moby/client"

	"github.com/devcontainers/cli/internal/log"
)

// nopConn is a net.Conn whose only real method is Close (HijackedResponse.Close
// calls Conn.Close); the embedded nil Conn is never otherwise used in the test.
type nopConn struct{ net.Conn }

func (nopConn) Close() error { return nil }

// stdcopyFrame writes one stdcopy frame (8-byte header: stream type + big-endian
// size, then payload) — the multiplexed format the non-TTY exec stream uses.
func stdcopyFrame(buf *bytes.Buffer, streamType byte, payload string) {
	var hdr [8]byte
	hdr[0] = streamType // 1=stdout, 2=stderr
	binary.BigEndian.PutUint32(hdr[4:], uint32(len(payload)))
	buf.Write(hdr[:])
	buf.WriteString(payload)
}

// framedExecStream builds a stdcopy-multiplexed stream carrying the given stdout
// and stderr payloads, as the non-TTY exec attach endpoint would return.
func framedExecStream(_ *testing.T, out, errText string) *bufio.Reader {
	var buf bytes.Buffer
	stdcopyFrame(&buf, 1, out)
	stdcopyFrame(&buf, 2, errText)
	return bufio.NewReader(&buf)
}

func TestExecInContainer(t *testing.T) {
	var gotOpts mobyclient.ExecCreateOptions
	api := &mockAPI{
		execCreateFn: func(_ context.Context, id string, opts mobyclient.ExecCreateOptions) (mobyclient.ExecCreateResult, error) {
			if id != "cid" {
				t.Errorf("containerID = %q", id)
			}
			gotOpts = opts
			return mobyclient.ExecCreateResult{ID: "exec1"}, nil
		},
		execAttachFn: func(_ context.Context, execID string, _ mobyclient.ExecAttachOptions) (mobyclient.ExecAttachResult, error) {
			if execID != "exec1" {
				t.Errorf("execID = %q", execID)
			}
			return mobyclient.ExecAttachResult{HijackedResponse: mobyclient.HijackedResponse{
				Conn:   nopConn{},
				Reader: framedExecStream(t, "hello\n", "oops\n"),
			}}, nil
		},
		execInspectFn: func(_ context.Context, _ string) (mobyclient.ExecInspectResult, error) {
			return mobyclient.ExecInspectResult{ExitCode: 3}, nil
		},
	}
	e := &EngineClient{API: api, Log: log.Null}

	stdout, stderr, code, err := e.ExecInContainer(context.Background(), "cid", "vscode", []string{"FOO=bar"}, "echo hello")
	if err != nil {
		t.Fatal(err)
	}
	if stdout != "hello\n" {
		t.Errorf("stdout = %q, want hello", stdout)
	}
	if stderr != "oops\n" {
		t.Errorf("stderr = %q, want oops", stderr)
	}
	if code != 3 {
		t.Errorf("exitCode = %d, want 3", code)
	}
	// The command is run as `/bin/sh -c <command>` with the given user/env.
	if gotOpts.User != "vscode" || len(gotOpts.Env) != 1 || gotOpts.Env[0] != "FOO=bar" {
		t.Errorf("opts user/env = %q/%v", gotOpts.User, gotOpts.Env)
	}
	if len(gotOpts.Cmd) != 3 || gotOpts.Cmd[0] != "/bin/sh" || gotOpts.Cmd[1] != "-c" || gotOpts.Cmd[2] != "echo hello" {
		t.Errorf("cmd = %v", gotOpts.Cmd)
	}
	if gotOpts.TTY {
		t.Error("exec must be non-TTY for stdcopy demux")
	}
}
