package executor

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/tuimeo/elastic-ci-executor/config"
	"github.com/tuimeo/elastic-ci-executor/internal/logger"
	"github.com/tuimeo/elastic-ci-executor/internal/metadata"
	"github.com/tuimeo/elastic-ci-executor/providers"
)

// helperStages lists stages that run in the helper container (git, cache, artifacts).
// All other stages run in the build container.
var helperStages = map[string]bool{
	"prepare_script":              true,
	"get_sources":                 true,
	"clear_worktree":              true,
	"restore_cache":               true,
	"download_artifacts":          true,
	"archive_cache":               true,
	"archive_cache_on_failure":    true,
	"upload_artifacts_on_success": true,
	"upload_artifacts_on_failure": true,
	"cleanup_file_variables":      true,
}

func targetContainerForStage(stageName string) string {
	if helperStages[stageName] {
		return "helper"
	}
	return "build"
}

// PrepareStage implements the "prepare" stage logic
func PrepareStage(ctx context.Context, cfg *config.Config) error {
	// Check job ID first before doing any real work
	jobID, err := GetJobID()
	if err != nil {
		return fmt.Errorf("failed to get job ID: %w", err)
	}

	provider, err := createProvider(cfg)
	if err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
	}

	image := config.RewriteImageForProxy(cfg.ImageProxy, getImageFromEnv(cfg.Image))
	settings := providers.JobContainerSettings{
		Image:                    image,
		BuildCPU:                 getBuildCPUFromEnv(cfg.BuildCPU),
		BuildMemory:              getBuildMemoryFromEnv(cfg.BuildMemory),
		EnvVars:                  buildContainerEnvVars(cfg.EnvVars),
		Command:                  buildKeepAliveCommand(cfg),
		NoSpot:                   getNoSpotFromEnv(cfg.NoSpot),
		ImageProxy:               cfg.ImageProxy,
		ImageRegistryCredentials: convertRegistryCredentials(cfg.ImageRegistryCredentials),
		HelperImage:              cfg.HelperImage,
		HelperCPU:                getHelperCPUFromEnv(cfg.HelperCPU),
		HelperMemory:             getHelperMemoryFromEnv(cfg.HelperMemory),
	}
	if validateErr := settings.Validate(); validateErr != nil {
		return fmt.Errorf("invalid container settings: %w", validateErr)
	}

	store, err := metadata.NewFileStore(cfg.JobStore)
	if err != nil {
		return fmt.Errorf("failed to create metadata store: %w", err)
	}
	defer func() { _ = store.Close() }()

	var container providers.JobContainer
	if debugID := os.Getenv("ECIE_DEBUG_CONTAINER_ID"); debugID != "" {
		logger.Printf("Skipping create, using existing container: %s\n", debugID)
		container = providers.JobContainer{
			Provider:   cfg.Provider,
			Identifier: debugID,
			Extra:      map[string]string{},
		}
	} else {
		logger.Printf("Creating container with image: %s...\n", settings.Image)
		var err error
		container, err = provider.CreateContainer(ctx, settings)
		if err != nil {
			return fmt.Errorf("failed to create container: %w", err)
		}
		logger.Printf("Container created: %s\n", container.Identifier)
	}

	// Save metadata immediately after creation so cleanup can find the container
	// even if the process crashes before WaitContainerReady completes
	meta := metadata.Job{
		JobID:       jobID,
		Provider:    container.Provider,
		ContainerID: container.Identifier,
		Region:      container.Region,
		Extra:       container.Extra,
	}
	if err := store.Save(jobID, meta); err != nil {
		return fmt.Errorf("failed to save metadata: %w", err)
	}
	logger.Printf("Metadata saved for job: %s\n", jobID)

	if err := provider.WaitContainerReady(ctx, container); err != nil {
		return fmt.Errorf("failed to wait for container: %w", err)
	}

	logger.Printf("Container is ready: %s\n", container.Identifier)

	// Create symlink so GitLab Runner's cache/artifact scripts can find the binary.
	// Custom executor scripts look for "gitlab-runner" but the helper image ships "gitlab-runner-helper".
	helperContainer := container
	if helperContainer.Extra == nil {
		helperContainer.Extra = make(map[string]string)
	}
	helperContainer.Extra["targetContainer"] = "helper"
	symlinkScript := []byte(`ln -sf /usr/bin/gitlab-runner-helper /usr/local/bin/gitlab-runner 2>/dev/null; true`)
	if _, err := provider.ExecCommand(ctx, helperContainer, symlinkScript, io.Discard); err != nil {
		logger.Debug("failed to create gitlab-runner symlink in helper container", "error", err)
	}

	return nil
}

