package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/devcontainers/cli/internal/config"
	"github.com/devcontainers/cli/internal/docker"
	"github.com/devcontainers/cli/internal/features"
	"github.com/devcontainers/cli/internal/imagemeta"
	"github.com/devcontainers/cli/internal/log"
)

const autoAddedStageLabel = "dev_container_auto_added_stage_label"

const featuresDockerfileName = "Dockerfile-with-features"

// mutate-lane: TestBuildDockerfileBuildsOnce, TestBuildDockerfileWritesTheReferenceDockerfile,
// TestUpDockerfileBuildsOnce and TestParityMatrix (build.dockerfile-multi-image-success,
// build.read-only-workspace-dockerfile-success, up.read-only-workspace-dockerfile-success),
// also run by TestE2E_BuildUpExec_Dockerfile, TestE2E_SiblingDockerignore,
// TestE2E_UpSecretsFile and TestE2E_CABundle.
func buildDockerfileImage(ctx context.Context, logger log.Logger, dockerClient *docker.Client, engine *docker.EngineClient, cfg *config.DevContainer, useBuildx bool, imageNames []string, basePlatform string, fb *FeatureBuildOptions) error {
	if cfg.Dockerfile() == "" {
		return fmt.Errorf("no Dockerfile specified")
	}
	dockerfilePath := dockerfileConfigPath(cfg)
	content, err := os.ReadFile(dockerfilePath)
	if err != nil {
		return fmt.Errorf("read Dockerfile: %w", err)
	}
	dockerfile := string(content)
	target := ""
	if cfg.Build != nil {
		target = cfg.Build.Target
	}
	baseName := target
	if baseName == "" {
		name, modified, err := docker.EnsureFinalStageName(dockerfile, autoAddedStageLabel)
		if err != nil {
			return err
		}
		baseName = name
		if modified != "" {
			dockerfile = modified
		}
	}

	parsed := docker.ExtractDockerfile(string(content))
	configArgs := buildArgsFromConfig(cfg)
	resolver := docker.NewResolver(parsed, configArgs, buildxPlatformArgs(basePlatform))
	baseImage := resolver.FindBaseImage(target)
	logger.Write(fmt.Sprintf("Base image: %s", baseImage), log.LevelInfo)

	baseLabels := engine.ImageLabelsEnsuringPresent(ctx, baseImage)
	var baseEnv map[string]string
	imageUser := ""
	if info, err := engine.InspectImage(ctx, baseImage); err == nil && info.Config != nil {
		baseEnv, imageUser = config.EnvMap(info.Config.Env), info.Config.User
	}
	if user := resolver.FindUserStatement(baseEnv, target); user != "" {
		imageUser = user
	}
	if imageUser == "" {
		imageUser = "root"
	}
	imageEntries := slices.Concat(
		imagemeta.ReadMetadataFromLabels(baseLabels, logger),
		imagemeta.ReadMetadataFromLabels(parsed.StageLabels(baseName), logger),
	)

	fetched, err := fetchBuildFeatures(ctx, logger, cfg.Features, fb)
	if err != nil {
		return err
	}

	userSyntax := parsed.Preamble.Directives["syntax"]
	var info *imagemeta.ExtendImageBuildInfo
	var folder string
	var secretIDs []string
	caSecrets := caBundleBuildSecrets(fb.CABundle, useBuildx, logger)
	if fetched == nil {
		if folder, err = createFeaturesFolder(); err != nil {
			return err
		}
		syntax := userSyntax
		if fb.OmitSyntaxDirective {
			syntax = ""
		}
		info = imagemeta.GenerateComposeLabelBuild(baseName, slices.Concat(imageEntries, fb.LabelMetadata), syntax)
	} else {
		folder = fetched.TmpDir
		var featureEntries []imagemeta.Entry
		for _, fs := range fetched.FeatureSets {
			featureEntries = append(featureEntries, featureMetadataEntry(fs, fb.SkipPersistCustoms))
		}
		containerUser, remoteUser := findContainerUsers(slices.Concat(imageEntries, featureEntries, fb.AdditionalMetadata), "", imageUser)
		if err := stageFeatureBuildFiles(folder, fetched.FeatureSets, containerUser, remoteUser); err != nil {
			return err
		}
		if useBuildx {
			secretIDs = buildSecretIDs(fb.Secrets)
		}
		info = imagemeta.GenerateFeaturesBuildOptions(imagemeta.FeaturesBuildParams{
			BaseName:            baseName,
			FeatureSets:         fetched.FeatureSets,
			Metadata:            slices.Concat(imageEntries, featureEntries, fb.LabelMetadata),
			ContainerUser:       containerUser,
			RemoteUser:          remoteUser,
			ImageUser:           imageUser,
			UseBuildKitContexts: useBuildx,
			Syntax:              featuresSyntaxDirective(ctx, engine, fb.OmitSyntaxDirective, useBuildx, parsed),
			ConfigContainerEnv:  fb.ContainerEnv,
			ConfigEnvOrder:      fb.ContainerEnvOrder,
			SecretIDs:           secretIDs,
			CABundle:            caSecrets != nil,
		})
		if !useBuildx {
			if err := buildFeatureContentImage(ctx, dockerClient, folder); err != nil {
				return err
			}
		}
	}

	if loc := composeSyntaxLineRe.FindStringIndex(dockerfile); loc != nil {
		dockerfile = dockerfile[loc[1]:]
	}
	finalPath := filepath.Join(folder, featuresDockerfileName)
	final := info.DockerfilePrefixContent + dockerfile + "\n" + info.DockerfileContent
	if err := os.WriteFile(finalPath, []byte(final), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", featuresDockerfileName, err)
	}
	copyDockerignore(dockerfilePath, finalPath)
	logger.Write("Generated Dockerfile:\n"+final, log.LevelTrace)

	extraArgs := slices.Clone(buildOptionsFromConfig(cfg))
	for _, k := range sortedStringKeys(info.BuildKitContexts) {
		v := info.BuildKitContexts[k]
		if v == "." {
			v = folder
		}
		extraArgs = append(extraArgs, "--build-context", k+"="+v)
	}
	for _, k := range featureBuildArgOrder(info.BuildArgs) {
		extraArgs = append(extraArgs, "--build-arg", k+"="+info.BuildArgs[k])
	}

	cacheFrom := cacheFromForDockerfileBuild(fb.CacheFrom, cfg)
	authEnv, authCleanup := bridgeBuildAuth(logger, baseImage, imageNames, cacheFrom, fb.CacheTo)
	defer authCleanup()

	result, err := dockerClient.Build(ctx, docker.BuildOptions{
		Dockerfile:  finalPath,
		ContextPath: dockerContextPath(cfg),
		Tags:        imageNames,
		Target:      info.OverrideTarget,
		BuildArgs:   orderedBuildArgs(configArgs, features.PropertyOrder(configArgs, cfg.BuildArgsOrder)),
		CacheFrom:   cacheFrom,
		Labels:      fb.Labels,
		NoCache:     fb.NoCache,
		ExtraArgs:   extraArgs,
		UseBuildx:   useBuildx,
		Platform:    fb.Platform,
		Push:        fb.Push,
		Output:      fb.Output,
		CacheTo:     fb.CacheTo,
		Env:         authEnv,
		Secrets:     buildxSecrets(fb.Secrets, useBuildx, logger),
		FileSecrets: caSecrets,
	})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf(msgDockerBuildFailed, result.ExitCode, string(result.Stderr))
	}
	if dockerClient.ProgressWriter == nil {
		logger.Write(string(result.Stderr), log.LevelInfo)
	}
	return nil
}

func orderedBuildArgs(args map[string]string, keys []string) [][2]string {
	out := make([][2]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, [2]string{k, args[k]})
	}
	return out
}

func featureBuildArgOrder(args map[string]string) []string {
	var keys []string
	for _, k := range []string{"_DEV_CONTAINERS_BASE_IMAGE", "_DEV_CONTAINERS_IMAGE_USER", "_DEV_CONTAINERS_FEATURE_CONTENT_SOURCE"} {
		if _, ok := args[k]; ok {
			keys = append(keys, k)
		}
	}
	return keys
}

func featuresSyntaxDirective(ctx context.Context, engine *docker.EngineClient, omit, useContexts bool, parsed *docker.Dockerfile) string {
	userSyntax := ""
	if parsed != nil {
		userSyntax = parsed.Preamble.Directives["syntax"]
	}
	switch {
	case omit:
		return ""
	case engineAtLeast23(ctx, engine):
		return userSyntax
	case useContexts && !dockerfileSupportsBuildContexts(parsed):
		return "docker/dockerfile:1.4"
	}
	return userSyntax
}
