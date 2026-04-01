package aliyun

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	eciclient "github.com/alibabacloud-go/eci-20180808/v3/client"
	globalconfig "github.com/tuimeo/elastic-ci-executor/config"
	"github.com/tuimeo/elastic-ci-executor/internal/logger"
	"github.com/tuimeo/elastic-ci-executor/providers"
)

func init() {
	providers.Register(
		"aliyun-eci",
		"Aliyun ECI - Run containers on Alibaba Cloud Elastic Container Instance",
		func(cfg *globalconfig.Config) (providers.Provider, error) {
			return NewECIProvider(cfg)
		},
	)
}

const (
	helperContainerName = "helper"
	buildContainerName  = "build"
	providerName        = "aliyun-eci"
	helperImageRegistry = "registry.gitlab.com/gitlab-org/gitlab-runner/gitlab-runner-helper"
	defaultHelperTag    = "x86_64-v18.9.0"

)

// ECIProvider implements the Provider interface for Aliyun ECI
type ECIProvider struct {
	cfg       Config
	eciClient *eciclient.Client
}

// NewECIProvider creates a new Aliyun ECI provider
func NewECIProvider(globalCfg *globalconfig.Config) (*ECIProvider, error) {
	var cfg Config
	if err := globalCfg.GetProviderConfig("aliyun-eci", &cfg); err != nil {
		return nil, fmt.Errorf("failed to load Aliyun config: %w", err)
	}
	logger.Debug("loaded aliyun config", "config_file", globalCfg.ConfigFilePath, "region", cfg.Region, "vpc_id", cfg.VpcId)

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid Aliyun config: %w", err)
	}

	accessKeyId := cfg.AccessKeyId
	accessKeySecret := cfg.AccessKeySecret
	if accessKeyId == "" {
		accessKeyId = os.Getenv("ALIYUN_ACCESS_KEY_ID")
	}
	if accessKeySecret == "" {
		accessKeySecret = os.Getenv("ALIYUN_ACCESS_KEY_SECRET")
	}
	if accessKeyId == "" || accessKeySecret == "" {
		return nil, fmt.Errorf("aliyun credentials not found in config or environment variables")
	}

	endpoint := fmt.Sprintf("eci.%s.aliyuncs.com", cfg.Region)
	openapiCfg := &openapi.Config{
		AccessKeyId:     &accessKeyId,
		AccessKeySecret: &accessKeySecret,
		RegionId:        &cfg.Region,
		Endpoint:        &endpoint,
	}

	client, err := eciclient.NewClient(openapiCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create ECI client: %w", err)
	}

	return &ECIProvider{
		cfg:       cfg,
		eciClient: client,
	}, nil
}

