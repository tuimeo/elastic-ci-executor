# Elastic CI Executor

A universal GitLab Runner Custom Executor that supports multiple cloud container platforms.

## Supported Providers

- **AWS Fargate** - Run CI jobs on AWS ECS Fargate
- **Aliyun ECI** - Run CI jobs on Alibaba Cloud Elastic Container Instance
- **Azure ACI** - Run CI jobs on Azure Container Instances

## Features

- ✅ Use any standard Docker image (node:18, python:3.11, etc.)
- ✅ No SSH required - uses native cloud provider exec APIs
- ✅ Automatic task definition management
- ✅ Stale task cleanup utility
- ✅ File-based metadata storage

## Architecture

The executor follows GitLab Runner's Custom Executor protocol:

1. **config** - Returns executor information
2. **prepare** - Creates and starts a container (with `sleep <job_timeout_seconds>`)
3. **run** - Executes scripts in the running container (called multiple times)
4. **cleanup** - Destroys the container and cleans up metadata

```
GitLab Runner
    │
    ├─> config  ─────> Returns executor info
    │
    ├─> prepare ─────> Creates container (sleep <job_timeout_seconds>)
    │                  Saves metadata to file store
    │
    ├─> run ─────────> Executes script via cloud exec API
    │   (called multiple times for different stages)
    │
    └─> cleanup ─────> Destroys container
                       Deletes metadata from file store
```

## Quick Start

### Installation

```bash
# Download the binary
curl -LO https://github.com/tuimeo/elastic-ci-executor/releases/latest/download/elastic-ci-executor-linux-amd64
chmod +x elastic-ci-executor-linux-amd64
sudo mv elastic-ci-executor-linux-amd64 /usr/local/bin/elastic-ci-executor

# Or build from source
git clone https://github.com/tuimeo/elastic-ci-executor.git
cd elastic-ci-executor
go build -o elastic-ci-executor ./cmd/executor
```

### Configuration

1. Create a configuration file:

```bash
cp config.toml.example /etc/elastic-ci-executor/config.toml
```

2. Edit the configuration for your cloud provider (e.g. AWS Fargate):

```toml
provider = "aws-fargate"

[aws-fargate]
region = "us-east-1"
cluster = "my-ecs-cluster"
subnets = ["subnet-xxx"]
security_groups = ["sg-xxx"]
task_definition_family = "elastic-ci-executor"
execution_role_arn = "arn:aws:iam::123456789012:role/ecsTaskExecutionRole"
```

3. Configure GitLab Runner (`/etc/gitlab-runner/config.toml`):

```toml
[[runners]]
  name = "elastic-ci-runner"
  url = "https://gitlab.com/"
  token = "YOUR_RUNNER_TOKEN"
  executor = "custom"
  builds_dir = "/builds"
  cache_dir = "/cache"

  [runners.custom]
    config_exec = "/usr/local/bin/elastic-ci-executor"
    config_args = ["execute", "config"]

    prepare_exec = "/usr/local/bin/elastic-ci-executor"
    prepare_args = ["--config", "/etc/elastic-ci-executor/config.toml", "execute", "prepare"]

    run_exec = "/usr/local/bin/elastic-ci-executor"
    run_args = ["--config", "/etc/elastic-ci-executor/config.toml", "execute", "run"]

    cleanup_exec = "/usr/local/bin/elastic-ci-executor"
    cleanup_args = ["--config", "/etc/elastic-ci-executor/config.toml", "execute", "cleanup"]
```

For detailed usage, per-job configuration, CLI reference, provider setup, and image proxy configuration, see [USAGE.md](USAGE.md).

## Development

```bash
# Clone repository
git clone https://github.com/tuimeo/elastic-ci-executor.git
cd elastic-ci-executor

# Install dependencies
go mod download

# Build
go build -o elastic-ci-executor ./cmd/executor

# Run tests
go test ./...
```

## License

Apache License 2.0 - see [LICENSE](LICENSE) for details.

## Contributing

Contributions welcome! Please open an issue or PR.
