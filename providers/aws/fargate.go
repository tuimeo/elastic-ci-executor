package aws

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"
	globalconfig "github.com/tuimeo/elastic-ci-executor/config"
	"github.com/tuimeo/elastic-ci-executor/providers"
)

func init() {
	providers.Register(
		"aws-fargate",
		"AWS Fargate - Run containers on Amazon ECS Fargate",
		func(cfg *globalconfig.Config) (providers.Provider, error) {
			return NewFargateProvider(cfg)
		},
	)
}

const (
	helperContainerName = "helper"
	buildContainerName  = "build"
	providerName        = "aws-fargate"
	helperImageRegistry = "registry.gitlab.com/gitlab-org/gitlab-runner/gitlab-runner-helper"
	defaultHelperTag    = "x86_64-v18.9.0"
)

// FargateProvider implements the Provider interface for AWS Fargate
type FargateProvider struct {
	cfg       Config
	ecsClient *ecs.Client
}

// NewFargateProvider creates a new AWS Fargate provider
func NewFargateProvider(globalCfg *globalconfig.Config) (*FargateProvider, error) {
	var cfg Config
	if err := globalCfg.GetProviderConfig("aws-fargate", &cfg); err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid AWS config: %w", err)
	}

	ctx := context.Background()
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(cfg.Region),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS SDK config: %w", err)
	}

	return &FargateProvider{
		cfg:       cfg,
		ecsClient: ecs.NewFromConfig(awsCfg),
	}, nil
}

// CreateContainer creates and starts a new Fargate task with helper + build containers
func (p *FargateProvider) CreateContainer(ctx context.Context, settings providers.JobContainerSettings) (providers.JobContainer, error) {
	taskDefArn, err := p.ensureTaskDefinition(ctx, settings)
	if err != nil {
		return providers.JobContainer{}, fmt.Errorf("failed to ensure task definition: %w", err)
	}

	// Prepare network configuration: default to public IP enabled
	assignPublicIP := types.AssignPublicIpEnabled
	if p.cfg.AssignPublicIP != nil && !*p.cfg.AssignPublicIP {
		assignPublicIP = types.AssignPublicIpDisabled
	}

	subnets := append([]string{}, p.cfg.Subnets...)
	securityGroups := append([]string{}, p.cfg.SecurityGroups...)

	runTaskInput := &ecs.RunTaskInput{
		Cluster:        aws.String(p.cfg.Cluster),
		TaskDefinition: aws.String(taskDefArn),
		NetworkConfiguration: &types.NetworkConfiguration{
			AwsvpcConfiguration: &types.AwsVpcConfiguration{
				Subnets:        subnets,
				SecurityGroups: securityGroups,
				AssignPublicIp: assignPublicIP,
			},
		},
		EnableExecuteCommand: true,
	}

	// Both containers share the same keep-alive command override
	runTaskInput.Overrides = &types.TaskOverride{
		ContainerOverrides: []types.ContainerOverride{
			{Name: aws.String(buildContainerName), Command: settings.Command},
			{Name: aws.String(helperContainerName), Command: settings.Command},
		},
	}

	// Spot vs on-demand: use capacity provider strategy instead of LaunchType
	if settings.NoSpot {
		runTaskInput.CapacityProviderStrategy = []types.CapacityProviderStrategyItem{
			{CapacityProvider: aws.String("FARGATE"), Weight: 1},
		}
	} else {
		runTaskInput.CapacityProviderStrategy = []types.CapacityProviderStrategyItem{
			{CapacityProvider: aws.String("FARGATE_SPOT"), Weight: 1},
		}
	}

	result, err := p.ecsClient.RunTask(ctx, runTaskInput)
	if err != nil {
		return providers.JobContainer{}, fmt.Errorf("failed to run container: %w", err)
	}

	if len(result.Failures) > 0 {
		return providers.JobContainer{}, fmt.Errorf("container run failed: %s - %s",
			aws.ToString(result.Failures[0].Reason),
			aws.ToString(result.Failures[0].Detail))
	}

	if len(result.Tasks) == 0 {
		return providers.JobContainer{}, fmt.Errorf("no container was created")
	}

	taskArn := aws.ToString(result.Tasks[0].TaskArn)

	return providers.JobContainer{
		Provider:   providerName,
		Identifier: taskArn,
		Region:     p.cfg.Region,
		Extra: map[string]string{
			"cluster": p.cfg.Cluster,
		},
	}, nil
}

