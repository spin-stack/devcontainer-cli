package clihost

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
)

// CLIHost abstracts the local host environment: filesystem, exec, and system info.
// Designed as an interface to support testing and future SSH/WSL hosts.
type CLIHost interface {
	// Filesystem
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte) error
	MkdirAll(path string) error
	IsFile(path string) bool
	IsDir(path string) bool

	// System info (names match Node.js conventions for TS parity)
	Platform() string // "linux", "darwin", "win32"
	Arch() string     // "x64", "arm64"
	HomeDir() string
	TmpDir() string
	Cwd() string
	Username() string
	UID() int
	GID() int
	Env() map[string]string

	// Process execution
	Exec(params ExecParams) (*ExecResult, error)
}

// ExecParams configures a process execution.
type ExecParams struct {
	Cmd  string
	Args []string
	Cwd  string
	Env  map[string]string
}

// ExecResult captures process output.
type ExecResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// localHost implements CLIHost for the local machine.
type localHost struct {
	cwd      string
	env      map[string]string
	platform string
	arch     string
	homeDir  string
	tmpDir   string
	username string
	uid      int
	gid      int
}

// NewLocal creates a CLIHost for the local filesystem.
func NewLocal(cwd string) (CLIHost, error) {
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working directory: %w", err)
		}
	}

	env := envToMap(os.Environ())

	h := &localHost{
		cwd:      cwd,
		env:      env,
		platform: goOSToNodePlatform(runtime.GOOS),
		arch:     goArchToNodeArch(runtime.GOARCH),
		homeDir:  os.TempDir(), // fallback
		tmpDir:   os.TempDir(),
	}

	if home, err := os.UserHomeDir(); err == nil {
		h.homeDir = home
	}

	if u, err := user.Current(); err == nil {
		h.username = u.Username
		h.uid, _ = strconv.Atoi(u.Uid)
		h.gid, _ = strconv.Atoi(u.Gid)
	}

	return h, nil
}

func (h *localHost) ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
func (h *localHost) WriteFile(path string, data []byte) error {
	return os.WriteFile(path, data, 0644)
}
func (h *localHost) MkdirAll(path string) error { return os.MkdirAll(path, 0755) }

func (h *localHost) IsFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func (h *localHost) IsDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func (h *localHost) Platform() string       { return h.platform }
func (h *localHost) Arch() string           { return h.arch }
func (h *localHost) HomeDir() string        { return h.homeDir }
func (h *localHost) TmpDir() string         { return h.tmpDir }
func (h *localHost) Cwd() string            { return h.cwd }
func (h *localHost) Username() string       { return h.username }
func (h *localHost) UID() int               { return h.uid }
func (h *localHost) GID() int               { return h.gid }
func (h *localHost) Env() map[string]string { return h.env }

func (h *localHost) Exec(params ExecParams) (*ExecResult, error) {
	cmd := exec.Command(params.Cmd, params.Args...)
	if params.Cwd != "" {
		cmd.Dir = params.Cwd
	}
	if params.Env != nil {
		cmd.Env = mapToEnv(params.Env)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("exec %s: %w", params.Cmd, err)
		}
	}

	return &ExecResult{
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
		ExitCode: exitCode,
	}, nil
}

// --- Platform mapping (Go names → Node.js names for TS parity) ---

func goOSToNodePlatform(goos string) string {
	switch goos {
	case "windows":
		return "win32"
	default:
		return goos // "linux", "darwin"
	}
}

func goArchToNodeArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	default:
		return goarch // "arm64"
	}
}

// --- Go names for OCI spec (Node arch/os → GOOS/GOARCH) ---

// PlatformToGOOS converts Node.js platform string to Go OS.
func PlatformToGOOS(platform string) string {
	switch platform {
	case "win32":
		return "windows"
	default:
		return platform
	}
}

// ArchToGOARCH converts Node.js arch string to Go arch.
func ArchToGOARCH(arch string) string {
	switch arch {
	case "x64":
		return "amd64"
	default:
		return arch
	}
}

// --- Env helpers ---

func envToMap(environ []string) map[string]string {
	m := make(map[string]string, len(environ))
	for _, e := range environ {
		for i := 0; i < len(e); i++ {
			if e[i] == '=' {
				m[e[:i]] = e[i+1:]
				break
			}
		}
	}
	return m
}

func mapToEnv(m map[string]string) []string {
	env := make([]string, 0, len(m))
	for k, v := range m {
		env = append(env, k+"="+v)
	}
	return env
}

// PathJoin wraps filepath.Join for the host platform.
func PathJoin(parts ...string) string {
	return filepath.Join(parts...)
}