// RunStage implements the "run" stage logic
func RunStage(ctx context.Context, cfg *config.Config, scriptPath, stageName string) error {
	// Check job ID first before doing any real work
	jobID, err := GetJobID()
	if err != nil {
		return fmt.Errorf("failed to get job ID: %w", err)
	}

	script, err := os.ReadFile(scriptPath) //nolint:gosec // G304 - path from GitLab Runner
	if err != nil {
		return fmt.Errorf("failed to read script: %w", err)
	}
	store, err := metadata.NewFileStore(cfg.JobStore)
	if err != nil {
		return fmt.Errorf("failed to create metadata store: %w", err)
	}
	defer func() { _ = store.Close() }()

	meta, err := store.Load(jobID)
	if err != nil {
		return fmt.Errorf("failed to load metadata: %w", err)
	}

	provider, err := createProvider(cfg)
	if err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
	}

	container := providers.JobContainer{
		Provider:   meta.Provider,
		Identifier: meta.ContainerID,
		Region:     meta.Region,
		Extra:      meta.Extra,
	}

	// Route stage to the correct container (helper or build)
	if container.Extra == nil {
		container.Extra = make(map[string]string)
	}
	target := targetContainerForStage(stageName)
	container.Extra["targetContainer"] = target

	logger.Printf("Executing stage '%s' in container %s (target: %s)\n", stageName, container.Identifier, target)

	exitCode, err := provider.ExecCommand(ctx, container, script, os.Stdout)
	if err != nil {
		return fmt.Errorf("failed to execute command: %w", err)
	}

	if exitCode != 0 {
		_ = store.Close()
		os.Exit(exitCode) //nolint:gocritic // store.Close() called manually above
	}

	return nil
}

// CleanupStage implements the "cleanup" stage logic
func CleanupStage(ctx context.Context, cfg *config.Config) error {
	jobID, err := GetJobID()
	if err != nil {
		return fmt.Errorf("failed to get job ID: %w", err)
	}
	store, err := metadata.NewFileStore(cfg.JobStore)
	if err != nil {
		return fmt.Errorf("failed to create metadata store: %w", err)
	}
	defer func() { _ = store.Close() }()

	meta, err := store.Load(jobID)
	if err != nil {
		logger.Println("No metadata found, nothing to clean up")
		return nil
	}

	provider, err := createProvider(cfg)
	if err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
	}

	container := providers.JobContainer{
		Provider:   meta.Provider,
		Identifier: meta.ContainerID,
		Region:     meta.Region,
		Extra:      meta.Extra,
	}

	logger.Printf("Destroying container: %s\n", container.Identifier)

	if err := provider.DestroyContainer(ctx, container); err != nil {
		logger.Errorf("Failed to destroy container %s: %v\n", container.Identifier, err)
	}

	if err := store.Delete(jobID); err != nil {
		logger.Errorf("Failed to delete metadata for job %s: %v\n", jobID, err)
	}

	logger.Println("Cleanup completed")
	return nil
}

// getImageFromEnv gets the container image, checking env vars first
func getImageFromEnv(defaultImage string) string {
	if img := os.Getenv("CUSTOM_ENV_CI_JOB_IMAGE"); img != "" {
		return img
	}
	if img := os.Getenv("CI_JOB_IMAGE"); img != "" {
		return img
	}
	return defaultImage
}

// getBuildCPUFromEnv gets the build container CPU value, checking env vars first (supports CUSTOM_ENV_CI_JOB_BUILD_CPU from GitLab CI)
func getBuildCPUFromEnv(defaultCPU int) int {
	if cpu := os.Getenv("CUSTOM_ENV_CI_JOB_BUILD_CPU"); cpu != "" {
		if v, err := strconv.Atoi(cpu); err == nil {
			return v
		}
	}
	if cpu := os.Getenv("CI_JOB_BUILD_CPU"); cpu != "" {
		if v, err := strconv.Atoi(cpu); err == nil {
			return v
		}
	}
	return defaultCPU
}

