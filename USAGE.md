# Usage Guide

## Quick Start

### 1. Install the Executor

```bash
make build
sudo cp bin/elastic-ci-executor /usr/local/bin/
```

### 2. Create a Config File

All configuration lives in a single `config.toml`. Create one based on your provider:

```toml
# Required: which provider to use
provider = "aliyun-eci"

# Optional: global settings
# log_level = "info"
# jobstore = "./jobstore"

# Optional: default container settings (top-level, not in a section)
# image = "alpine:latest"
# build_cpu = 2           # Build container CPU in vCPU cores (default: 2)
# build_memory = 4        # Build container memory in GiB (default: 4)

# Provider-specific settings (section name must match provider value above)
[aliyun-eci]
region = "cn-hangzhou"
vpc_id = "vpc-xxxxx"
vswitch_id = "vsw-xxxxx"
security_group_id = "sg-xxxxx"
access_key_id = "YOUR_KEY"
access_key_secret = "YOUR_SECRET"
```

See `examples/` for ready-to-use templates for each provider.

### 3. Config File Discovery

The executor searches for `config.toml` in this order:

1. Path given by `--config` flag or `ECIE_CONFIG` env var
2. `./config.toml` (current working directory)
3. `/etc/elastic-ci-executor/config.toml`
4. `~/.config/elastic-ci-executor/config.toml`
5. `<executable-dir>/config.toml` (portable mode — place `config.toml` next to the binary)

### 4. Configure GitLab Runner

In your GitLab Runner's `config.toml`, point each executor stage at the binary. Pass `--config` if the config file is not in an auto-discovered location:

```toml
concurrent = 3

[[runners]]
  name = "runner-aliyun"
  url = "https://gitlab.com"
  token = "YOUR_RUNNER_TOKEN"
  executor = "custom"
  [runners.custom]
    config_exec  = "elastic-ci-executor"
    config_args  = ["execute", "config"]
    prepare_exec = "elastic-ci-executor"
    prepare_args = ["--config", "/etc/elastic-ci-executor/config.toml", "execute", "prepare"]
    run_exec     = "elastic-ci-executor"
    run_args     = ["--config", "/etc/elastic-ci-executor/config.toml", "execute", "run"]
    cleanup_exec = "elastic-ci-executor"
    cleanup_args = ["--config", "/etc/elastic-ci-executor/config.toml", "execute", "cleanup"]
```

If you put `config.toml` next to the binary (e.g. `/usr/local/bin/config.toml`) or rely on another auto-discovered path, you can omit the `--config` flag entirely.

## Per-Job Configuration via .gitlab-ci.yml

You can override container settings per-job using GitLab CI variables:

```yaml
build:
  image: node:18                    # Sets CI_JOB_IMAGE
  variables:
    CI_JOB_BUILD_CPU: "2"           # Build container CPU in vCPU cores
    CI_JOB_BUILD_MEMORY: "4"        # Build container memory in GiB
  script:
    - npm ci && npm run build

small-job:
  image: alpine:latest
  variables:
    CI_JOB_BUILD_CPU: "1"            # 1 vCPU
    CI_JOB_BUILD_MEMORY: "2"        # 2 GiB
  script:
    - echo "Lightweight task"

on-demand-job:
  image: alpine:latest
  variables:
    CI_JOB_NO_SPOT: "true"          # Use on-demand instance instead of spot (Aliyun ECI, etc.)
  script:
    - echo "Using on-demand instance"
```

**Supported Variables:**

| Variable | Description | Default |
|----------|-------------|---------|
| `CI_JOB_IMAGE` | Container image to use | `alpine:latest` |
| `CI_JOB_BUILD_CPU` | Build container CPU in vCPU cores | `2` |
| `CI_JOB_BUILD_MEMORY` | Build container memory in GiB | `4` |
| `CI_JOB_HELPER_CPU` | Helper container CPU in vCPU cores | `2` |
| `CI_JOB_HELPER_MEMORY` | Helper container memory in GiB | `4` |
| `CI_JOB_NO_SPOT` | Use on-demand instead of spot/preemptible | `false` (use spot) |

**Priority:** `CI_JOB_*` > `config.toml` top-level settings > Built-in defaults

### Cloud Provider Resource Constraints

> ⚠️ **Important:** Each cloud provider enforces specific CPU/memory combinations.

**AWS Fargate** valid combinations (CPU in vCPU cores):
| CPU | Valid Memory (GiB) |
|-----|-------------------|
| 0.25 | 0.5, 1, 2 |
| 0.5 | 1, 2, 3, 4 |
| 1 | 2 - 8 |
| 2 | 4 - 16 |
| 4 | 8 - 30 |

Invalid values result in: *"No Fargate configuration exists for given values"*

**Azure ACI** and **Aliyun ECI** are more flexible but still have min/max limits. Check their documentation for details.

## CLI Reference

### Global Flags

| Flag | Short | Env Var | Default | Description |
|------|-------|---------|---------|-------------|
| `--config` | `-c` | `ECIE_CONFIG` | - | Path to config.toml (auto-discovers if omitted) |
| `--verbose` | `-v` | `ECIE_VERBOSE` | `false` | Enable debug logging (overrides log_level in config) |

