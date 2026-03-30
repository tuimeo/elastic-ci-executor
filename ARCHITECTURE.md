# Elastic CI Executor - Architecture Documentation

## Overview

Elastic CI Executor is a universal GitLab Runner Custom Executor that supports running CI/CD tasks on multiple cloud container platforms.

## Design Principles

1. **Cloud-Agnostic** - Unified interface abstraction supporting multiple cloud platforms
2. **Standard Images** - No image modification required, supports any Docker image
3. **Pure SDK** - Uses cloud SDKs + WebSocket for command execution, no external CLI dependencies
4. **Easy to Extend** - Provider registry pattern for adding new cloud platforms

## Core Architecture

```
┌─────────────────────────────────────────────────────────┐
│                   GitLab Runner                          │
└────────────────────┬────────────────────────────────────┘
                     │
                     ├─> config  (returns executor info)
                     ├─> prepare (creates container)
                     ├─> run     (executes scripts, multiple calls)
                     └─> cleanup (destroys container)
                     │
┌────────────────────┴────────────────────────────────────┐
│              Elastic CI Executor                         │
│                                                          │
│  ┌──────────────────────────────────────────────┐      │
│  │         Provider Interface                    │      │
│  │  - CreateContainer()                          │      │
│  │  - WaitContainerReady()                       │      │
│  │  - ExecCommand()                              │      │
│  │  - DestroyContainer()                         │      │
│  └──────────┬──────────────┬──────────┬─────────┘      │
│             │              │          │                 │
│      ┌──────▼──────┐ ┌─────▼─────┐ ┌─▼────────┐       │
│      │ AWS Fargate │ │Aliyun ECI │ │Azure ACI │       │
│      └─────────────┘ └───────────┘ └──────────┘       │
└─────────────────────────────────────────────────────────┘
                     │
         ┌───────────┴──────────────┐
         │                          │
    ┌────▼─────┐             ┌──────▼──────┐
    │ ECS Task │             │ ECI Instance│
    │  (sleep) │             │   (sleep)   │
    └──────────┘             └─────────────┘
```

## Dual-Container Model

Each CI job creates **two containers** that share a `/builds` volume:

- **Build container** - Runs the user's CI scripts (build_script, step_script, after_script). Uses the job image specified in `.gitlab-ci.yml` or the default `image` from config. Resources are configured via `build_cpu` / `build_memory` (or `CI_JOB_CPU` / `CI_JOB_MEMORY` env vars).
- **Helper container** - Runs the GitLab Runner helper binary for infrastructure stages: `get_sources` (git clone), `restore_cache`, `download_artifacts`, `archive_cache`, `upload_artifacts`, etc. Uses the `helper_image` from config. Resources are configured via `helper_cpu` / `helper_memory` (or `CI_JOB_HELPER_CPU` / `CI_JOB_HELPER_MEMORY` env vars).

The executor routes each stage to the appropriate container based on its name. Infrastructure stages are sent to the helper container; user script stages are sent to the build container.

## Workflow

### 1. Prepare Stage

```
1. Read configuration file
2. Create Provider instance via registry
3. Call CreateContainer() twice - one build container and one helper container
4. Both containers start with "sleep <job_timeout>" and share a /builds volume
5. Call WaitContainerReady() for each container
6. Create gitlab-runner symlink in helper container
7. Save metadata (ContainerID, Provider, etc.) to file-based JSON store
```

### 2. Run Stage (called multiple times)

```
Called once for each GitLab CI stage:
1. Read ContainerID from file-based JSON metadata
2. Route stage to the correct container (helper or build)
3. Read script content
4. Call ExecCommand() - execute script via cloud SDK + WebSocket
5. Stream stdout output
6. Return exit code
```

### 3. Cleanup Stage

```
1. Read ContainerID from metadata
2. Call DestroyContainer() for both containers
3. Delete metadata record
```

## Directory Structure