// WaitContainerReady waits until the task is in RUNNING state
func (p *FargateProvider) WaitContainerReady(ctx context.Context, container providers.JobContainer) error {
	waiter := ecs.NewTasksRunningWaiter(p.ecsClient)

	cluster := container.Extra["cluster"]
	err := waiter.Wait(ctx, &ecs.DescribeTasksInput{
		Cluster: aws.String(cluster),
		Tasks:   []string{container.Identifier},
	}, 5*time.Minute)

	if err != nil {
		return fmt.Errorf("failed waiting for container to be ready: %w", err)
	}

	return nil
}

// ExecCommand executes a command in the running container via ECS ExecuteCommand API + SSM WebSocket.
// It routes to the helper or build container based on Extra["targetContainer"].
func (p *FargateProvider) ExecCommand(ctx context.Context, container providers.JobContainer, script []byte, stdout io.Writer) (int, error) {
	targetContainer := container.Extra["targetContainer"]
	if targetContainer == "" {
		targetContainer = buildContainerName
	}

	cluster := container.Extra["cluster"]
	return p.execCommandViaAPI(ctx, cluster, container.Identifier, targetContainer, script, stdout)
}

// DestroyContainer stops the Fargate task
func (p *FargateProvider) DestroyContainer(ctx context.Context, container providers.JobContainer) error {
	cluster := container.Extra["cluster"]

	_, err := p.ecsClient.StopTask(ctx, &ecs.StopTaskInput{
		Cluster: aws.String(cluster),
		Task:    aws.String(container.Identifier),
		Reason:  aws.String("Container stopped by elastic-ci-executor"),
	})

	if err != nil {
		return fmt.Errorf("failed to stop container: %w", err)
	}

	return nil
}

// CheckPermissions verifies that the configured credentials have all required ECS permissions.
func (p *FargateProvider) CheckPermissions(ctx context.Context) []providers.PermissionCheckResult {
	var results []providers.PermissionCheckResult

	_, err := p.ecsClient.DescribeClusters(ctx, &ecs.DescribeClustersInput{
		Clusters: []string{p.cfg.Cluster},
	})
	results = append(results, providers.PermissionCheckResult{
		Action: "ecs:DescribeClusters", Passed: err == nil, Message: errMsg(err),
	})

	_, err = p.ecsClient.ListTasks(ctx, &ecs.ListTasksInput{
		Cluster: aws.String(p.cfg.Cluster),
	})
	results = append(results, providers.PermissionCheckResult{
		Action: "ecs:ListTasks", Passed: err == nil, Message: errMsg(err),
	})

	return results
}