### `execute` — GitLab Custom Executor Stages

These are called by GitLab Runner automatically.

```bash
elastic-ci-executor [--config PATH] execute config
elastic-ci-executor [--config PATH] execute prepare
elastic-ci-executor [--config PATH] execute run <script_path> <stage_name>
elastic-ci-executor [--config PATH] execute cleanup
```

### `mgmt` — Management Commands

```bash
# List all containers tracked in the database
elastic-ci-executor [--config PATH] mgmt list

# Dry-run: show stale containers older than 24h
elastic-ci-executor [--config PATH] mgmt cleanup-stale --max-age 24h --dry-run

# Actually delete stale containers
elastic-ci-executor [--config PATH] mgmt cleanup-stale --max-age 24h
```

| Flag | Short | Default | Description |
|------|-------|---------|-------------|
| `--max-age` | `-a` | `24h` | Delete containers older than this |
| `--dry-run` | `-n` | false | Show what would be deleted without deleting |

### `provider` — Provider Commands

```bash
# List all registered providers
elastic-ci-executor provider list

# Check provider config and test container lifecycle
elastic-ci-executor [--config PATH] provider check aliyun-eci
```

## Provider Configuration

All providers are configured as sections inside the single `config.toml`.

### AWS Fargate

```toml
provider = "aws-fargate"

[aws-fargate]
region = "us-east-1"
cluster = "my-ecs-cluster"
task_definition_family = "elastic-ci-executor"
subnets = ["subnet-12345678", "subnet-87654321"]
security_groups = ["sg-12345678"]
assign_public_ip = true
execution_role_arn = "arn:aws:iam::123456789012:role/ecsTaskExecutionRole"
```

### Aliyun ECI

```toml
provider = "aliyun-eci"

[aliyun-eci]
region = "cn-hangzhou"
vpc_id = "vpc-xxxxx"
vswitch_id = "vsw-xxxxx"
security_group_id = "sg-xxxxx"
# Credentials can also be set via env vars:
#   ALIYUN_ACCESS_KEY_ID / ALIYUN_ACCESS_KEY_SECRET
# access_key_id = ""
# access_key_secret = ""
# Image cache accelerates container startup (default: true)
# auto_match_image_cache = true
# EIP settings for public internet access
# auto_create_eip = true
# eip_bandwidth = 50
# eip_common_bandwidth_package = "cbwp-xxxxx"
# eip_instance_id = "eip-xxxxx"
```

### Azure ACI

```toml
provider = "azure-aci"

[azure-aci]
subscription_id = "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
resource_group = "my-resource-group"
location = "eastus"
# subnet_id = "/subscriptions/xxx/resourceGroups/xxx/providers/Microsoft.Network/virtualNetworks/xxx/subnets/xxx"
```

## Provider Permissions

### AWS Fargate

IAM permissions required:

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

RAM permissions required:

```json
{
  "Version": "1",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "eci:CreateContainerGroup",
        "eci:DescribeContainerGroups",
        "eci:ExecContainerCommand",
        "eci:DeleteContainerGroup",
        "eci:DescribeImageCaches",
        "eci:CreateImageCache",
        "eci:DeleteImageCache"
      ],
      "Resource": "*"
    }
  ]
}
```

**Credentials** (in priority order):
1. `access_key_id` / `access_key_secret` in config
2. Environment variables: `ALIYUN_ACCESS_KEY_ID`, `ALIYUN_ACCESS_KEY_SECRET`
3. Instance RAM role

### Azure ACI

**Authentication** (any one of):
- Azure CLI: `az login`
- Service Principal
- Managed Identity

## Image Proxy

The `image_proxy` setting rewrites container image references through a proxy host using crproxy-style path routing. This is useful in two scenarios:

### China cloud environments (crproxy)

When Docker Hub / GHCR are not directly accessible, use a self-hosted [crproxy](https://github.com/daocloud/crproxy) instance:

```toml
image_proxy = "crproxy.internal.example.com"
# ubuntu:latest → crproxy.internal.example.com/docker.io/library/ubuntu:latest
```

### AWS ECR Pull-Through Cache

ECR Pull-Through Cache caches upstream registry images in your ECR, reducing pull latency and avoiding Docker Hub rate limits. The `image_proxy` setting works directly with it — no extra code needed.

**Setup:**

1. Create pull-through cache rules in ECR (console, CLI, or Terraform). Set the ECR prefix to match the upstream registry domain:

   | Upstream Registry | ECR Prefix |
   |---|---|
   | Docker Hub | `docker.io` |
   | GitHub GHCR | `ghcr.io` |
   | Quay | `quay.io` |

2. Set `image_proxy` to your ECR endpoint:

   ```toml
   image_proxy = "123456789.dkr.ecr.us-east-1.amazonaws.com"
   # ubuntu:latest → 123456789.dkr.ecr.us-east-1.amazonaws.com/docker.io/library/ubuntu:latest
   ```

The first pull fetches from upstream and caches in ECR. Subsequent pulls are served directly from ECR in the same region.