// CreateContainer creates and starts a new ECI container group with helper + build containers
func (p *ECIProvider) CreateContainer(ctx context.Context, settings providers.JobContainerSettings) (providers.JobContainer, error) {
	name := p.generateContainerGroupName()

	// Configured CPU/Memory is the total for the ContainerGroup.
	// Build container gets the remainder after reserving helper's share.
	helperCPU := float32(settings.HelperCPU)
	helperMem := float32(settings.HelperMemory)
	buildCPU := float32(settings.BuildCPU)
	buildMem := float32(settings.BuildMemory)


	command := settings.Command
	cmdPtrs := make([]*string, len(command))
	for i := range command {
		cmdPtrs[i] = &command[i]
	}

	imagePullPolicy := "IfNotPresent"

	// Build environment variables (shared by both containers)
	var envVars []*eciclient.CreateContainerGroupRequestContainerEnvironmentVar
	if len(settings.EnvVars) > 0 {
		for k, v := range settings.EnvVars {
			k, v := k, v
			envVars = append(envVars, &eciclient.CreateContainerGroupRequestContainerEnvironmentVar{
				Key:   &k,
				Value: &v,
			})
		}
	}

	// Shared EmptyDir volume for /builds
	volumeName := "builds"
	buildsMount := []*eciclient.CreateContainerGroupRequestContainerVolumeMount{
		{
			Name:      &volumeName,
			MountPath: strPtr("/builds"),
		},
	}

	// Helper container: git clone, cache, artifacts
	// Priority: config helper_image > auto-detect from CI_RUNNER_VERSION > default tag
	helperImage := settings.HelperImage
	if helperImage == "" {
		tag := defaultHelperTag
		if v := settings.EnvVars["CI_RUNNER_VERSION"]; v != "" {
			tag = "x86_64-v" + v
		}
		helperImage = helperImageRegistry + ":" + tag
	}
	// Apply image proxy if configured
	helperImage = globalconfig.RewriteImageForProxy(settings.ImageProxy, helperImage)
	hCPU := helperCPU
	hMem := helperMem
	helperContainer := &eciclient.CreateContainerGroupRequestContainer{
		Name:            strPtr(helperContainerName),
		Image:           &helperImage,
		Cpu:             &hCPU,
		Memory:          &hMem,
		Command:         cmdPtrs,
		ImagePullPolicy: &imagePullPolicy,
		EnvironmentVar:  envVars,
		VolumeMount:     buildsMount,
	}

	// Build container: user scripts
	buildContainer := &eciclient.CreateContainerGroupRequestContainer{
		Name:            strPtr(buildContainerName),
		Image:           &settings.Image,
		Cpu:             &buildCPU,
		Memory:          &buildMem,
		Command:         cmdPtrs,
		ImagePullPolicy: &imagePullPolicy,
		EnvironmentVar:  envVars,
		VolumeMount:     buildsMount,
	}

	req := &eciclient.CreateContainerGroupRequest{
		RegionId:           &p.cfg.Region,
		ContainerGroupName: &name,
		RestartPolicy:      strPtr("Never"),
		VSwitchId:          &p.cfg.VSwitchId,
		SecurityGroupId:    &p.cfg.SecurityGroupId,
		Container:          []*eciclient.CreateContainerGroupRequestContainer{helperContainer, buildContainer},
		Volume: []*eciclient.CreateContainerGroupRequestVolume{
			p.buildEmptyDirVolume(volumeName),
		},
	}

	// Set image registry credentials (only username/password entries, skip CredentialsParameter-only)
	if len(settings.ImageRegistryCredentials) > 0 {
		var creds []*eciclient.CreateContainerGroupRequestImageRegistryCredential
		for _, c := range settings.ImageRegistryCredentials {
			if c.Username == "" || c.Password == "" {
				continue
			}
			c := c
			creds = append(creds, &eciclient.CreateContainerGroupRequestImageRegistryCredential{
				Server:   &c.Server,
				UserName: &c.Username,
				Password: &c.Password,
			})
		}
		if len(creds) > 0 {
			req.ImageRegistryCredential = creds
		}
	}

	autoCreateEip := p.cfg.AutoCreateEip == nil || *p.cfg.AutoCreateEip
	if autoCreateEip && p.cfg.EipInstanceId == "" {
		t := true
		req.AutoCreateEip = &t
		bw := 50
		if p.cfg.EipBandwidth != nil {
			bw = *p.cfg.EipBandwidth
		}
		bw32 := int32(bw) //nolint:gosec // G115 - bandwidth bounded by cloud API limits
		req.EipBandwidth = &bw32
		if p.cfg.EipCommonBandwidthPackage != "" {
			req.EipCommonBandwidthPackage = &p.cfg.EipCommonBandwidthPackage
		}
	} else if p.cfg.EipInstanceId != "" {
		req.EipInstanceId = &p.cfg.EipInstanceId
	}

	// Auto-match image cache: defaults to true when not explicitly configured
	autoMatchImageCache := p.cfg.AutoMatchImageCache == nil || *p.cfg.AutoMatchImageCache
	req.AutoMatchImageCache = &autoMatchImageCache

	// Set spot strategy: use SpotAsPriceGo by default (cheaper), or NoSpot if specified
	if settings.NoSpot {
		spotStrategy := "NoSpot"
		req.SpotStrategy = &spotStrategy
	} else {
		spotStrategy := "SpotAsPriceGo"
		req.SpotStrategy = &spotStrategy
	}

	logger.Debug("calling CreateContainerGroup",
		"name", name,
		"region", p.cfg.Region,
		"vswitch", p.cfg.VSwitchId,
		"security_group", p.cfg.SecurityGroupId,
		"build_image", settings.Image,
		"helper_image", helperImage,
		"auto_create_eip", autoCreateEip,
		"no_spot", settings.NoSpot,
		"spot_strategy", *req.SpotStrategy,
		"auto_match_image_cache", autoMatchImageCache,
		"image_pull_policy", imagePullPolicy,
	)

	resp, err := p.eciClient.CreateContainerGroup(req)
	if err != nil {
		return providers.JobContainer{}, fmt.Errorf("failed to create container group: %w", err)
	}

	createdId := ""
	if resp.Body != nil && resp.Body.ContainerGroupId != nil {
		createdId = *resp.Body.ContainerGroupId
	}
	requestId := ""
	if resp.Body != nil && resp.Body.RequestId != nil {
		requestId = *resp.Body.RequestId
	}
	logger.Debug("CreateContainerGroup response", "id", createdId, "request_id", requestId)

	return providers.JobContainer{
		Provider:   providerName,
		Identifier: createdId,
		Region:     p.cfg.Region,
		Extra: map[string]string{
			"containerGroupName": name,
		},
	}, nil
}

