package main

import (
	"context"
	"fmt"
	"os"

	"github.com/tuimeo/elastic-ci-executor/internal/logger"
	"github.com/urfave/cli/v3"
)

// leave to init by Makefile
var _VERSION = "DEV"
var _BUILD_TIME = "UNKNOWN"

func main() {
	// Override built-in version flag to use -V instead of -v
	cli.VersionFlag = &cli.BoolFlag{
		Name:    "version",
		Aliases: []string{"V"},
		Usage:   "print the version",
	}

	cmd := &cli.Command{
		Name:    "elastic-ci-executor",
		Usage:   "GitLab Runner Custom Executor for multiple cloud providers",
		Version: fmt.Sprintf("%v built at %v", _VERSION, _BUILD_TIME),
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Aliases: []string{"c"},
				Usage:   "Path to config.toml (auto-discovers if not specified)",
				Sources: cli.EnvVars("ECIE_CONFIG"),
			},
&cli.BoolFlag{
				Name:    "verbose",
				Aliases: []string{"v"},
				Usage:   "Enable debug logging (overrides log_level in config)",
				Sources: cli.EnvVars("ECIE_VERBOSE"),
			},
		},
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			isVerbose := cmd.Bool("verbose")
			level := "info"
			if isVerbose {
				level = "debug"
			} else {
				// Try to read log_level from config if available
				if cfg, err := loadConfig(cmd); err == nil {
					level = cfg.LogLevel
				}
			}
			logger.Init(level, isVerbose)
			if isVerbose {
				logger.Debug("debug logging enabled")
			}
			return ctx, nil
		},
		Commands: []*cli.Command{
			// GitLab Custom Executor stages
			newExecuteCommand(),
			// Management commands
			newMgmtCommand(),
			// Provider commands
			newProviderCommand(),
		},
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
