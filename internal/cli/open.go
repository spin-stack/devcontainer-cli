package cli

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/devcontainers/cli/internal/config"
	"github.com/spf13/cobra"
)

type openOpts struct {
	workspaceFolder string
	configPath      string
	editor          string
	dryRun          bool
}

// fileURIJSON is VS Code's serialized `file:` URI for the dev container config,
// as embedded in the dev-container remote authority. On Linux there is no
// authority component (it only appears for WSL/UNC paths on Windows).
type fileURIJSON struct {
	Scheme    string `json:"scheme"`
	Authority string `json:"authority,omitempty"`
	Path      string `json:"path"`
}

// devcontainerURIJSON is the JSON payload hex-encoded into the
// `dev-container+<hex>` remote authority. Field names/shape match what the VS
// Code Dev Containers resolver expects (verified against the vscli launcher).
type devcontainerURIJSON struct {
	HostPath   string      `json:"hostPath"`
	ConfigFile fileURIJSON `json:"configFile"`
}

func newOpenCmd() *cobra.Command {
	var opts openOpts

	cmd := &cobra.Command{
		Use:   "open [path]",
		Short: "Open a workspace in VS Code, attached to its dev container",
		Long: `Open a workspace folder in VS Code inside its dev container.

This builds the vscode-remote:// dev-container URI for the workspace and launches
the editor with it. VS Code's Dev Containers extension then provisions (or
reconnects to) the container — so 'open' pairs with 'up': run 'devcontainer up'
first to provision, then 'devcontainer open' to attach the editor.

Go-only command; not part of the upstream @devcontainers/cli.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && opts.workspaceFolder == "" {
				opts.workspaceFolder = args[0]
			}
			return runOpen(cmd.Context(), outputFor(cmd), opts)
		},
	}

	f := cmd.Flags()
	f.StringVar(&opts.workspaceFolder, "workspace-folder", "", "Workspace folder path (defaults to [path] or the current directory).")
	f.StringVar(&opts.configPath, "config", "", "devcontainer.json path.")
	f.StringVar(&opts.editor, "editor", "code", "Editor launcher binary (e.g. code, code-insiders, cursor).")
	f.BoolVar(&opts.dryRun, "dry-run", false, "Print the folder URI and launch command without opening the editor.")

	return cmd
}

func runOpen(ctx context.Context, out Output, opts openOpts) error {
	workspaceFolder := opts.workspaceFolder
	if workspaceFolder == "" {
		workspaceFolder, _ = os.Getwd()
	}
	workspaceFolder = resolvePath(workspaceFolder)

	configPath := ""
	if opts.configPath != "" {
		configPath = resolvePath(opts.configPath)
	}

	// Load the config to discover the resolved devcontainer.json path and the
	// workspace folder *inside* the container (the URI's trailing path).
	loadResult, err := config.LoadDevContainerConfig(workspaceFolder, configPath, "")
	if err != nil {
		return err
	}

	localConfigPath := loadResult.Config.ConfigFilePath
	containerFolder := ""
	if loadResult.WorkspaceConfig != nil {
		containerFolder = loadResult.WorkspaceConfig.WorkspaceFolder
	}
	if containerFolder == "" {
		return fmt.Errorf("could not determine the container workspace folder for %s", workspaceFolder)
	}

	uri, err := devcontainerFolderURI(workspaceFolder, localConfigPath, containerFolder)
	if err != nil {
		return err
	}

	editor := opts.editor
	if editor == "" {
		editor = "code"
	}

	if opts.dryRun {
		fmt.Fprintln(out.Stdout(), uri)
		fmt.Fprintf(out.Stderr(), "%s --folder-uri %s\n", editor, uri)
		return nil
	}

	fmt.Fprintf(out.Stderr(), "Opening %s in VS Code (dev container)...\n", workspaceFolder)
	launch := exec.CommandContext(ctx, editor, "--folder-uri", uri)
	launch.Stdout = out.Stdout()
	launch.Stderr = out.Stderr()
	if err := launch.Run(); err != nil {
		return fmt.Errorf("launch %q: %w (is the editor's launcher on PATH? override with --editor)", editor, err)
	}
	return nil
}

// devcontainerFolderURI builds the vscode-remote:// folder URI that opens
// containerFolder inside the dev container for the given local workspace and
// config. The remote authority is `dev-container+<hex>` where <hex> is the
// hex-encoded JSON {hostPath, configFile:{scheme,path}}.
func devcontainerFolderURI(localWorkspaceFolder, localConfigPath, containerFolder string) (string, error) {
	payload := devcontainerURIJSON{
		HostPath: localWorkspaceFolder,
		ConfigFile: fileURIJSON{
			Scheme: "file",
			// Linux fs paths already use forward slashes; use the absolute path as-is.
			Path: filepath.ToSlash(localConfigPath),
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	authority := "dev-container+" + hex.EncodeToString(data)
	return "vscode-remote://" + authority + containerFolder, nil
}
