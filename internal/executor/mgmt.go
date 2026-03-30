package executor

import (
	"context"
	"fmt"
	"time"

	"github.com/tuimeo/elastic-ci-executor/config"
	"github.com/tuimeo/elastic-ci-executor/internal/metadata"
	"github.com/tuimeo/elastic-ci-executor/providers"
)

// createProviderByName creates a provider by name (for cleanup operations)
func createProviderByName(cfg *config.Config, providerName string) (providers.Provider, error) {
	return providers.Create(providerName, cfg)
}

// ListContainers lists all stored container metadata
func ListContainers(cfg *config.Config, jobstore string) error {
	store, err := metadata.NewFileStore(jobstore)
	if err != nil {
		return fmt.Errorf("failed to create metadata store: %w", err)
	}
	defer func() { _ = store.Close() }()

	containers, err := store.List()
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}

	if len(containers) == 0 {
		fmt.Println("No containers found")
		return nil
	}

	fmt.Printf("Found %d container(s):\n\n", len(containers))
	for i, container := range containers {
		age := time.Since(container.CreatedAt)
		fmt.Printf("%d. Job ID: %s\n", i+1, container.JobID)
		fmt.Printf("   Provider: %s\n", container.Provider)
		fmt.Printf("   Container ID: %s\n", container.ContainerID)
		fmt.Printf("   Created: %s (age: %v)\n", container.CreatedAt.Format(time.RFC3339), age)
		if len(container.Extra) > 0 {
			fmt.Printf("   Extra: %v\n", container.Extra)
		}
		fmt.Println()
	}

	return nil
}

// CleanupStaleOptions holds options for cleanup operation
type CleanupStaleOptions struct {
	Config   *config.Config
	JobStore string
	MaxAge   time.Duration
	DryRun   bool
}

// CleanupStaleContainers cleans up stale containers
func CleanupStaleContainers(ctx context.Context, opts CleanupStaleOptions) error {
	store, err := metadata.NewFileStore(opts.JobStore)
	if err != nil {
		return fmt.Errorf("failed to create metadata store: %w", err)
	}
	defer func() { _ = store.Close() }()

	containers, err := store.List()
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}

	var staleCount int
	for _, container := range containers {
		age := time.Since(container.CreatedAt)
		if age > opts.MaxAge {
			staleCount++
			fmt.Printf("Found stale container: %s (age: %v, provider: %s)\n",
				container.JobID, age, container.Provider)

			if opts.DryRun {
				fmt.Printf("  [DRY RUN] Would destroy container: %s\n", container.ContainerID)
				continue
			}

			// Create provider and destroy container
			provider, err := createProviderByName(opts.Config, container.Provider)
			if err != nil {
				fmt.Printf("  ✗ Failed to create provider: %v\n", err)
				continue
			}

			instance := providers.JobContainer{
				Provider:   container.Provider,
				Identifier: container.ContainerID,
				Region:     container.Region,
				Extra:      container.Extra,
			}

			if err := provider.DestroyContainer(ctx, instance); err != nil {
				fmt.Printf("  ✗ Failed to destroy container: %v\n", err)
			} else {
				fmt.Printf("  ✓ Container destroyed\n")
			}

			if err := store.Delete(container.JobID); err != nil {
				fmt.Printf("  ✗ Failed to delete metadata: %v\n", err)
			} else {
				fmt.Printf("  ✓ Metadata deleted\n")
			}
		}
	}

	switch {
	case staleCount == 0:
		fmt.Printf("No stale containers found (max age: %v)\n", opts.MaxAge)
	case opts.DryRun:
		fmt.Printf("\n[DRY RUN] Would have cleaned up %d stale container(s)\n", staleCount)
	default:
		fmt.Printf("\nCleaned up %d stale container(s)\n", staleCount)
	}

	return nil
}