func errMsg(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// ensureTaskDefinition registers a task definition with helper + build containers
func (p *FargateProvider) ensureTaskDefinition(ctx context.Context, settings providers.JobContainerSettings) (string, error) {
	family := p.cfg.TaskDefinitionFamily

	// Fargate CPU is in CPU units (1024 = 1 vCPU), Memory is in MiB
	totalCPU := (settings.BuildCPU + settings.HelperCPU) * 1024
	totalMemory := (settings.BuildMemory + settings.HelperMemory) * 1024

	// Resolve helper image: config override > auto-detect from CI_RUNNER_VERSION > default
	helperImage := settings.HelperImage
	if helperImage == "" {
		tag := defaultHelperTag
		if v := settings.EnvVars["CI_RUNNER_VERSION"]; v != "" {
			tag = "x86_64-v" + v
		}
		helperImage = helperImageRegistry + ":" + tag
	}
	helperImage = globalconfig.RewriteImageForProxy(settings.ImageProxy, helperImage)

	// Build environment variables (shared by both containers)
	var envVars []types.KeyValuePair
	for key, value := range settings.EnvVars {
		envVars = append(envVars, types.KeyValuePair{
			Name:  aws.String(key),
			Value: aws.String(value),
		})
	}

	logOptions := map[string]string{
		"awslogs-group":         fmt.Sprintf("/ecs/%s", family),
		"awslogs-region":        p.cfg.Region,
		"awslogs-stream-prefix": "ecs",
		"awslogs-create-group":  "true",
	}

	// Build container definition
	buildDef := types.ContainerDefinition{
		Name:      aws.String(buildContainerName),
		Image:     aws.String(settings.Image),
		Essential: aws.Bool(true),
		Cpu:       int32(settings.BuildCPU * 1024),    //nolint:gosec // G115 - CPU bounded by config validation
		Memory:    aws.Int32(int32(settings.BuildMemory * 1024)), //nolint:gosec // G115 - memory bounded by config validation
		MountPoints: []types.MountPoint{
			{SourceVolume: aws.String("builds"), ContainerPath: aws.String("/builds")},
		},
		Environment:   envVars,
		LogConfiguration: &types.LogConfiguration{
			LogDriver: types.LogDriverAwslogs,
			Options:   logOptions,
		},
	}

	// Helper container definition
	helperDef := types.ContainerDefinition{
		Name:      aws.String(helperContainerName),
		Image:     aws.String(helperImage),
		Essential: aws.Bool(true),
		Cpu:       int32(settings.HelperCPU * 1024),    //nolint:gosec // G115 - CPU bounded by config validation
		Memory:    aws.Int32(int32(settings.HelperMemory * 1024)), //nolint:gosec // G115 - memory bounded by config validation
		MountPoints: []types.MountPoint{
			{SourceVolume: aws.String("builds"), ContainerPath: aws.String("/builds")},
		},
		Environment:   envVars,
		LogConfiguration: &types.LogConfiguration{
			LogDriver: types.LogDriverAwslogs,
			Options:   logOptions,
		},
	}

	// Add repository credentials for private registry authentication (build image)
	if cred := matchCredential(settings.Image, settings.ImageRegistryCredentials); cred != nil {
		buildDef.RepositoryCredentials = &types.RepositoryCredentials{
			CredentialsParameter: aws.String(cred.CredentialsParameter),
		}
	}
	// Add repository credentials for helper image
	if cred := matchCredential(helperImage, settings.ImageRegistryCredentials); cred != nil {
		helperDef.RepositoryCredentials = &types.RepositoryCredentials{
			CredentialsParameter: aws.String(cred.CredentialsParameter),
		}
	}

	taskDef := &ecs.RegisterTaskDefinitionInput{
		Family:                  aws.String(family),
		NetworkMode:             types.NetworkModeAwsvpc,
		RequiresCompatibilities: []types.Compatibility{types.CompatibilityFargate},
		Cpu:                     aws.String(strconv.Itoa(totalCPU)),
		Memory:                  aws.String(strconv.Itoa(totalMemory)),
		ContainerDefinitions:    []types.ContainerDefinition{buildDef, helperDef},
		Volumes: []types.Volume{
			{Name: aws.String("builds")},
		},
	}

	// Add task role if specified
	if p.cfg.TaskRoleArn != "" {
		taskDef.TaskRoleArn = aws.String(p.cfg.TaskRoleArn)
	}

	// Add execution role if specified
	if p.cfg.ExecutionRoleArn != "" {
		taskDef.ExecutionRoleArn = aws.String(p.cfg.ExecutionRoleArn)
	}

	result, err := p.ecsClient.RegisterTaskDefinition(ctx, taskDef)
	if err != nil {
		return "", fmt.Errorf("failed to register task definition: %w", err)
	}

	return aws.ToString(result.TaskDefinition.TaskDefinitionArn), nil
}

// matchCredential finds the ImageRegistryCredential whose Server matches the
// image's registry domain. Only credentials with CredentialsParameter set are
// considered (AWS Fargate uses Secrets Manager ARNs, not username/password).
// Returns nil if no match is found.
func matchCredential(image string, creds []providers.ImageRegistryCredential) *providers.ImageRegistryCredential {
	domain := ""
	if slash := strings.IndexByte(image, '/'); slash > 0 {
		candidate := image[:slash]
		if strings.ContainsAny(candidate, ".:") {
			domain = candidate
		}
	}

	for i := range creds {
		if creds[i].CredentialsParameter == "" {
			continue
		}
		if creds[i].Server == domain {
			return &creds[i]
		}
	}
	return nil
}
