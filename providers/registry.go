package providers

import (
	"fmt"
	"sort"
	"sync"

	"github.com/tuimeo/elastic-ci-executor/config"
)

// ProviderFactory is a function that creates a provider instance from config
type ProviderFactory func(cfg *config.Config) (Provider, error)

// ProviderInfo contains metadata about a registered provider
type ProviderInfo struct {
	Name        string
	Description string
	Factory     ProviderFactory
}

var (
	registry = make(map[string]ProviderInfo)
	mu       sync.RWMutex
)

// Register registers a provider with the global registry
// This should be called from init() functions in provider packages
func Register(name, description string, factory ProviderFactory) {
	mu.Lock()
	defer mu.Unlock()

	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("provider %s is already registered", name))
	}

	registry[name] = ProviderInfo{
		Name:        name,
		Description: description,
		Factory:     factory,
	}
}

// Get retrieves a provider factory by name
func Get(name string) (ProviderInfo, bool) {
	mu.RLock()
	defer mu.RUnlock()

	info, exists := registry[name]
	return info, exists
}

// List returns all registered providers sorted by name
func List() []ProviderInfo {
	mu.RLock()
	defer mu.RUnlock()

	var providers []ProviderInfo
	for _, info := range registry {
		providers = append(providers, info)
	}

	sort.Slice(providers, func(i, j int) bool {
		return providers[i].Name < providers[j].Name
	})

	return providers
}

// Create creates a provider instance by name using the factory
func Create(name string, cfg *config.Config) (Provider, error) {
	info, exists := Get(name)
	if !exists {
		return nil, fmt.Errorf("provider %s is not registered", name)
	}

	return info.Factory(cfg)
}
