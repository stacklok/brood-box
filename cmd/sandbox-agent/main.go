// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package main is the entrypoint for the sandbox-agent CLI.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/stacklok/sandbox-agent/internal/app"
	"github.com/stacklok/sandbox-agent/internal/domain/agent"
	domainconfig "github.com/stacklok/sandbox-agent/internal/domain/config"
	"github.com/stacklok/sandbox-agent/internal/domain/snapshot"
	infraagent "github.com/stacklok/sandbox-agent/internal/infra/agent"
	infraconfig "github.com/stacklok/sandbox-agent/internal/infra/config"
	"github.com/stacklok/sandbox-agent/internal/infra/diff"
	"github.com/stacklok/sandbox-agent/internal/infra/exclude"
	"github.com/stacklok/sandbox-agent/internal/infra/review"
	infrassh "github.com/stacklok/sandbox-agent/internal/infra/ssh"
	infraterminal "github.com/stacklok/sandbox-agent/internal/infra/terminal"
	infravm "github.com/stacklok/sandbox-agent/internal/infra/vm"
	infraws "github.com/stacklok/sandbox-agent/internal/infra/workspace"
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
		cpus     uint32
		memory   uint32
		wsPath   string
		sshPort  uint16
		cfgPath  string
		image    string
		debug    bool
		noReview bool
		excludes []string
	)

	cmd := &cobra.Command{
		Use:   "sandbox-agent <agent-name>",
		Short: "Run coding agents in hardware-isolated sandbox VMs",
		Long: `sandbox-agent boots a microVM, mounts your workspace, forwards secrets,
and drops into an interactive terminal session with a coding agent.

By default, the workspace is mounted as a COW snapshot. After the agent
finishes, you review changes per-file before they touch the real workspace.
Use --no-review to disable snapshot isolation and mount the workspace directly.

Supported agents: claude-code, codex, opencode

Example:
  sandbox-agent claude-code
  sandbox-agent codex --cpus 4 --memory 4096
  sandbox-agent opencode --workspace /path/to/project
  sandbox-agent claude-code --no-review
  sandbox-agent claude-code --exclude "*.log" --exclude "tmp/"`,
		Args:    cobra.ExactArgs(1),
		Version: fmt.Sprintf("%s (%s)", version.Version, version.Commit),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), args[0], runFlags{
				cpus:      cpus,
				memory:    memory,
				workspace: wsPath,
				sshPort:   sshPort,
				cfgPath:   cfgPath,
				image:     image,
				debug:     debug,
				noReview:  noReview,
				excludes:  excludes,
			})
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.Flags().Uint32Var(&cpus, "cpus", 0, "Number of vCPUs (0 = agent default)")
	cmd.Flags().Uint32Var(&memory, "memory", 0, "RAM in MiB (0 = agent default)")
	cmd.Flags().StringVar(&wsPath, "workspace", "", "Workspace directory to mount (default: current directory)")
	cmd.Flags().Uint16Var(&sshPort, "ssh-port", 0, "Host SSH port (0 = auto-pick)")
	cmd.Flags().StringVar(&cfgPath, "config", "", "Config file path (default: ~/.config/sandbox-agent/config.yaml)")
	cmd.Flags().StringVar(&image, "image", "", "Override OCI image reference")
	cmd.Flags().BoolVar(&debug, "debug", false, "Enable debug logging")
	cmd.Flags().BoolVar(&noReview, "no-review", false, "Disable workspace snapshot isolation (mount workspace directly)")
	cmd.Flags().StringSliceVar(&excludes, "exclude", nil, "Additional exclude patterns for workspace snapshot (repeatable)")

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
	noReview  bool
	excludes  []string
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
	ws := flags.workspace
	if ws == "" {
		var err error
		ws, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("getting current directory: %w", err)
		}
	}

	// Clean up stale snapshot dirs from previous crashes.
	if !flags.noReview {
		infraws.CleanupStaleSnapshots(ws, logger)
	}

	// Build registry with config-based custom agents.
	registry := infraagent.NewRegistry()
	cfgLoader := infraconfig.NewLoader(flags.cfgPath)

	cfg, err := cfgLoader.Load()
	if err != nil {
		logger.Warn("failed to load config, using defaults", "error", err)
	}

	// Load per-workspace config and merge.
	localCfg, err := infraconfig.LoadFromPath(filepath.Join(ws, domainconfig.LocalConfigFile))
	if err != nil {
		logger.Warn("failed to load local config, ignoring", "error", err)
	}
	if localCfg != nil && localCfg.Review.Enabled != nil {
		logger.Warn("review.enabled in local config is ignored for security — use --no-review or global config")
	}
	cfg = domainconfig.MergeConfigs(cfg, localCfg)

	if cfg.Agents != nil {
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

	// Determine review mode. Default is enabled unless --no-review is set
	// or config explicitly disables it.
	reviewEnabled := !flags.noReview
	if reviewEnabled && cfg != nil && cfg.Review.Enabled != nil && !*cfg.Review.Enabled {
		reviewEnabled = false
	}

	// Merge exclude patterns from config and CLI.
	var excludePatterns []string
	if cfg != nil {
		excludePatterns = append(excludePatterns, cfg.Review.ExcludePatterns...)
	}
	excludePatterns = append(excludePatterns, flags.excludes...)

	// Build exclude matchers (moved from app layer to composition root).
	var snapshotMatcher, diffMatcher snapshot.Matcher
	if reviewEnabled {
		excludeCfg, err := exclude.LoadExcludeConfig(ws, excludePatterns, logger)
		if err != nil {
			return fmt.Errorf("loading exclude config: %w", err)
		}
		snapshotMatcher = exclude.NewMatcherFromConfig(excludeCfg)

		gitignorePatterns, err := exclude.LoadGitignorePatterns(ws, logger)
		if err != nil {
			logger.Warn("failed to load .gitignore patterns", "error", err)
		}
		diffMatcher = exclude.NewDiffMatcher(excludeCfg, gitignorePatterns)
	}

	// Wire dependencies.
	terminal := infraterminal.NewOSTerminal(os.Stdin, os.Stdout, os.Stderr)

	var reviewer *review.InteractiveReviewer
	deps := app.SandboxDeps{
		Registry:      registry,
		VMRunner:      infravm.NewPropolisRunner("", logger),
		SessionRunner: infrassh.NewInteractiveSession(logger),
		Terminal:      terminal,
		Config:        cfg,
		EnvProvider:   agent.NewOSEnvProvider(os.Environ),
		Logger:        logger,
	}

	// Wire snapshot isolation dependencies only when review is enabled.
	if reviewEnabled {
		deps.WorkspaceCloner = infraws.NewFSWorkspaceCloner(
			infraws.NewPlatformCloner(), logger,
		)
		reviewer = review.NewInteractiveReviewer(os.Stdin, os.Stdout)
		deps.Reviewer = reviewer
		deps.Flusher = review.NewFSFlusher()
		deps.Differ = diff.NewFSDiffer()
	}

	runner := app.NewSandboxRunner(deps)

	opts := app.RunOpts{
		CPUs:          flags.cpus,
		Memory:        flags.memory,
		Workspace:     ws,
		SSHPort:       flags.sshPort,
		ImageOverride: flags.image,
		Snapshot: app.SnapshotOpts{
			Enabled:         reviewEnabled,
			SnapshotMatcher: snapshotMatcher,
			DiffMatcher:     diffMatcher,
		},
	}

	sb, err := runner.Prepare(ctx, agentName, opts)
	if err != nil {
		return err
	}
	defer func() {
		logger.Info("cleaning up workspace snapshot")
		if cleanErr := sb.Cleanup(); cleanErr != nil {
			logger.Error("failed to clean up snapshot", "error", cleanErr)
		}
	}()

	restore, _ := terminal.MakeRaw()
	termErr := runner.Attach(ctx, sb, terminal)
	restore()

	if stopErr := runner.Stop(sb); stopErr != nil {
		logger.Error("failed to stop VM", "error", stopErr)
	}

	var reviewErr error
	if reviewEnabled && sb.Snapshot != nil && reviewer != nil {
		changes, chErr := runner.Changes(sb)
		if chErr != nil {
			reviewErr = chErr
		} else if len(changes) > 0 {
			logger.Info("workspace changes detected", "count", len(changes))
			result, revErr := reviewer.Review(changes)
			if revErr != nil {
				reviewErr = fmt.Errorf("reviewing changes: %w", revErr)
			} else if len(result.Accepted) > 0 {
				logger.Info("flushing accepted changes",
					"accepted", len(result.Accepted),
					"rejected", len(result.Rejected),
				)
				reviewErr = runner.Flush(sb, result.Accepted)
			} else {
				logger.Info("no changes accepted")
			}
		} else {
			logger.Info("no workspace changes detected")
		}
		if reviewErr != nil {
			logger.Error("review/flush failed", "error", reviewErr)
		}
	}

	err = termErr
	if err == nil {
		err = reviewErr
	}

	if err != nil {
		// Propagate the agent's exit code without printing an error.
		var exitErr *infrassh.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		}
		// Print available agents on not-found errors.
		var notFound *agent.ErrNotFound
		if errors.As(err, &notFound) {
			_, _ = fmt.Fprintf(os.Stderr, "\nAvailable agents:\n")
			for _, a := range registry.List() {
				_, _ = fmt.Fprintf(os.Stderr, "  %-15s %s\n", a.Name, a.Image)
			}
		}
		return err
	}

	return nil
}
