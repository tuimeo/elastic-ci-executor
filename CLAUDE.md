# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Elastic CI Executor is a universal GitLab Runner Custom Executor that supports multiple cloud container platforms (AWS Fargate, Aliyun ECI, Azure ACI). It implements GitLab's Custom Executor protocol to run CI/CD jobs on cloud container services without requiring SSH.

## Build Commands

```bash
# Build for current platform
make build

# Build for all platforms (Linux/macOS/Windows, amd64/arm64)
make build-all

# Run tests
make test

# Run tests with coverage
make test-coverage

# Format code
make fmt

# Run linter (requires golangci-lint)
make lint

# Download dependencies
make deps
```

## Testing the Executor

```bash
# Build the binary
make build

# Test individual commands (requires a valid config.toml)
./elastic-ci-executor --config config.toml execute config
./elastic-ci-executor --config config.toml execute prepare
./elastic-ci-executor --config config.toml execute run <script_path> <stage_name>
./elastic-ci-executor --config config.toml execute cleanup

# Management commands
./elastic-ci-executor --config config.toml mgmt list
./elastic-ci-executor --config config.toml mgmt cleanup-stale --max-age 24h --dry-run
./elastic-ci-executor --version
```

## Architecture Overview

### Provider Interface Pattern

The core abstraction is the `Provider` interface in [providers/provider.go](providers/provider.go):

```go
type Provider interface {
    CreateContainer(ctx context.Context, settings JobContainerSettings) (JobContainer, error)
    WaitContainerReady(ctx context.Context, container JobContainer) error
    ExecCommand(ctx context.Context, container JobContainer, script []byte, stdout, stderr io.Writer) (exitCode int, err error)
    DestroyContainer(ctx context.Context, container JobContainer) error
}
```

All cloud providers implement this interface:
- [providers/aws/fargate.go](providers/aws/fargate.go) - AWS Fargate implementation
- [providers/aliyun/eci.go](providers/aliyun/eci.go) - Aliyun ECI implementation
- [providers/azure/aci.go](providers/azure/aci.go) - Azure ACI implementation

### GitLab Custom Executor Lifecycle

The executor implements four stages defined in [cmd/executor/execute.go](cmd/executor/execute.go):

1. **config** - Returns executor metadata to GitLab Runner
2. **prepare** - Creates container with `sleep <job_timeout>`, saves metadata to SQLite
3. **run** - Executes scripts via cloud provider exec APIs (called multiple times)
4. **cleanup** - Destroys container and removes metadata

### Metadata Persistence

Since GitLab Runner spawns separate processes for each stage, task metadata is persisted to SQLite in [internal/metadata/sqlite_store.go](internal/metadata/sqlite_store.go). The job ID (from `CUSTOM_ENV_CI_JOB_ID` or `CI_JOB_ID`) is used as the key.

### Command Execution Strategy

Instead of SSH, each provider uses its cloud platform's native exec API via SDK + WebSocket:
- AWS: ECS `ExecuteCommand` API + SSM Session Manager WebSocket protocol
- Aliyun: ECI `ExecContainerCommand` API + WebSocket streaming
- Azure: ACI `ExecuteCommand` API + WebSocket streaming

Exec implementations are in separate files:
- [providers/aws/exec.go](providers/aws/exec.go)
- [providers/aliyun/exec.go](providers/aliyun/exec.go)
- [providers/azure/exec.go](providers/azure/exec.go)

## Adding a New Cloud Provider

1. Create a new directory under `providers/<provider-name>/`
2. Implement the `Provider` interface
3. Add provider-specific configuration to [config/config.go](config/config.go)
4. Register the provider via `init()` using `providers.Register()` (see existing providers for examples)
5. Update [config.toml.example](config.toml.example) with example configuration

## Configuration Structure

Configuration is defined in [config/config.go](config/config.go) with TOML format:

- `provider` - Which cloud platform to use ("aws-fargate", "aliyun-eci", "azure-aci")
- `jobstore` - Path to jobstore directory for metadata storage (default: "./jobstore")
- `image` - Default container image (can be overridden by CI_JOB_IMAGE)
- `build_cpu` - Build container CPU in vCPU cores, integer (default: 2, can be overridden by CI_JOB_BUILD_CPU)
- `build_memory` - Build container memory in GiB, integer (default: 4, can be overridden by CI_JOB_BUILD_MEMORY)
- `image_proxy` - Docker image proxy host for rewriting container images (crproxy-style path routing)
- `image_registry_credentials` - Array of registry credentials. Two forms: {server, username, password} for direct auth, or {server, credentials_parameter} for AWS Secrets Manager ARN
- `helper_image` - Override the gitlab-runner-helper image for infrastructure stages
- `helper_cpu` - Helper container CPU in vCPU cores, integer (default: 2, can be overridden by CI_JOB_HELPER_CPU)
- `helper_memory` - Helper container memory in GiB, integer (default: 4, can be overridden by CI_JOB_HELPER_MEMORY)
- `no_spot` - Use on-demand instances instead of spot/preemptible (default: false)
- `job_timeout` - Max container lifetime in minutes (default: 60)
- `env_vars` - Environment variables for all jobs (TOML table)
- `[aws-fargate]`, `[aliyun-eci]`, `[azure-aci]` - Provider-specific settings

## Container Lifecycle

All containers are started with: `/bin/sh -c "sleep <seconds>"` where seconds is derived from `job_timeout` (default: 60 minutes = 3600 seconds).

This keeps the container running until the timeout expires or the cleanup stage stops it. Scripts are then executed via the cloud provider's exec API.

## Error Handling and Stale Tasks

If GitLab Runner crashes during a job, containers may not be cleaned up. The [cmd/executor/mgmt.go](cmd/executor/mgmt.go) command provides utilities to:
- List all tracked tasks
- Clean up tasks older than a specified age
- Support dry-run mode for safety

## Key Implementation Details

- AWS Fargate: Dynamically registers task definitions using `TaskDefinitionFamily` from config
- All providers: Container info is stored in `JobContainer.Extra` map for provider-specific metadata
- Metadata types defined in [internal/metadata/types.go](internal/metadata/types.go)
- Version info: Build-time version injection in [internal/version/version.go](internal/version/version.go)

## Development Notes

- Go version: 1.25.0
- Uses `urfave/cli/v3` for CLI framework
- Configuration parsing: `BurntSushi/toml`
- AWS SDK: `aws-sdk-go-v2`
- All providers use cloud SDK APIs + WebSocket for command execution, no external CLI tools required
