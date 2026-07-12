// Package templates applies and publishes dev container Templates.
package templates

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/devcontainers/cli/internal/log"
	"github.com/devcontainers/cli/internal/oci"
	"github.com/devcontainers/cli/internal/pfs"
	"github.com/tailscale/hujson"
)

// ApplyParams holds parameters for template application.
type ApplyParams struct {
	// OCIClient is the registry seam (interface, not *oci.Client) so tests can
	// inject a fake registry.
	OCIClient oci.Registry
	// FS is the filesystem seam. When nil, the default OS-backed FS is used.
	FS              pfs.FS
	Logger          log.Logger
	Env             map[string]string
	WorkspaceFolder string
	TmpDir          string
}

// FetchAndApply downloads a template from OCI and applies it to the workspace.
// Returns the list of files created.
func FetchAndApply(params ApplyParams, selected SelectedTemplate) ([]string, error) {
	logger := params.Logger
	fsys := params.FS
	if fsys == nil {
		fsys = pfs.DefaultFS()
	}

	// Parse template ID as OCI ref
	ref, err := oci.ParseRef(selected.ID)
	if err != nil {
		return nil, fmt.Errorf("parse template ID %q: %w", selected.ID, err)
	}

	// Fetch manifest
	manifest, err := params.OCIClient.FetchManifest(ref, "")
	if err != nil {
		return nil, fmt.Errorf("fetch template manifest: %w", err)
	}

	if len(manifest.Manifest.Layers) == 0 {
		return nil, fmt.Errorf("template manifest has no layers")
	}

	// Download the first layer (tarball)
	layer := manifest.Manifest.Layers[0]
	blobData, err := params.OCIClient.FetchBlob(ref, layer.Digest)
	if err != nil {
		return nil, fmt.Errorf("fetch template blob: %w", err)
	}

	// Extract to temp dir
	tmpDir := params.TmpDir
	if tmpDir == "" {
		tmpDir = os.TempDir()
	}
	extractDir := filepath.Join(tmpDir, "template-"+ref.ID)
	if err := fsys.MkdirAll(extractDir); err != nil {
		return nil, fmt.Errorf("create extract dir: %w", err)
	}

	// Write tarball and extract it into extractDir.
	tarPath := filepath.Join(extractDir, "template.tar")
	if err := fsys.WriteFile(tarPath, blobData); err != nil {
		return nil, fmt.Errorf("write tarball: %w", err)
	}
	if err := pfs.ExtractTarGz(tarPath, extractDir); err != nil {
		return nil, fmt.Errorf("extract template tarball: %w", err)
	}

	// Fill in option defaults from devcontainer-template.json for any option the
	// user did not provide (matching the TS CLI), then use the merged set for
	// ${templateOption:x} substitution.
	options := applyOptionDefaults(fsys, extractDir, selected.Options, logger)

	// Build omit sets. Always omit the template's own metadata/doc files
	// (matching the TS CLI: devcontainer-template.json, README.md, NOTES.md).
	omitDirs := make(map[string]bool)
	omitFiles := map[string]bool{
		"devcontainer-template.json": true,
		"README.md":                  true,
		"NOTES.md":                   true,
	}
	for _, p := range selected.OmitPaths {
		if strings.HasSuffix(p, "/*") {
			omitDirs[strings.TrimSuffix(p, "/*")+"/"] = true
		} else {
			omitFiles[p] = true
		}
	}

	logger.Write(fmt.Sprintf("Template %q resolved to %s", selected.ID, manifest.CanonicalID), log.LevelTrace)

	// Apply template options (replace ${templateOption:name} in files)
	var appliedFiles []string
	err = fsys.Walk(extractDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		relPath, _ := filepath.Rel(extractDir, path)

		// Skip omitted files
		cleanedPath := strings.ReplaceAll(relPath, "\\", "/")
		cleanedPath = strings.TrimPrefix(cleanedPath, "./")
		if omitFiles[cleanedPath] {
			return nil
		}
		for dir := range omitDirs {
			if strings.HasPrefix(cleanedPath, dir) {
				return nil
			}
		}

		// Skip the tarball itself
		if relPath == "template.tar" {
			return nil
		}

		// Read, substitute, write to workspace
		data, err := fsys.ReadFile(path)
		if err != nil {
			return err
		}
		content := substituteTemplateOptions(string(data), options)

		destPath := filepath.Join(params.WorkspaceFolder, relPath)
		if err := fsys.MkdirAll(filepath.Dir(destPath)); err != nil {
			return err
		}
		if err := fsys.WriteFile(destPath, []byte(content)); err != nil {
			return err
		}
		// Report paths as "./<rel>" with forward slashes, matching the TS CLI.
		appliedFiles = append(appliedFiles, "./"+cleanedPath)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("apply template files: %w", err)
	}

	// Merge features into devcontainer.json if specified (matching TS behavior).
	if len(selected.Features) > 0 {
		if err := mergeFeatures(fsys, params.WorkspaceFolder, selected.Features, logger); err != nil {
			return nil, fmt.Errorf("merge features into config: %w", err)
		}
	}

	return appliedFiles, nil
}

