package aws

import "fmt"

// Config contains AWS-specific configuration
type Config struct {
	Region               string   `toml:"region"`
	Cluster              string   `toml:"cluster"`
	Subnets              []string `toml:"subnets"`
	SecurityGroups       []string `toml:"security_groups"`
	TaskDefinitionFamily string   `toml:"task_definition_family"`
	// AssignPublicIP assigns a public IP to the Fargate task for internet access.
	// Defaults to true when not set (most users run in public subnets).
	// Set to false when using private subnets with a NAT Gateway.
	AssignPublicIP *bool `toml:"assign_public_ip"`
	TaskRoleArn          string   `toml:"task_role_arn"`
	ExecutionRoleArn     string   `toml:"execution_role_arn"`
}

// Validate checks if the AWS configuration is valid
func (c *Config) Validate() error {
	if c.Region == "" {
		return fmt.Errorf("region is required")
	}
	if c.Cluster == "" {
		return fmt.Errorf("cluster is required")
	}
	if len(c.Subnets) == 0 {
		return fmt.Errorf("at least one subnet is required")
	}
	if len(c.SecurityGroups) == 0 {
		return fmt.Errorf("at least one security group is required")
	}
	if c.TaskDefinitionFamily == "" {
		return fmt.Errorf("task_definition_family is required")
	}
	if c.ExecutionRoleArn == "" {
		return fmt.Errorf("execution_role_arn is required (needed for CloudWatch Logs and image pulls)")
	}
	return nil
}