// WaitContainerReady waits until the container group is in Running state
func (p *ECIProvider) WaitContainerReady(ctx context.Context, container providers.JobContainer) error {
	logger.Debug("waiting for container", "id", container.Identifier, "region", p.cfg.Region)

	containerGroupIds := fmt.Sprintf("[%q]", container.Identifier)
	req := &eciclient.DescribeContainerGroupsRequest{
		RegionId:          &p.cfg.Region,
		ContainerGroupIds: &containerGroupIds,
	}

	maxAttempts := 60
	lastStatus := ""
	for i := 0; i < maxAttempts; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		logger.Debug("calling DescribeContainerGroups", "attempt", i+1, "endpoint", fmt.Sprintf("eci.%s.aliyuncs.com", p.cfg.Region), "region", p.cfg.Region)

		resp, err := p.eciClient.DescribeContainerGroups(req)
		if err != nil {
			logger.Errorf("DescribeContainerGroups failed: %v\n", err)
			return fmt.Errorf("failed to describe container group: %w", err)
		}

		if resp.Body == nil {
			logger.Println("Warning: response body is nil")
			time.Sleep(5 * time.Second)
			continue
		}

		requestId := ""
		if resp.Body.RequestId != nil {
			requestId = *resp.Body.RequestId
		}
		totalCount := int32(0)
		if resp.Body.TotalCount != nil {
			totalCount = *resp.Body.TotalCount
		}
		logger.Debug("describe response",
			"request_id", requestId,
			"total_count", totalCount,
			"groups_in_page", len(resp.Body.ContainerGroups),
		)

		found := false
		for _, cg := range resp.Body.ContainerGroups {
			id, status, name := "", "", ""
			if cg.ContainerGroupId != nil {
				id = *cg.ContainerGroupId
			}
			if cg.Status != nil {
				status = *cg.Status
			}
			if cg.ContainerGroupName != nil {
				name = *cg.ContainerGroupName
			}
			logger.Debug("container group in response", "id", id, "name", name, "status", status)

			if id != container.Identifier {
				continue
			}
			found = true

			// Show status only when it changes
			if status != lastStatus {
				lastStatus = status
				switch {
				case logger.IsVerbose():
					logger.Printf("Target container status: %s = %s\n", id, status)
				case status == "Running":
					fmt.Printf("Waiting for container to be ready... %s ✓\n", status)
				default:
					fmt.Printf("Waiting for container to be ready... %s\n", status)
				}
			}

			switch status {
			case "Running":
				return nil
			case "Failed", "Expired":
				return fmt.Errorf("container group failed with status: %s", status)
			}
		}

		if !found {
			logger.Debug("target container not in response", "looking_for", container.Identifier, "total_count", totalCount)
		}

		time.Sleep(5 * time.Second)
	}

	return fmt.Errorf("timeout waiting for container group to be ready")
}

// ExecCommand executes a command in the running container via ECI API + WebSocket.
// It routes to the helper or build container based on Extra["targetContainer"].
func (p *ECIProvider) ExecCommand(ctx context.Context, container providers.JobContainer, script []byte, stdout io.Writer) (int, error) {
	targetContainer := container.Extra["targetContainer"]
	if targetContainer == "" {
		targetContainer = buildContainerName
	}
	return p.execCommandViaAPI(ctx, container.Identifier, targetContainer, script, stdout)
}

// DestroyContainer deletes the ECI container group
func (p *ECIProvider) DestroyContainer(ctx context.Context, container providers.JobContainer) error {
	req := &eciclient.DeleteContainerGroupRequest{
		RegionId:         &p.cfg.Region,
		ContainerGroupId: &container.Identifier,
	}

	_, err := p.eciClient.DeleteContainerGroup(req)
	if err != nil {
		return fmt.Errorf("failed to delete container group: %w", err)
	}

	return nil
}

// CheckPermissions verifies that the configured credentials have all required ECI permissions.
func (p *ECIProvider) CheckPermissions(ctx context.Context) []providers.PermissionCheckResult {
	var results []providers.PermissionCheckResult

	// Check eci:DescribeContainerGroups (required for WaitContainerReady)
	_, err := p.eciClient.DescribeContainerGroups(&eciclient.DescribeContainerGroupsRequest{
		RegionId: &p.cfg.Region,
	})
	results = append(results, permResult("eci:DescribeContainerGroups", err))

	// Check eci:DescribeImageCaches (required for AutoMatchImageCache)
	_, err = p.eciClient.DescribeImageCaches(&eciclient.DescribeImageCachesRequest{
		RegionId: &p.cfg.Region,
	})
	results = append(results, permResult("eci:DescribeImageCaches", err))

	return results
}

func permResult(action string, err error) providers.PermissionCheckResult {
	if err == nil {
		return providers.PermissionCheckResult{Action: action, Passed: true}
	}
	return providers.PermissionCheckResult{Action: action, Passed: false, Message: err.Error()}
}

func (p *ECIProvider) generateContainerGroupName() string {
	return fmt.Sprintf("elastic-ci-%d", time.Now().Unix())
}

// buildEmptyDirVolume creates the /builds EmptyDir volume.
// Defaults to memory-backed (tmpfs); set builds_in_memory = false for cloud disk.
func (p *ECIProvider) buildEmptyDirVolume(name string) *eciclient.CreateContainerGroupRequestVolume {
	vol := &eciclient.CreateContainerGroupRequestVolume{
		Name: &name,
		Type: strPtr("EmptyDirVolume"),
	}
	buildsInMemory := p.cfg.BuildsInMemory == nil || *p.cfg.BuildsInMemory
	if buildsInMemory {
		vol.EmptyDirVolume = &eciclient.CreateContainerGroupRequestVolumeEmptyDirVolume{
			Medium: strPtr("Memory"),
		}
	}
	return vol
}

func strPtr(s string) *string { return &s }