// getBuildMemoryFromEnv gets the build container memory value, checking env vars first (supports CUSTOM_ENV_CI_JOB_BUILD_MEMORY from GitLab CI)
func getBuildMemoryFromEnv(defaultMemory int) int {
	if mem := os.Getenv("CUSTOM_ENV_CI_JOB_BUILD_MEMORY"); mem != "" {
		if v, err := strconv.Atoi(mem); err == nil {
			return v
		}
	}
	if mem := os.Getenv("CI_JOB_BUILD_MEMORY"); mem != "" {
		if v, err := strconv.Atoi(mem); err == nil {
			return v
		}
	}
	return defaultMemory
}

// getNoSpotFromEnv checks env vars to determine if on-demand instances should be used (no_spot=true means on-demand)
func getNoSpotFromEnv(defaultNoSpot bool) bool {
	if v := os.Getenv("CUSTOM_ENV_CI_JOB_NO_SPOT"); v != "" {
		return v == "true" || v == "1"
	}
	if v := os.Getenv("CI_JOB_NO_SPOT"); v != "" {
		return v == "true" || v == "1"
	}
	return defaultNoSpot
}

// getHelperCPUFromEnv gets the helper container CPU value from env vars
func getHelperCPUFromEnv(defaultCPU int) int {
	if cpu := os.Getenv("CUSTOM_ENV_CI_JOB_HELPER_CPU"); cpu != "" {
		if v, err := strconv.Atoi(cpu); err == nil {
			return v
		}
	}
	if cpu := os.Getenv("CI_JOB_HELPER_CPU"); cpu != "" {
		if v, err := strconv.Atoi(cpu); err == nil {
			return v
		}
	}
	return defaultCPU
}

// getHelperMemoryFromEnv gets the helper container memory value from env vars
func getHelperMemoryFromEnv(defaultMemory int) int {
	if mem := os.Getenv("CUSTOM_ENV_CI_JOB_HELPER_MEMORY"); mem != "" {
		if v, err := strconv.Atoi(mem); err == nil {
			return v
		}
	}
	if mem := os.Getenv("CI_JOB_HELPER_MEMORY"); mem != "" {
		if v, err := strconv.Atoi(mem); err == nil {
			return v
		}
	}
	return defaultMemory
}

// createProvider creates a provider instance based on configuration
func createProvider(cfg *config.Config) (providers.Provider, error) {
	return providers.Create(cfg.Provider, cfg)
}

// buildContainerEnvVars merges config env_vars with CUSTOM_ENV_* CI variables
// from GitLab Runner into a single map for the container.
func buildContainerEnvVars(configEnvVars map[string]string) map[string]string {
	envVars := make(map[string]string)

	// 1. Start with env vars from config.toml [env_vars]
	for k, v := range configEnvVars {
		envVars[k] = v
	}

	// 2. Forward CUSTOM_ENV_* variables from GitLab Runner
	// GitLab Runner passes all CI variables with CUSTOM_ENV_ prefix to the custom executor.
	// Strip the prefix and inject them as container environment variables.
	for _, env := range os.Environ() {
		key, value, ok := strings.Cut(env, "=")
		if !ok {
			continue
		}
		if strings.HasPrefix(key, "CUSTOM_ENV_") {
			ciKey := strings.TrimPrefix(key, "CUSTOM_ENV_")
			envVars[ciKey] = value
		}
	}

	return envVars
}

// defaultJobTimeoutMinutes is the default container keep-alive duration.
// 60 minutes is reasonable for CI jobs — if something goes wrong, resources are released automatically.
const defaultJobTimeoutMinutes = 60

// buildKeepAliveCommand returns the sleep command for keeping the container alive.
// Uses configured job_timeout (in minutes), falling back to 60 minutes.
func buildKeepAliveCommand(cfg *config.Config) []string {
	minutes := cfg.JobTimeout
	if minutes <= 0 {
		minutes = defaultJobTimeoutMinutes
	}
	seconds := minutes * 60
	return []string{"/bin/sh", "-c", fmt.Sprintf("sleep %d", seconds)}
}

// convertRegistryCredentials converts config credentials to provider credentials
func convertRegistryCredentials(creds []config.ImageRegistryCredential) []providers.ImageRegistryCredential {
	if len(creds) == 0 {
		return nil
	}
	result := make([]providers.ImageRegistryCredential, len(creds))
	for i, c := range creds {
		result[i] = providers.ImageRegistryCredential{
			Server:               c.Server,
			Username:             c.Username,
			Password:             c.Password,
			CredentialsParameter: c.CredentialsParameter,
		}
	}
	return result
}

// Ensure stdout/stderr are properly closed
var _ io.Writer = os.Stdout