```
elastic-ci-executor/
├── cmd/executor/              # CLI entry point
│   ├── main.go                # Main program, global --config flag
│   ├── execute.go             # execute config/prepare/run/cleanup stages
│   ├── mgmt.go                # mgmt list/cleanup-stale commands
│   └── provider.go            # provider list/check commands
│
├── providers/                 # Cloud platform abstraction layer
│   ├── provider.go            # Provider interface definition
│   ├── registry.go            # Provider registration system
│   ├── aws/                   # AWS Fargate implementation
│   │   ├── fargate.go         # Main logic (SDK-based)
│   │   ├── config.go          # AWS-specific config struct
│   │   └── exec.go            # Command execution (SSM WebSocket)
│   ├── aliyun/                # Aliyun ECI implementation
│   │   ├── eci.go             # Main logic (SDK-based)
│   │   ├── config.go          # Aliyun-specific config struct
│   │   └── exec.go            # Command execution (WebSocket)
│   └── azure/                 # Azure ACI implementation
│       ├── aci.go             # Main logic (SDK-based)
│       ├── config.go          # Azure-specific config struct
│       └── exec.go            # Command execution (WebSocket)
│
├── internal/
│   ├── executor/              # Executor business logic
│   │   ├── stages.go          # prepare/run/cleanup stage implementations
│   │   ├── mgmt.go            # list/cleanup-stale implementations
│   │   └── provider.go        # provider registration helpers
│   └── metadata/              # Job metadata management
│       ├── types.go           # Job struct
│       ├── store.go           # Store interface definition
│       └── file_store.go      # File-based JSON storage implementation
│
├── config/                    # Configuration management
│   └── config.go              # Configuration parsing and validation
│
├── README.md                  # User documentation
├── ARCHITECTURE.md            # Architecture documentation (this file)
├── config.toml.example        # Configuration example
└── go.mod                     # Go module
```

## Provider Interface

```go
type Provider interface {
    // CreateContainer creates and starts a new container
    CreateContainer(ctx context.Context, settings JobContainerSettings) (JobContainer, error)

    // WaitContainerReady waits until the container is ready to accept commands
    WaitContainerReady(ctx context.Context, container JobContainer) error

    // ExecCommand executes a command in the running container
    ExecCommand(ctx context.Context, container JobContainer, script []byte,
                stdout io.Writer) (exitCode int, err error)

    // DestroyContainer stops and removes the container
    DestroyContainer(ctx context.Context, container JobContainer) error
}
```

## Metadata Design

Metadata is stored as file-based JSON (under `./jobstore` directory by default), one file per job:

```json
{
  "job_id": "123456",
  "provider": "aws-fargate",
  "container_id": "arn:aws:ecs:us-east-1:123456789:task/...",
  "created_at": "2025-01-07T10:00:00Z",
  "extra": {
    "cluster": "my-cluster"
  }
}
```

**Why file-based JSON?**
- GitLab Runner spawns a new process for each stage
- Processes cannot share memory
- File-based JSON provides simple, reliable persistence with no external dependencies

## Command Execution Strategy

All providers use cloud SDK APIs + WebSocket for command execution. No external CLI tools are required.

| Provider | SDK API | Transport | Protocol |
|----------|---------|-----------|----------|
| AWS Fargate | ECS `ExecuteCommand` | SSM Session Manager | Binary WebSocket (SSM Agent protocol) |
| Aliyun ECI | ECI `ExecContainerCommand` | WebSocket | Text/Base64 WebSocket |
| Azure ACI | ACI `ExecuteCommand` | WebSocket | Text WebSocket (password auth) |

### Exit Code Capture

Since WebSocket streaming doesn't provide structured exit codes, all providers use a sentinel-based approach:

1. Wrap the user script: `/bin/sh -c '<script>'; echo "__SENTINEL__$?"`
2. Parse the sentinel from WebSocket output to extract the real exit code
3. Forward all output before the sentinel to stdout

## Container Keep-Alive Strategy

All containers use a timeout-based sleep command:

```bash
/bin/sh -c "sleep <seconds>"
```

- Seconds is derived from `job_timeout` config (default: 60 minutes = 3600 seconds)
- Container exits automatically after the timeout, preventing resource leaks from missed cleanups
- Under normal operation, the cleanup stage stops the container before the timeout expires

## Task Definition Management

### AWS Fargate

**Dynamic Creation:**
1. Use `TaskDefinitionFamily` from config as family name
2. Register new Task Definition based on Image/CPU/Memory
3. May create new revision each time (e.g., `elastic-ci-executor:1`, `elastic-ci-executor:2`)

