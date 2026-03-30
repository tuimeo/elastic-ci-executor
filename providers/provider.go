package providers

import (
	"context"
	"fmt"
	"io"
)

// Provider defines the interface for cloud container providers
type Provider interface {
	// CreateContainer creates and starts a new container
	CreateContainer(ctx context.Context, settings JobContainerSettings) (JobContainer, error)

	// WaitContainerReady waits until the container is ready to accept commands
	WaitContainerReady(ctx context.Context, container JobContainer) error

	// ExecCommand executes a command in the running container.
	// Cloud exec APIs return a merged TTY stream (stdout+stderr combined),
	// so all remote output is written to stdout. Returns the exit code and any error.
	ExecCommand(ctx context.Context, container JobContainer, script []byte, stdout io.Writer) (exitCode int, err error)

	// DestroyContainer stops and removes the container
	DestroyContainer(ctx context.Context, container JobContainer) error
}

// PermissionChecker is an optional interface that providers can implement
// to verify that the configured credentials have sufficient permissions.
// Each check uses lightweight read-only API calls without creating resources.
type PermissionChecker interface {
	CheckPermissions(ctx context.Context) []PermissionCheckResult
}

// PermissionCheckResult represents the result of a single permission check
type PermissionCheckResult struct {
	Action  string // e.g. "eci:DescribeContainerGroups"
	Passed  bool
	Message string // error detail on failure
}

// ImageRegistryCredential contains authentication info for pulling images from private registries.
// Two forms are supported:
//   - Username/Password: Server + Username + Password (Aliyun ECI, Azure ACI)
//   - Credentials Parameter: Server + CredentialsParameter (AWS Secrets Manager ARN)
type ImageRegistryCredential struct {
	Server               string
	Username             string
	Password             string
	CredentialsParameter string // AWS Secrets Manager ARN (alternative to Username/Password)
}

// JobContainerSettings contains the configuration for creating a container to run GitLab CI jobs
type JobContainerSettings struct {
	Image       string
	BuildCPU    int // vCPU cores
	BuildMemory int // GiB
	EnvVars map[string]string
	// Command is the keep-alive command for the container (required, set by executor stages)
	Command []string
	// NoSpot indicates whether to use on-demand instances instead of spot/preemptible (default: false = use spot)
	NoSpot bool
	// ImageProxy is the proxy host for rewriting image URLs (crproxy-style path routing).
	// Providers should apply this to any additional images they resolve (e.g., helper images).
	ImageProxy string
	// ImageRegistryCredentials contains authentication credentials for private image registries.
	// Multiple credentials are supported for pulling images from different registries.
	ImageRegistryCredentials []ImageRegistryCredential
	// HelperImage overrides the gitlab-runner-helper image for infrastructure stages (git clone, cache, artifacts).
	HelperImage  string
	HelperCPU    int // Helper container CPU in vCPU cores
	HelperMemory int // Helper container memory in GiB
}

// Validate checks that all required fields are set.
func (s JobContainerSettings) Validate() error {
	if s.Image == "" {
		return fmt.Errorf("container image is required but not set")
	}
	if s.BuildCPU <= 0 {
		return fmt.Errorf("container CPU must be a positive integer")
	}
	if s.BuildMemory <= 0 {
		return fmt.Errorf("container memory must be a positive integer")
	}
	if len(s.Command) == 0 {
		return fmt.Errorf("container command is required but not set")
	}
	return nil
}

// JobContainer contains information about a created container instance
type JobContainer struct {
	Provider   string            // Provider name: "aws-fargate", "aliyun-eci", "azure-aci"
	Identifier string            // Container identifier (ARN, ID, Name, etc.)
	Region     string            // Cloud region
	Extra      map[string]string // Provider-specific metadata
}

// ExecResult contains the result of command execution
type ExecResult struct {
	ExitCode int
	Error    error
}
