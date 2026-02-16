// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package main is the entrypoint for the sandbox-agent CLI.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/stacklok/sandbox-agent/internal/app"
	"github.com/stacklok/sandbox-agent/internal/domain/agent"
	domainconfig "github.com/stacklok/sandbox-agent/internal/domain/config"
	"github.com/stacklok/sandbox-agent/internal/domain/egress"
	"github.com/stacklok/sandbox-agent/internal/domain/progress"
	"github.com/stacklok/sandbox-agent/internal/domain/snapshot"
	infraagent "github.com/stacklok/sandbox-agent/internal/infra/agent"
	infraconfig "github.com/stacklok/sandbox-agent/internal/infra/config"
	"github.com/stacklok/sandbox-agent/internal/infra/diff"
	"github.com/stacklok/sandbox-agent/internal/infra/exclude"
	infralogging "github.com/stacklok/sandbox-agent/internal/infra/logging"
	inframcp "github.com/stacklok/sandbox-agent/internal/infra/mcp"
	infraprogress "github.com/stacklok/sandbox-agent/internal/infra/progress"
	"github.com/stacklok/sandbox-agent/internal/infra/review"
	infrassh "github.com/stacklok/sandbox-agent/internal/infra/ssh"
	infraterminal "github.com/stacklok/sandbox-agent/internal/infra/terminal"
	infravm "github.com/stacklok/sandbox-agent/internal/infra/vm"
	infraws "github.com/stacklok/sandbox-agent/internal/infra/workspace"
	"github.com/stacklok/sandbox-agent/internal/version"
)

// defaultLogDir is the directory for sandbox-agent log files.
const defaultLogDir = ".config/sandbox-agent/logs"

// defaultLogFile is the log file name within the log directory.
const defaultLogFile = "sandbox-agent.log"

// maxLogSize is the maximum log file size before truncation (10 MiB).
const maxLogSize = 10 * 1024 * 1024

