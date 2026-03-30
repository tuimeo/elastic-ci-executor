package aliyun

import (
	"fmt"
)

// Config contains Aliyun-specific configuration
type Config struct {
	Region          string `toml:"region"`
	VpcId           string `toml:"vpc_id"`
	VSwitchId       string `toml:"vswitch_id"`
	SecurityGroupId string `toml:"security_group_id"`
	AccessKeyId     string `toml:"access_key_id"`
	AccessKeySecret string `toml:"access_key_secret"`
	// AutoCreateEip automatically allocates and binds a new EIP per container for public internet access.
	// Required when the VSwitch has no SNAT gateway. The EIP is released when the container is destroyed.
	// Defaults to true.
	AutoCreateEip *bool `toml:"auto_create_eip"`
	// EipBandwidth sets the EIP bandwidth cap in Mbps when AutoCreateEip is true. Defaults to 50.
	EipBandwidth *int `toml:"eip_bandwidth"`
	// EipCommonBandwidthPackage associates the auto-created EIP with a shared bandwidth package (optional).
	EipCommonBandwidthPackage string `toml:"eip_common_bandwidth_package"`
	// EipInstanceId binds a pre-allocated EIP. Use this only for serial (non-concurrent) jobs.
	EipInstanceId string `toml:"eip_instance_id"`
	// AutoMatchImageCache automatically matches and uses the best image cache to accelerate container startup.
	// Defaults to true when not set.
	AutoMatchImageCache *bool `toml:"auto_match_image_cache"`
}

// Validate checks if the Aliyun configuration is valid
func (c *Config) Validate() error {
	if c.Region == "" {
		return fmt.Errorf("region is required")
	}
	if c.VpcId == "" {
		return fmt.Errorf("vpc_id is required")
	}
	if c.VSwitchId == "" {
		return fmt.Errorf("vswitch_id is required")
	}
	if c.SecurityGroupId == "" {
		return fmt.Errorf("security_group_id is required")
	}
	return nil
}
