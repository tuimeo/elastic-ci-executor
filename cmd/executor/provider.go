package main

import (
	"context"
	"fmt"

	"github.com/tuimeo/elastic-ci-executor/internal/executor"
	// Import all providers to trigger their init() registration
	_ "github.com/tuimeo/elastic-ci-executor/providers/aliyun"
	_ "github.com/tuimeo/elastic-ci-executor/providers/aws"
	_ "github.com/tuimeo/elastic-ci-executor/providers/azure"
	"github.com/urfave/cli/v3"
)

func newProviderCommand() *cli.Command {
	return &cli.Command{
		Name:    "provider",
		Aliases: []string{"p"},
		Usage:   "Provider management commands",
		Commands: []*cli.Command{
			newProviderListCommand(),
			newProviderCheckCommand(),
		},
	}
}

func newProviderListCommand() *cli.Command {
	return &cli.Command{
		Name:    "list",
		Aliases: []string{"ls"},
		Usage:   "List all registered providers",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return executor.ListProviders()
		},
	}
}

func newProviderCheckCommand() *cli.Command {
	return &cli.Command{
		Name:      "check",
		Usage:     "Check provider configuration and test container lifecycle",
		ArgsUsage: "<provider>",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() < 1 {
				return fmt.Errorf("missing required argument: provider")
			}

			providerName := cmd.Args().Get(0)

			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}

			return executor.CheckProvider(ctx, providerName, cfg)
		},
	}
}
