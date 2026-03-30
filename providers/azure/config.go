package azure

import "fmt"

// Config contains Azure-specific configuration
type Config struct {
	SubscriptionId string `toml:"subscription_id"`
	ResourceGroup  string `toml:"resource_group"`
	Location       string `toml:"location"`
	SubnetId       string `toml:"subnet_id"`
}

// Validate checks if the Azure configuration is valid
func (c *Config) Validate() error {
	if c.SubscriptionId == "" {
		return fmt.Errorf("subscription_id is required")
	}
	if c.ResourceGroup == "" {
		return fmt.Errorf("resource_group is required")
	}
	if c.Location == "" {
		return fmt.Errorf("location is required")
	}
	return nil
}
