package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// ImageRegistryCredential contains authentication info for pulling images from private registries.
// Two forms are supported:
//   - Username/Password: set server, username, password (used by Aliyun ECI, Azure ACI)
//   - Credentials Parameter: set server, credentials_parameter (AWS Secrets Manager ARN, used by AWS Fargate)
type ImageRegistryCredential struct {
	Server               string `toml:"server"`               // Registry server address (e.g., "registry.example.com")
	Username             string `toml:"username"`             // Registry username
	Password             string `toml:"password"`             // Registry password
	CredentialsParameter string `toml:"credentials_parameter"` // AWS Secrets Manager ARN containing {"username":"...","password":"..."}
}

// Config contains all configuration for the executor
type Config struct {
	Provider   string `toml:"provider"`    // "aws-fargate", "aliyun-eci", "azure-aci"
	LogLevel   string `toml:"log_level"`
	JobStore   string `toml:"jobstore"`    // Path to jobstore directory (default: ./jobstore)
	ImageProxy string `toml:"image_proxy"` // Docker image proxy host (e.g., "proxy.example.com"). Uses crproxy-style path routing.

	// Container settings
	Image                    string                    `toml:"image"`
	BuildCPU                 int                       `toml:"build_cpu"`
	BuildMemory              int                       `toml:"build_memory"`
	EnvVars                  map[string]string         `toml:"env_vars"`
	NoSpot                   bool                      `toml:"no_spot"`                    // Use on-demand instances instead of spot/preemptible (default: false = use spot)
	JobTimeout               int                       `toml:"job_timeout"`                 // Max container lifetime in minutes (default: 60). Container sleeps for this duration then exits, preventing resource leaks.
	ImageRegistryCredentials []ImageRegistryCredential `toml:"image_registry_credentials"` // Credentials for pulling images from private registries
	HelperImage              string                    `toml:"helper_image"`                // Override the gitlab-runner-helper image for infrastructure stages (git clone, cache, artifacts)
	HelperCPU                int                       `toml:"helper_cpu"`                  // Helper container CPU in vCPU cores (default: 2)
	HelperMemory             int                       `toml:"helper_memory"`               // Helper container memory in GiB (default: 4)

	// Internal not from TOML
	ConfigFilePath string        // Path to the config file, not from TOML
	TOMLMetadata   toml.MetaData // Providers use this parsed TOML metadata to get provider-specific config
}

// LoadFromFile loads configuration from a TOML file
func LoadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // G304 - path from CLI --config flag
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	metadata, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Set defaults
	if cfg.Provider == "" {
		return nil, fmt.Errorf("provider is required in config.toml")
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	if cfg.JobStore == "" {
		cfg.JobStore = "./jobstore"
	}
	if cfg.Image == "" {
		cfg.Image = "alpine:latest"
	}
	if cfg.BuildCPU == 0 {
		cfg.BuildCPU = 2 // 2 vCPU default (unit: cores)
	}
	if cfg.BuildMemory == 0 {
		cfg.BuildMemory = 4 // 4 GiB default (unit: GiB)
	}
	if cfg.HelperCPU == 0 {
		cfg.HelperCPU = 2
	}
	if cfg.HelperMemory == 0 {
		cfg.HelperMemory = 4
	}
	cfg.ConfigFilePath = path
	cfg.TOMLMetadata = metadata

	return &cfg, nil
}

func AutoDiscoverLoadConfig() (*Config, error) {
	path, err := findConfigFile()
	if err != nil {
		return nil, err
	}
	return LoadFromFile(path)
}

// GetProviderConfig extracts provider-specific configuration from the config file.
// Provider should pass a pointer to their config struct.
// Example: var awsCfg AWSConfig; cfg.GetProviderConfig("aws-fargate", &awsCfg)
func (c *Config) GetProviderConfig(providerName string, target interface{}) error {
	data, err := os.ReadFile(c.ConfigFilePath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	// Decode into a map of Primitives for lazy per-section decoding
	var primitives map[string]toml.Primitive
	md, err := toml.Decode(string(data), &primitives)
	if err != nil {
		return fmt.Errorf("failed to parse config: %w", err)
	}

	prim, exists := primitives[providerName]
	if !exists {
		return fmt.Errorf("provider section [%s] not found in config", providerName)
	}

	if err := md.PrimitiveDecode(prim, target); err != nil {
		return fmt.Errorf("failed to decode provider config for %s: %w", providerName, err)
	}

	return nil
}

// FindConfigFile searches for config.toml in standard locations
// Returns the path to the first config file found, or error if none found
func findConfigFile() (string, error) {
	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}

	// Get executable directory
	exePath, err := os.Executable()
	exeDir := "."
	if err == nil {
		exeDir = filepath.Dir(exePath)
	}

	// Get home directory for user config
	homeDir, err := os.UserHomeDir()
	userConfigDir := ""
	if err == nil {
		userConfigDir = filepath.Join(homeDir, ".config", "elastic-ci-executor")
	}

	// Search paths in priority order
	searchPaths := []string{
		filepath.Join(cwd, "config.toml"),           // 1. Current working directory
		"/etc/elastic-ci-executor/config.toml",      // 2. System config
		filepath.Join(userConfigDir, "config.toml"), // 3. User config
		filepath.Join(exeDir, "config.toml"),        // 4. Executable directory
	}

	for _, path := range searchPaths {
		if path == "" {
			continue
		}
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("config.toml not found in any of the standard locations: %v", searchPaths)
}