func main() {
	if err := rootCmd().Execute(); err != nil {
		// Cobra won't print the error (SilenceErrors: true), so we do.
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var (
		cpus          uint32
		memory        uint32
		wsPath        string
		sshPort       uint16
		cfgPath       string
		image         string
		debug         bool
		noReview      bool
		excludes      []string
		logFile       string
		egressProfile string
		allowHosts    []string
		mcpEnabled    bool
		mcpGroup      string
		mcpPort       uint16
		mcpConfig     string
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
  sandbox-agent claude-code --exclude "*.log" --exclude "tmp/"
  sandbox-agent claude-code --egress-profile locked
  sandbox-agent claude-code --allow-host "custom-api.example.com:443"
  sandbox-agent claude-code --mcp
  sandbox-agent claude-code --mcp --mcp-group "coding-tools"`,
		Args:    cobra.ExactArgs(1),
		Version: fmt.Sprintf("%s (%s)", version.Version, version.Commit),
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), args[0], runFlags{
				cpus:          cpus,
				memory:        memory,
				workspace:     wsPath,
				sshPort:       sshPort,
				cfgPath:       cfgPath,
				image:         image,
				debug:         debug,
				noReview:      noReview,
				excludes:      excludes,
				logFile:       logFile,
				egressProfile: egressProfile,
				allowHosts:    allowHosts,
				mcpEnabled:    mcpEnabled,
				mcpGroup:      mcpGroup,
				mcpPort:       mcpPort,
				mcpConfig:     mcpConfig,
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
	cmd.Flags().BoolVar(&debug, "debug", false, "Enable debug logging (shows full slog output on stderr)")
	cmd.Flags().BoolVar(&noReview, "no-review", false, "Disable workspace snapshot isolation (mount workspace directly)")
	cmd.Flags().StringSliceVar(&excludes, "exclude", nil, "Additional exclude patterns for workspace snapshot (repeatable)")
	cmd.Flags().StringVar(&logFile, "log-file", "", "Override log file path (default: ~/.config/sandbox-agent/logs/sandbox-agent.log)")
	cmd.Flags().StringVar(&egressProfile, "egress-profile", "", "Egress restriction level: permissive, standard, locked (default: agent's built-in default)")
	cmd.Flags().StringSliceVar(&allowHosts, "allow-host", nil, "Additional allowed egress host, format: hostname[:port] (repeatable)")
	cmd.Flags().BoolVar(&mcpEnabled, "mcp", false, "Enable MCP tool proxy (discovers servers from ToolHive)")
	cmd.Flags().StringVar(&mcpGroup, "mcp-group", "default", "ToolHive group to discover MCP servers from")
	cmd.Flags().Uint16Var(&mcpPort, "mcp-port", 4483, "Port for MCP proxy on VM gateway")
	cmd.Flags().StringVar(&mcpConfig, "mcp-config", "", "Path to custom vmcp config YAML")

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
	cpus          uint32
	memory        uint32
	workspace     string
	sshPort       uint16
	cfgPath       string
	image         string
	debug         bool
	noReview      bool
	excludes      []string
	logFile       string
	egressProfile string
	allowHosts    []string
	mcpEnabled    bool
	mcpGroup      string
	mcpPort       uint16
	mcpConfig     string
}

func run(parentCtx context.Context, agentName string, flags runFlags) error {
	// Set up signal-aware context.
	ctx, cancel := signal.NotifyContext(parentCtx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Set up logging: always write to file, optionally also to stderr.
	logFile, logCloser, err := openLogFile(flags.logFile)
	if err != nil {
		// Non-fatal: fall back to stderr-only logging.
		_, _ = fmt.Fprintf(os.Stderr, "Warning: could not open log file: %s\n", err)
	}
	if logCloser != nil {
		defer func() { _ = logCloser.Close() }()
	}

	logger := setupLogger(logFile, flags.debug)
	slog.SetDefault(logger)

	// Set up progress observer based on mode.
	terminal := infraterminal.NewOSTerminal(os.Stdin, os.Stdout, os.Stderr)
	observer := chooseObserver(terminal, logger, flags.debug)

	// Resolve workspace.
	ws := flags.workspace
	if ws == "" {
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
	if localCfg != nil && localCfg.Defaults.EgressProfile != "" {
		globalProfile := egress.ProfileName(cfg.Defaults.EgressProfile)
		localProfile := egress.ProfileName(localCfg.Defaults.EgressProfile)
		if globalProfile.IsValid() && localProfile.IsValid() && egress.Stricter(globalProfile, localProfile) == globalProfile {
			logger.Warn("egress_profile in local config cannot widen global — keeping global profile",
				"global", globalProfile, "local", localProfile)
		}
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
	var reviewer *review.InteractiveReviewer
	deps := app.SandboxDeps{
		Registry:      registry,
		VMRunner:      infravm.NewPropolisRunner("", logger),
		SessionRunner: infrassh.NewInteractiveSession(logger),
		Config:        cfg,
		EnvProvider:   agent.NewOSEnvProvider(os.Environ),
		Logger:        logger,
		Observer:      observer,
	}

	// Wire MCP proxy when enabled (CLI flag or config).
	mcpEnabled := flags.mcpEnabled
	if !mcpEnabled && cfg != nil && cfg.MCP.Enabled {
		mcpEnabled = true
	}
	if mcpEnabled {
		mcpGroup := flags.mcpGroup
		mcpPort := flags.mcpPort
		mcpConfigPath := flags.mcpConfig
		if cfg != nil {
			if mcpGroup == "default" && cfg.MCP.Group != "" {
				mcpGroup = cfg.MCP.Group
			}
			if mcpPort == 4483 && cfg.MCP.Port != 0 {
				mcpPort = cfg.MCP.Port
			}
			if mcpConfigPath == "" && cfg.MCP.ConfigPath != "" {
				mcpConfigPath = cfg.MCP.ConfigPath
			}
		}
		mcpProvider := inframcp.NewVMCPProvider(mcpGroup, mcpPort, mcpConfigPath, logger, logFile)
		deps.MCPProvider = mcpProvider
		defer func() { _ = mcpProvider.Close() }()
		// Ensure config reflects MCP enabled state for the application layer.
		cfg.MCP.Enabled = true
		cfg.MCP.Group = mcpGroup
		cfg.MCP.Port = mcpPort
		cfg.MCP.ConfigPath = mcpConfigPath
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

	// Validate and parse egress flags.
	if flags.egressProfile != "" && !egress.ProfileName(flags.egressProfile).IsValid() {
		return fmt.Errorf("invalid --egress-profile %q: valid values are %v",
			flags.egressProfile, egress.ValidProfiles())
	}

	var parsedAllowHosts []egress.Host
	for _, h := range flags.allowHosts {
		parsed, parseErr := egress.ParseHostFlag(h)
		if parseErr != nil {
			return fmt.Errorf("invalid --allow-host %q: %w", h, parseErr)
		}
		parsedAllowHosts = append(parsedAllowHosts, parsed)
	}

	if flags.egressProfile != "" {
		logger.Info("egress profile override", "profile", flags.egressProfile)
	}

	runner := app.NewSandboxRunner(deps)

	opts := app.RunOpts{
		CPUs:          flags.cpus,
		Memory:        flags.memory,
		Workspace:     ws,
		SSHPort:       flags.sshPort,
		ImageOverride: flags.image,
		EgressProfile: flags.egressProfile,
		AllowHosts:    parsedAllowHosts,
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
		observer.Start(progress.PhaseCleaning, "Cleaning up...")
		if cleanErr := sb.Cleanup(); cleanErr != nil {
			observer.Fail("Failed to clean up snapshot")
			logger.Error("failed to clean up snapshot", "error", cleanErr)
		} else {
			observer.Complete("Cleaned up snapshot")
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
			result, revErr := reviewer.Review(changes)
			if revErr != nil {
				reviewErr = fmt.Errorf("reviewing changes: %w", revErr)
			} else if len(result.Accepted) > 0 {
				reviewErr = runner.Flush(sb, result.Accepted)
			} else {
				observer.Warn("No changes accepted")
			}
		} else {
			observer.Warn("No workspace changes detected")
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

// openLogFile opens (or creates) the log file, truncating if it exceeds maxLogSize.
// Returns the file, a closer, and any error.
func openLogFile(override string) (*os.File, io.Closer, error) {
	logPath := override
	if logPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, nil, fmt.Errorf("getting home dir: %w", err)
		}
		logDir := filepath.Join(home, defaultLogDir)
		if err := os.MkdirAll(logDir, 0o750); err != nil {
			return nil, nil, fmt.Errorf("creating log dir: %w", err)
		}
		logPath = filepath.Join(logDir, defaultLogFile)
	}

	// Truncate if oversized.
	if info, err := os.Stat(logPath); err == nil && info.Size() > maxLogSize {
		_ = os.Truncate(logPath, 0)
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, nil, err
	}
	return f, f, nil
}

// setupLogger creates a slog.Logger with file + optional stderr handlers.
func setupLogger(logFile *os.File, debug bool) *slog.Logger {
	var handlers []slog.Handler

	// Always log to file if available.
	if logFile != nil {
		handlers = append(handlers, infralogging.NewFileHandler(logFile, slog.LevelDebug))
	}

	// In debug mode, also log to stderr.
	if debug {
		handlers = append(handlers, slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}))
	}

	if len(handlers) == 0 {
		// Fallback: discard all logs.
		return slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if len(handlers) == 1 {
		return slog.New(handlers[0])
	}
	return slog.New(infralogging.NewFanoutHandler(handlers...))
}

// chooseObserver selects the appropriate progress observer for the current environment.
func chooseObserver(terminal *infraterminal.OSTerminal, logger *slog.Logger, debug bool) progress.Observer {
	if debug {
		return infraprogress.NewLogObserver(logger)
	}
	if terminal.IsInteractive() {
		return infraprogress.NewSpinnerObserver(os.Stderr)
	}
	return infraprogress.NewSimpleObserver(os.Stderr)
}
