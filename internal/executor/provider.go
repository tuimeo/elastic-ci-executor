package executor

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/tuimeo/elastic-ci-executor/config"
	"github.com/tuimeo/elastic-ci-executor/providers"
)

// ListProviders lists all registered providers
func ListProviders() error {
	providerList := providers.List()

	if len(providerList) == 0 {
		fmt.Println("No providers registered")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "NAME\tDESCRIPTION")
	_, _ = fmt.Fprintln(w, "----\t-----------")

	for _, p := range providerList {
		_, _ = fmt.Fprintf(w, "%s\t%s\n", p.Name, p.Description)
	}

	return w.Flush()
}

// CheckProvider checks provider configuration and tests container lifecycle
func CheckProvider(ctx context.Context, providerName string, cfg *config.Config) error {
	fmt.Printf("🔍 Checking provider: %s\n\n", providerName)

	// Check if provider is registered
	info, exists := providers.Get(providerName)
	if !exists {
		return fmt.Errorf("provider %s is not registered. Run 'elastic-ci-executor provider list' to see available providers", providerName)
	}

	fmt.Printf("✓ Provider registered: %s\n", info.Description)

	fmt.Printf("✓ Config path: %s\n", cfg.ConfigFilePath)

	// Check if config file exists
	if _, err := os.Stat(cfg.ConfigFilePath); os.IsNotExist(err) {
		return fmt.Errorf("config file not found: %s", cfg.ConfigFilePath)
	}

	fmt.Printf("✓ Config file exists\n\n")

	// Create provider instance
	fmt.Println("📦 Creating provider instance...")
	provider, err := providers.Create(providerName, cfg)
	if err != nil {
		return fmt.Errorf("failed to create provider: %w", err)
	}
	fmt.Println("✓ Provider instance created")

	// Check permissions if provider supports it
	if checker, ok := provider.(providers.PermissionChecker); ok {
		fmt.Println("\n🔑 Checking permissions...")
		results := checker.CheckPermissions(ctx)
		allPassed := true
		for _, r := range results {
			if r.Passed {
				fmt.Printf("  ✓ %s\n", r.Action)
			} else {
				allPassed = false
				fmt.Printf("  ✗ %s — %s\n", r.Action, r.Message)
			}
		}
		if !allPassed {
			return fmt.Errorf("permission check failed: some required permissions are missing")
		}
		fmt.Println("✓ All permissions verified")
	}

	// Test container lifecycle
	fmt.Println("\n🧪 Testing container lifecycle...")

	testCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	// Prepare container settings using defaults from config
	settings := providers.JobContainerSettings{
		Image:      config.RewriteImageForProxy(cfg.ImageProxy, cfg.Image),
		BuildCPU:    cfg.BuildCPU,
		BuildMemory: cfg.BuildMemory,
		EnvVars: map[string]string{
			"TEST_MODE": "true",
		},
		Command:      buildKeepAliveCommand(cfg),
		ImageProxy:   cfg.ImageProxy,
		HelperImage:  cfg.HelperImage,
		HelperCPU:    cfg.HelperCPU,
		HelperMemory: cfg.HelperMemory,
	}
	if validateErr := settings.Validate(); validateErr != nil {
		return fmt.Errorf("invalid container settings: %w", validateErr)
	}

	fmt.Printf("\n1️⃣  Creating test container...\n")
	fmt.Printf("   Image: %s\n", settings.Image)
	fmt.Printf("   CPU: %d, Memory: %d\n", settings.BuildCPU, settings.BuildMemory)

	container, err := provider.CreateContainer(testCtx, settings)
	if err != nil {
		return fmt.Errorf("❌ Failed to create container: %w", err)
	}
	fmt.Printf("✓ Container created: %s\n", container.Identifier)

	// Ensure cleanup happens
	defer func() {
		fmt.Println("\n4️⃣  Cleaning up test container...")
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()

		if destroyErr := provider.DestroyContainer(cleanupCtx, container); destroyErr != nil {
			fmt.Printf("⚠️  Warning: Failed to destroy container: %v\n", destroyErr)
		} else {
			fmt.Println("✓ Container destroyed")
		}
	}()

	fmt.Println("\n2️⃣  Waiting for container to be ready...")
	if waitErr := provider.WaitContainerReady(testCtx, container); waitErr != nil {
		return fmt.Errorf("❌ Failed to wait for container: %w", waitErr)
	}
	fmt.Println("✓ Container is ready")

	fmt.Println("\n3️⃣  Testing command execution...")
	testScript := []byte("#!/bin/sh\necho 'Hello from test container'\necho 'Current directory:'\npwd\necho 'Environment:'\nenv | grep TEST_MODE\n")

	var output []byte
	outputWriter := &bufferWriter{buf: &output}

	exitCode, err := provider.ExecCommand(testCtx, container, testScript, outputWriter)
	if err != nil {
		return fmt.Errorf("❌ Failed to execute command: %w", err)
	}

	fmt.Printf("✓ Command executed (exit code: %d)\n", exitCode)
	if len(output) > 0 {
		fmt.Printf("\n📄 Output:\n%s\n", string(output))
	}

	if exitCode != 0 {
		return fmt.Errorf("❌ Command exited with non-zero code: %d", exitCode)
	}

	fmt.Println("\n✅ All checks passed! Provider is configured correctly.")
	return nil
}

// bufferWriter is a simple io.Writer that writes to a byte slice
type bufferWriter struct {
	buf *[]byte
}

func (w *bufferWriter) Write(p []byte) (n int, err error) {
	*w.buf = append(*w.buf, p...)
	return len(p), nil
}
