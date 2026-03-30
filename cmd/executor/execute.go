package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/tuimeo/elastic-ci-executor/config"
	"github.com/tuimeo/elastic-ci-executor/internal/executor"

	// Import all providers to trigger their init() registration
	_ "github.com/tuimeo/elastic-ci-executor/providers/aliyun"
	_ "github.com/tuimeo/elastic-ci-executor/providers/aws"
	_ "github.com/tuimeo/elastic-ci-executor/providers/azure"
	"github.com/urfave/cli/v3"
)

// Common flags for all custom commands (empty now, kept for future use)
var commonFlags = []cli.Flag{}

// ExecuteCommand groups all GitLab Custom Executor lifecycle commands
func newExecuteCommand() *cli.Command {
	return &cli.Command{
		Name:    "execute",
		Aliases: []string{"exec", "e"},
		Usage:   "GitLab Runner Custom Executor lifecycle commands",
		Commands: []*cli.Command{
			newConfigCommand(),
			newPrepareCommand(),
			newRunCommand(),
			newCleanupCommand(),
		},
	}
}

// ConfigCommand implements the "config" stage
func newConfigCommand() *cli.Command {
	return &cli.Command{
		Name:  "config",
		Usage: "Output driver configuration (config stage)",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			response := map[string]interface{}{
				"driver": map[string]string{
					"name":    "elastic-ci-executor",
					"version": _VERSION,
				},
				"builds_dir": "/builds",
				"cache_dir":  "/cache",
				"shell":      "sh",
			}

			encoder := json.NewEncoder(os.Stdout)
			return encoder.Encode(response)
		},
	}
}

// PrepareCommand implements the "prepare" stage
func newPrepareCommand() *cli.Command {
	return &cli.Command{
		Name:  "prepare",
		Usage: "Prepare the execution environment (prepare stage)",
		Flags: commonFlags,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			return executor.PrepareStage(ctx, cfg)
		},
	}
}

// RunCommand implements the "run" stage
func newRunCommand() *cli.Command {
	return &cli.Command{
		Name:      "run",
		Usage:     "Execute a script in the container (run stage)",
		ArgsUsage: "<script_path> <stage_name>",
		Flags:     commonFlags,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			if cmd.NArg() < 2 {
				return fmt.Errorf("missing required arguments: script_path and stage_name")
			}

			scriptPath := cmd.Args().Get(0)
			stageName := cmd.Args().Get(1)

			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}

			return executor.RunStage(ctx, cfg, scriptPath, stageName)
		},
	}
}

// CleanupCommand implements the "cleanup" stage
func newCleanupCommand() *cli.Command {
	return &cli.Command{
		Name:  "cleanup",
		Usage: "Clean up the execution environment (cleanup stage)",
		Flags: commonFlags,
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			return executor.CleanupStage(ctx, cfg)
		},
	}
}

// loadConfig loads config from config file
func loadConfig(cmd *cli.Command) (*config.Config, error) {
	// Get config path from global flag if specified
	configPath := cmd.String("config")

	var cfg *config.Config
	var err error

	if configPath == "" {
		cfg, err = config.AutoDiscoverLoadConfig()
	} else {
		cfg, err = config.LoadFromFile(configPath)
	}

	if err != nil {
		return nil, err
	}

	return cfg, nil
}