// mergeFeatures adds features to the devcontainer.json in the workspace.
func mergeFeatures(fsys pfs.FS, workspaceFolder string, featureOpts []TemplateFeatureOption, logger log.Logger) error {
	// Find devcontainer.json
	candidates := []string{
		filepath.Join(workspaceFolder, ".devcontainer", "devcontainer.json"),
		filepath.Join(workspaceFolder, ".devcontainer.json"),
	}
	var configPath string
	for _, c := range candidates {
		if _, err := fsys.Stat(c); err == nil {
			configPath = c
			break
		}
	}
	if configPath == "" {
		logger.Write("No devcontainer.json found to merge features into", log.LevelWarning)
		return nil
	}

	data, err := fsys.ReadFile(configPath)
	if err != nil {
		return err
	}

	// Read the existing features (standardized) to skip duplicates and know
	// whether the "features" key already exists. Standardize blanks comments in
	// place, so pass a copy to keep `data` intact for the format-preserving Parse.
	stdData, stdErr := hujson.Standardize(append([]byte(nil), data...))
	if stdErr != nil {
		return fmt.Errorf("parse %s: %w", configPath, stdErr)
	}
	var config map[string]json.RawMessage
	json.Unmarshal(stdData, &config)
	existing := map[string]bool{}
	_, hasFeatures := config["features"]
	if hasFeatures {
		var feats map[string]json.RawMessage
		if json.Unmarshal(config["features"], &feats) == nil {
			for k := range feats {
				existing[k] = true
			}
		}
	}

	// Build a JSON Patch and apply it via hujson so the edit preserves the
	// original formatting and comments (JSONC), matching the TS CLI which uses a
	// text-preserving jsonc edit instead of a reformatting re-serialize.
	var ops []map[string]interface{}
	if !hasFeatures {
		ops = append(ops, map[string]interface{}{"op": "add", "path": "/features", "value": map[string]interface{}{}})
	}
	for _, f := range featureOpts {
		if existing[f.ID] {
			continue
		}
		var value interface{} = "latest"
		if len(f.Options) > 0 {
			value = f.Options
		}
		ops = append(ops, map[string]interface{}{
			"op":    "add",
			"path":  "/features/" + escapeJSONPointer(f.ID),
			"value": value,
		})
	}
	if len(ops) == 0 {
		return nil
	}

	v, err := hujson.Parse(data)
	if err != nil {
		return fmt.Errorf("parse %s: %w", configPath, err)
	}
	patch, _ := json.Marshal(ops)
	if err := v.Patch(patch); err != nil {
		return fmt.Errorf("merge features into %s: %w", configPath, err)
	}
	return fsys.WriteFile(configPath, v.Pack())
}

// escapeJSONPointer escapes a string for use as a JSON Pointer reference token
// (RFC 6901): "~" -> "~0", "/" -> "~1". Feature IDs contain "/".
func escapeJSONPointer(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	return s
}

// applyOptionDefaults returns a copy of userOptions with any option declared in
// the template's devcontainer-template.json filled in from its `default` when
// the user did not provide a value (matching the TS CLI). string and boolean
// option defaults are supported.
func applyOptionDefaults(fsys pfs.FS, extractDir string, userOptions map[string]string, logger log.Logger) map[string]string {
	merged := make(map[string]string, len(userOptions))
	for k, v := range userOptions {
		merged[k] = v
	}

	metaPath := filepath.Join(extractDir, "devcontainer-template.json")
	data, err := fsys.ReadFile(metaPath)
	if err != nil {
		return merged
	}
	var meta TemplateMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		return merged
	}
	for key, raw := range meta.Options {
		if _, ok := merged[key]; ok {
			continue
		}
		def, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		switch d := def["default"].(type) {
		case string:
			merged[key] = d
		case bool:
			if d {
				merged[key] = "true"
			} else {
				merged[key] = "false"
			}
		default:
			continue
		}
		logger.Write(fmt.Sprintf("Using default value for %s --> %s", key, merged[key]), log.LevelTrace)
	}
	return merged
}

var templateOptionRegex = regexp.MustCompile(`\$\{templateOption:\s*(\w+)\s*\}`)

// substituteTemplateOptions replaces ${templateOption:name} with the provided values.
func substituteTemplateOptions(content string, options map[string]string) string {
	return templateOptionRegex.ReplaceAllStringFunc(content, func(match string) string {
		sub := templateOptionRegex.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		name := sub[1]
		if val, ok := options[name]; ok {
			return val
		}
		return match
	})
}
