// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package main is the entrypoint for the sandbox-agent CLI.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/stacklok/sandbox-agent/internal/app"
	"github.com/stacklok/sandbox-agent/internal/domain/agent"
	infraagent "github.com/stacklok/sandbox-agent/internal/infra/agent"
	infraconfig "github.com/stacklok/sandbox-agent/internal/infra/config"
	infrassh "github.com/stacklok/sandbox-agent/internal/infra/ssh"
	"github.com/stacklok/sandbox-agent/internal/infra/vm"
	"github.com/stacklok/sandbox-agent/internal/version"
)

func main() {
	if err := rootCmd().Execute(); err != nil {
		// Cobra won't print the error (SilenceErrors: true), so we do.
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var (
		cpus      uint32
		memory    uint32
		workspace string
		sshPort   uint16
		cfgPath   string
		image     string
		debug     bool
	)

	cmd := &cobra.Command{
		Use:   "sandbox-agent <agent-name>",
		Short: "Run coding agents in hardware-isolated sandbox VMs",
		Long: `sandbox-agent boots a microVM, mounts your workspace, forwards secrets,
and drops into an interactive terminal session with a coding agent.

Supported agents: claude-code, codex, opencode

Example:
  sandbox-agent claude-code
  sandbox-agent codex --cpus 4 --memory 4096
  sandbox-agent opencode --workspace /path/to/project`,
		Args:    cobra.ExactArgs(1),
		Version: fmt.Sprintf("%s (%s)", version.Version, version.Commit),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), args[0], runFlags{
				cpus:      cpus,
				memory:    memory,
				workspace: workspace,
				sshPort:   sshPort,
				cfgPath:   cfgPath,
				image:     image,
				debug:     debug,
			})
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.Flags().Uint32Var(&cpus, "cpus", 0, "Number of vCPUs (0 = agent default)")
	cmd.Flags().Uint32Var(&memory, "memory", 0, "RAM in MiB (0 = agent default)")
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace directory to mount (default: current directory)")
	cmd.Flags().Uint16Var(&sshPort, "ssh-port", 0, "Host SSH port (0 = auto-pick)")
	cmd.Flags().StringVar(&cfgPath, "config", "", "Config file path (default: ~/.config/sandbox-agent/config.yaml)")
	cmd.Flags().StringVar(&image, "image", "", "Override OCI image reference")
	cmd.Flags().BoolVar(&debug, "debug", false, "Enable debug logging")

	// Add list subcommand.
	cmd.AddCommand(listCmd())

	return cmd
}

func listCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List available agents",
		RunE: func(_ *cobra.Command, _ []string) error {
			registry := infraagent.NewRegistry()
			agents := registry.List()
			for _, a := range agents {
				fmt.Printf("%-15s %s\n", a.Name, a.Image)
			}
			return nil
		},
	}
}

type runFlags struct {
	cpus      uint32
	memory    uint32
	workspace string
	sshPort   uint16
	cfgPath   string
	image     string
	debug     bool
}

func run(parentCtx context.Context, agentName string, flags runFlags) error {
	// Set up signal-aware context.
	ctx, cancel := signal.NotifyContext(parentCtx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	var logLevel slog.LevelVar
	logLevel.Set(slog.LevelInfo)
	if flags.debug {
		logLevel.Set(slog.LevelDebug)
	} else if lvl := os.Getenv("SLOG_LEVEL"); lvl != "" {
		if err := logLevel.UnmarshalText([]byte(lvl)); err != nil {
			return fmt.Errorf("invalid SLOG_LEVEL %q: %w", lvl, err)
		}
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: &logLevel,
	}))
	slog.SetDefault(logger)

	// Resolve workspace.
	workspace := flags.workspace
	if workspace == "" {
		var err error
		workspace, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("getting current directory: %w", err)
		}
	}

	// Build registry with config-based custom agents.
	registry := infraagent.NewRegistry()
	cfgLoader := infraconfig.NewLoader(flags.cfgPath)

	cfg, err := cfgLoader.Load()
	if err != nil {
		logger.Warn("failed to load config, using defaults", "error", err)
	} else if cfg.Agents != nil {
		// Register custom agents from config (only those not already built-in).
		for name, override := range cfg.Agents {
			if _, err := registry.Get(name); err != nil {
				// Not a built-in agent — register as custom if it has an image.
				if override.Image != "" {
					registry.Add(agent.Agent{
						Name:          name,
						Image:         override.Image,
						Command:       override.Command,
						EnvForward:    override.EnvForward,
						DefaultCPUs:   override.CPUs,
						DefaultMemory: override.Memory,
					})
				}
			}
		}
	}

	// Wire dependencies.
	runner := app.NewSandboxRunner(app.SandboxDeps{
		Registry:    registry,
		VMRunner:    vm.NewPropolisRunner("", logger),
		Terminal:    infrassh.NewInteractiveSession(logger),
		CfgLoader:   cfgLoader,
		EnvProvider: agent.NewOSEnvProvider(os.Environ),
		Logger:      logger,
	})

	err = runner.Run(ctx, agentName, app.RunOpts{
		CPUs:          flags.cpus,
		Memory:        flags.memory,
		Workspace:     workspace,
		SSHPort:       flags.sshPort,
		ImageOverride: flags.image,
	})

	if err != nil {
		// Propagate the agent's exit code without printing an error.
		if exitErr, ok := err.(*infrassh.ExitError); ok {
			os.Exit(exitErr.Code)
		}
		// Print available agents on not-found errors.
		var notFound *agent.ErrNotFound
		if isErrNotFound(err, &notFound) {
			fmt.Fprintf(os.Stderr, "\nAvailable agents:\n")
			for _, a := range registry.List() {
				fmt.Fprintf(os.Stderr, "  %-15s %s\n", a.Name, a.Image)
			}
		}
		return err
	}

	return nil
}

// isErrNotFound checks if err wraps an agent.ErrNotFound and sets target.
func isErrNotFound(err error, target **agent.ErrNotFound) bool {
	for err != nil {
		if e, ok := err.(*agent.ErrNotFound); ok {
			*target = e
			return true
		}
		// Unwrap if possible.
		if unwrapper, ok := err.(interface{ Unwrap() error }); ok {
			err = unwrapper.Unwrap()
		} else {
			break
		}
	}
	return false
}
