package azure

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerinstance/armcontainerinstance/v2"
	globalconfig "github.com/tuimeo/elastic-ci-executor/config"
	"github.com/tuimeo/elastic-ci-executor/providers"
)

func init() {
	providers.Register(
		"azure-aci",
		"Azure ACI - Run containers on Microsoft Azure Container Instances",
		func(cfg *globalconfig.Config) (providers.Provider, error) {
			return NewACIProvider(cfg)
		},
	)
}

const (
	helperContainerName = "helper"
	buildContainerName  = "build"
	providerName        = "azure-aci"
	helperImageRegistry = "registry.gitlab.com/gitlab-org/gitlab-runner/gitlab-runner-helper"
	defaultHelperTag    = "x86_64-v18.9.0"
)

// ACIProvider implements the Provider interface for Azure Container Instances
type ACIProvider struct {
	cfg                   Config
	containerGroupsClient *armcontainerinstance.ContainerGroupsClient
	containerClient       *armcontainerinstance.ContainersClient
}

// NewACIProvider creates a new Azure ACI provider
func NewACIProvider(globalCfg *globalconfig.Config) (*ACIProvider, error) {
	var cfg Config
	if err := globalCfg.GetProviderConfig("azure-aci", &cfg); err != nil {
		return nil, fmt.Errorf("failed to load Azure config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid Azure config: %w", err)
	}

	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure credential: %w", err)
	}

	groupsClient, err := armcontainerinstance.NewContainerGroupsClient(
		cfg.SubscriptionId, cred, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create container groups client: %w", err)
	}

	containersClient, err := armcontainerinstance.NewContainersClient(
		cfg.SubscriptionId, cred, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create containers client: %w", err)
	}

	return &ACIProvider{
		cfg:                   cfg,
		containerGroupsClient: groupsClient,
		containerClient:       containersClient,
	}, nil
}