### Aliyun ECI

**Direct Creation:**
- No pre-definition needed, directly create Container Group via API

### Azure ACI

**Direct Creation:**
- Similar to ECI, directly create Container Group via ARM API

## Error Handling

### Container Creation Failure
- Immediately return error
- No metadata created
- User can retry

### Container Runtime Failure
- Run stage returns non-zero exit code
- GitLab Runner marks job as failed
- Cleanup stage still executes

### Cleanup Failure
- Container may remain
- Use `cleanup-stale` command to clean up

## Cleanup Strategy

### Normal Cleanup
```
prepare → run (multiple times) → cleanup ✓
```

### Exception Case
```
prepare → run → [Runner crashes] ✗
```

**Solution:**
```bash
# Periodically run cleanup tool
elastic-ci-executor mgmt cleanup-stale --max-age 24h
```

Can be placed in cron job:
```bash
# /etc/cron.daily/elastic-ci-cleanup
0 2 * * * /usr/local/bin/elastic-ci-executor --config /path/to/config.toml mgmt cleanup-stale --max-age 24h
```

## Extensibility

### Adding a New Cloud Platform

1. Implement Provider interface:
```go
// providers/newcloud/provider.go
type NewCloudProvider struct {}

func (p *NewCloudProvider) CreateContainer(...) (JobContainer, error) { }
func (p *NewCloudProvider) WaitContainerReady(...) error { }
func (p *NewCloudProvider) ExecCommand(...) (int, error) { }
func (p *NewCloudProvider) DestroyContainer(...) error { }
```

2. Add configuration:
```go
// providers/newcloud/config.go
type Config struct {
    // Cloud platform-specific configuration
}
```

3. Register the provider via `init()` (same pattern as existing providers):
```go
// providers/newcloud/provider.go
func init() {
    providers.Register(
        "newcloud",
        "NewCloud - description",
        func(cfg *globalconfig.Config) (providers.Provider, error) {
            return NewProvider(cfg)
        },
    )
}
```

Import the package in `cmd/executor/provider.go` to trigger registration:
```go
_ "github.com/tuimeo/elastic-ci-executor/providers/newcloud"
```

## Performance Considerations

### Container Startup Time
- Fargate: ~30-60 seconds
- ECI: ~20-40 seconds
- ACI: ~30-50 seconds

### Optimization Recommendations
1. Use smaller base images
2. Pre-pull common images to private registry
3. Configure CPU/Memory appropriately (avoid oversizing)

## Security Considerations

### Credential Management
- AWS: Use IAM Role or environment variables
- Aliyun: Use RAM Role or environment variables
- Azure: Use Managed Identity or Service Principal

### Network Isolation
- Recommend using private subnets + NAT Gateway
- Restrict Security Group / NSG rules
- Enable audit logging

### Principle of Least Privilege
- Grant only necessary IAM/RAM permissions
- Regularly rotate credentials
- Use temporary credentials

## Monitoring and Logging

### Recommended Metrics
- Container creation success rate
- Container startup time
- Job execution time
- Cleanup success rate
- Number of orphaned containers

### Log Locations
- Executor logs: Specified in config file (stdout or file)
- Container logs:
  - AWS: CloudWatch Logs
  - Aliyun: SLS (Simple Log Service)
  - Azure: Azure Monitor

## Troubleshooting

### Issue: Container Cannot Start
**Check:**
- Does the image exist
- Is network configuration correct
- Is CPU/Memory configuration appropriate
- Check error messages in cloud platform console

### Issue: Command Execution Fails
**Check:**
- Is container in running state
- Is ExecuteCommand enabled (AWS)
- Are cloud credentials configured correctly
- Check WebSocket connectivity

### Issue: Orphaned Containers
**Solution:**
```bash
# List all containers tracked in the database
elastic-ci-executor mgmt list

# Clean up stale containers
elastic-ci-executor mgmt cleanup-stale --max-age 24h
```

## References

- [GitLab Runner Custom Executor](https://docs.gitlab.com/runner/executors/custom.html)
- [AWS Fargate](https://aws.amazon.com/fargate/)
- [Aliyun ECI](https://help.aliyun.com/product/89129.html)
- [Azure Container Instances](https://azure.microsoft.com/en-us/services/container-instances/)
