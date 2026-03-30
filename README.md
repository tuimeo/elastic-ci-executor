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
2. **prepare** - Creates and starts a container (with `sleep <job_timeout>`)
3. **run** - Executes scripts in the running container (called multiple times)
4. **cleanup** - Destroys the container and cleans up metadata

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
cp config.toml.example /etc/gitlab-runner/elastic-ci-executor/config.toml
```

2. Edit the configuration for your cloud provider:

**AWS Fargate Example:**

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

### GitLab Runner Configuration

Add to your GitLab Runner config (`/etc/gitlab-runner/config.toml`):

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
    prepare_args = ["--config", "/etc/gitlab-runner/elastic-ci-executor/config.toml", "execute", "prepare"]

    run_exec = "/usr/local/bin/elastic-ci-executor"
    run_args = ["--config", "/etc/gitlab-runner/elastic-ci-executor/config.toml", "execute", "run"]

    cleanup_exec = "/usr/local/bin/elastic-ci-executor"
    cleanup_args = ["--config", "/etc/gitlab-runner/elastic-ci-executor/config.toml", "execute", "cleanup"]
```

## Per-Job Configuration in .gitlab-ci.yml

You can customize the container image and resources for each job in your `.gitlab-ci.yml`:

```yaml
# Use a specific image for this job
build:
  image: node:18
  variables:
    CI_JOB_BUILD_CPU: "2"   # 2 vCPU cores
    CI_JOB_BUILD_MEMORY: "4" # 4 GB
  script:
    - npm ci
    - npm run build

# Different resources for different jobs
test:
  image: python:3.11
  variables:
    CI_JOB_BUILD_CPU: "1"   # 1 vCPU
    CI_JOB_BUILD_MEMORY: "2" # 2 GB
  script:
    - pytest

# Small lightweight job
lint:
  image: alpine:latest
  variables:
    CI_JOB_BUILD_CPU: "1"   # 1 vCPU
    CI_JOB_BUILD_MEMORY: "2" # 2 GB
  script:
    - echo "Linting..."
```

**Note:** If not specified in `.gitlab-ci.yml`, top-level values from `config.toml` are used. If not configured there, defaults are 2 vCPU / 4 GB memory.

### Cloud Provider Resource Limits

Each cloud provider has specific valid CPU/memory combinations:

**AWS Fargate:**
| CPU (vCPU) | Valid Memory (GiB) |
|------------|-------------------|
| 0.25 | 0.5, 1, 2 |
| 0.5 | 1, 2, 3, 4 |
| 1 | 2 - 8 |
| 2 | 4 - 16 |
| 4 | 8 - 30 |

Invalid combinations will result in error: *"No Fargate configuration exists for given values"*

**Azure ACI:**
- CPU: 0.25 - 4.0 cores (per container)
- Memory: 0.5 - 16 GB (per container)
- Maximum per container group: 4 CPU cores and 16 GB memory

**Aliyun ECI:**
- Minimum: 0.25 vCPU / 0.5 GB memory
- Maximum: 4 vCPU / 16 GB memory
- Specific CPU/memory combinations apply

## Commands

```bash
# Custom executor commands (called by GitLab Runner)
elastic-ci-executor execute config
elastic-ci-executor execute prepare
elastic-ci-executor execute run <script_path> <stage_name>
elastic-ci-executor execute cleanup

# Management commands
elastic-ci-executor mgmt list
elastic-ci-executor mgmt cleanup-stale --max-age 24h
elastic-ci-executor mgmt cleanup-stale --max-age 2h --dry-run

# Provider commands
elastic-ci-executor provider list
elastic-ci-executor provider check aliyun-eci

# Version
elastic-ci-executor --version
```

## Provider-Specific Setup

### AWS Fargate

**IAM Permissions Required:**

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ecs:RegisterTaskDefinition",
        "ecs:RunTask",
        "ecs:DescribeTasks",
        "ecs:StopTask",
        "ecs:ExecuteCommand"
      ],
      "Resource": "*"
    },
    {
      "Effect": "Allow",
      "Action": [
        "iam:PassRole"
      ],
      "Resource": "arn:aws:iam::*:role/ecsTaskExecutionRole"
    }
  ]
}
```

**Network Requirements:**
- Containers need internet access (for `npm install`, `apt-get`, etc.)
- Configure NAT Gateway if using private subnets
- Security groups must allow outbound traffic

### Aliyun ECI

**Credentials:**
- Use AccessKey/AccessSecret in config
- Or use environment variables: `ALIYUN_ACCESS_KEY_ID`, `ALIYUN_ACCESS_KEY_SECRET`
- Or use instance RAM role

### Azure ACI

**Authentication:**
- Use Azure CLI authentication: `az login`
- Or use Service Principal
- Or use Managed Identity

## Stale Task Cleanup

If GitLab Runner crashes, containers may not be cleaned up. Use the cleanup utility:

```bash
# List all containers tracked in the database
elastic-ci-executor --config config.toml mgmt list

# Dry run to see what would be cleaned
elastic-ci-executor --config config.toml mgmt cleanup-stale --max-age 24h --dry-run

# Actually clean up containers older than 24 hours
elastic-ci-executor --config config.toml mgmt cleanup-stale --max-age 24h
```

Add to cron for automatic cleanup:

```bash
# /etc/cron.daily/elastic-ci-cleanup
#!/bin/bash
/usr/local/bin/elastic-ci-executor \
  --config /etc/gitlab-runner/elastic-ci-executor/config.toml \
  mgmt cleanup-stale --max-age 24h
```

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

## Architecture Diagram

```
GitLab Runner
    │
    ├─> config  ─────> Returns executor info
    │
    ├─> prepare ─────> Creates container (sleep <job_timeout>)
    │                  Saves metadata to file store
    │
    ├─> run ─────────> Executes script via cloud exec API
    │   (called multiple times for different stages)
    │
    └─> cleanup ─────> Destroys container
                       Deletes metadata from file store
```

## License

MIT

## Contributing

Contributions welcome! Please open an issue or PR.
