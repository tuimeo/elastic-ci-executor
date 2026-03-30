package main

import (
	"context"
	"time"

	"github.com/tuimeo/elastic-ci-executor/internal/executor"
	"github.com/urfave/cli/v3"
)

func newMgmtCommand() *cli.Command {
	return &cli.Command{
		Name:     "mgmt",
		Aliases:  []string{"m"},
		Usage:    "Management commands for containers and metadata",
		Commands: []*cli.Command{
			newListCommand(),
			newCleanupStaleCommand(),
		},
	}
}

func newListCommand() *cli.Command {
	return &cli.Command{
		Name:    "list",
		Aliases: []string{"ls"},
		Usage:   "List all stored container metadata",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}
			return executor.ListContainers(cfg, cfg.JobStore)
		},
	}
}

func newCleanupStaleCommand() *cli.Command {
	return &cli.Command{
		Name:    "cleanup-stale",
		Aliases: []string{"cleanup"},
		Usage:   "Clean up stale containers that were not properly cleaned up",
		Flags: []cli.Flag{
			&cli.DurationFlag{
				Name:    "max-age",
				Aliases: []string{"a"},
				Usage:   "Maximum age of containers to keep",
				Value:   24 * time.Hour,
			},
			&cli.BoolFlag{
				Name:    "dry-run",
				Aliases: []string{"n"},
				Usage:   "Show what would be deleted without actually deleting",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			cfg, err := loadConfig(cmd)
			if err != nil {
				return err
			}

			opts := executor.CleanupStaleOptions{
				Config:   cfg,
				JobStore: cfg.JobStore,
				MaxAge:   cmd.Duration("max-age"),
				DryRun:   cmd.Bool("dry-run"),
			}
			return executor.CleanupStaleContainers(ctx, opts)
		},
	}
}