// CreateContainer creates and starts a new ACI container group with helper + build containers
func (p *ACIProvider) CreateContainer(ctx context.Context, settings providers.JobContainerSettings) (providers.JobContainer, error) {
	containerGroupName := p.generateContainerGroupName()

	buildCPU := float64(settings.BuildCPU)
	buildMem := float64(settings.BuildMemory)
	helperCPU := float64(settings.HelperCPU)
	helperMem := float64(settings.HelperMemory)

	command := toStringPtrSlice(settings.Command)

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
	var envVars []*armcontainerinstance.EnvironmentVariable
	for key, value := range settings.EnvVars {
		k := key
		v := value
		envVars = append(envVars, &armcontainerinstance.EnvironmentVariable{
			Name:  &k,
			Value: &v,
		})
	}

	// Shared empty volume for /builds
	volumeName := "builds"
	volumeMounts := []*armcontainerinstance.VolumeMount{
		{Name: &volumeName, MountPath: stringPtr("/builds")},
	}

	// Build container
	buildContainer := &armcontainerinstance.Container{
		Name: stringPtr(buildContainerName),
		Properties: &armcontainerinstance.ContainerProperties{
			Image:   &settings.Image,
			Command: command,
			Resources: &armcontainerinstance.ResourceRequirements{
				Requests: &armcontainerinstance.ResourceRequests{
					CPU:        &buildCPU,
					MemoryInGB: &buildMem,
				},
			},
			EnvironmentVariables: envVars,
			VolumeMounts:         volumeMounts,
		},
	}

	// Helper container
	helperContainer := &armcontainerinstance.Container{
		Name: stringPtr(helperContainerName),
		Properties: &armcontainerinstance.ContainerProperties{
			Image:   &helperImage,
			Command: command,
			Resources: &armcontainerinstance.ResourceRequirements{
				Requests: &armcontainerinstance.ResourceRequests{
					CPU:        &helperCPU,
					MemoryInGB: &helperMem,
				},
			},
			EnvironmentVariables: envVars,
			VolumeMounts:         volumeMounts,
		},
	}

	osType := armcontainerinstance.OperatingSystemTypesLinux
	restartPolicy := armcontainerinstance.ContainerGroupRestartPolicyNever

	containerGroup := armcontainerinstance.ContainerGroup{
		Location: &p.cfg.Location,
		Properties: &armcontainerinstance.ContainerGroupPropertiesProperties{
			OSType:        &osType,
			RestartPolicy: &restartPolicy,
			Containers:    []*armcontainerinstance.Container{buildContainer, helperContainer},
			Volumes: []*armcontainerinstance.Volume{
				{Name: &volumeName, EmptyDir: map[string]any{}},
			},
		},
	}

	// Note: ACI Spot containers lack public IP and VNet support, making them
	// unusable for CI jobs that need network access. Always use regular priority.
	// See: https://learn.microsoft.com/en-us/azure/container-instances/container-instances-spot-containers-overview

	// Set image registry credentials (only username/password entries, skip CredentialsParameter-only)
	if len(settings.ImageRegistryCredentials) > 0 {
		var creds []*armcontainerinstance.ImageRegistryCredential
		for _, c := range settings.ImageRegistryCredentials {
			if c.Username == "" || c.Password == "" {
				continue
			}
			c := c
			creds = append(creds, &armcontainerinstance.ImageRegistryCredential{
				Server:   &c.Server,
				Username: &c.Username,
				Password: &c.Password,
			})
		}
		if len(creds) > 0 {
			containerGroup.Properties.ImageRegistryCredentials = creds
		}
	}

	// Add subnet if specified
	if p.cfg.SubnetId != "" {
		subnetID := p.cfg.SubnetId
		containerGroup.Properties.SubnetIDs = []*armcontainerinstance.ContainerGroupSubnetID{
			{ID: &subnetID},
		}
	}

	// Create container group
	poller, err := p.containerGroupsClient.BeginCreateOrUpdate(
		ctx, p.cfg.ResourceGroup, containerGroupName, containerGroup, nil,
	)
	if err != nil {
		return providers.JobContainer{}, fmt.Errorf("failed to begin creating container group: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return providers.JobContainer{}, fmt.Errorf("failed to create container group: %w", err)
	}

	return providers.JobContainer{
		Provider:   providerName,
		Identifier: containerGroupName,
		Region:     p.cfg.Location,
		Extra: map[string]string{
			"resourceGroup": p.cfg.ResourceGroup,
		},
	}, nil
}

// WaitContainerReady waits until the container group is in Running state
func (p *ACIProvider) WaitContainerReady(ctx context.Context, container providers.JobContainer) error {
	resourceGroup := container.Extra["resourceGroup"]
	maxAttempts := 60

	for range maxAttempts {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		response, err := p.containerGroupsClient.Get(
			ctx, resourceGroup, container.Identifier, nil,
		)
		if err != nil {
			return fmt.Errorf("failed to get container group: %w", err)
		}

		if response.Properties != nil && response.Properties.InstanceView != nil {
			state := response.Properties.InstanceView.State
			if state != nil {
				switch *state {
				case "Running":
					return nil
				case "Failed", "Stopped":
					return fmt.Errorf("container group failed with state: %s", *state)
				}
			}
		}

		time.Sleep(5 * time.Second)
	}

	return fmt.Errorf("timeout waiting for container group to be ready")
}

// ExecCommand executes a command in the running container via ACI API + WebSocket.
// It routes to the helper or build container based on Extra["targetContainer"].
func (p *ACIProvider) ExecCommand(ctx context.Context, container providers.JobContainer, script []byte, stdout io.Writer) (int, error) {
	targetContainer := container.Extra["targetContainer"]
	if targetContainer == "" {
		targetContainer = buildContainerName
	}

	resourceGroup := container.Extra["resourceGroup"]
	return p.execCommandViaAPI(ctx, resourceGroup, container.Identifier, targetContainer, script, stdout)
}

// DestroyContainer deletes the ACI container group
func (p *ACIProvider) DestroyContainer(ctx context.Context, container providers.JobContainer) error {
	resourceGroup := container.Extra["resourceGroup"]

	poller, err := p.containerGroupsClient.BeginDelete(
		ctx, resourceGroup, container.Identifier, nil,
	)
	if err != nil {
		return fmt.Errorf("failed to begin deleting container group: %w", err)
	}

	_, err = poller.PollUntilDone(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to delete container group: %w", err)
	}

	return nil
}

// CheckPermissions verifies that the configured credentials have all required ACI permissions.
func (p *ACIProvider) CheckPermissions(ctx context.Context) []providers.PermissionCheckResult {
	var results []providers.PermissionCheckResult

	pager := p.containerGroupsClient.NewListByResourceGroupPager(p.cfg.ResourceGroup, nil)
	_, err := pager.NextPage(ctx)
	action := "Microsoft.ContainerInstance/containerGroups/read"
	if err == nil {
		results = append(results, providers.PermissionCheckResult{Action: action, Passed: true})
	} else {
		results = append(results, providers.PermissionCheckResult{Action: action, Passed: false, Message: err.Error()})
	}

	return results
}

func (p *ACIProvider) generateContainerGroupName() string {
	timestamp := time.Now().UnixNano()
	// Use nanosecond timestamp truncated to 8 hex chars for uniqueness
	return fmt.Sprintf("elastic-ci-%d-%08x", timestamp/1e9, uint32(timestamp)) //nolint:gosec // G115 - truncation intentional for name uniqueness
}

func stringPtr(s string) *string {
	return &s
}

func toStringPtrSlice(s []string) []*string {
	result := make([]*string, len(s))
	for i := range s {
		result[i] = &s[i]
	}
	return result
}
