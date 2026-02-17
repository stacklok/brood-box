// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package main is the entrypoint for the apiary CLI.
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

	"github.com/stacklok/apiary/internal/app"
	"github.com/stacklok/apiary/internal/domain/agent"
	domainconfig "github.com/stacklok/apiary/internal/domain/config"
	"github.com/stacklok/apiary/internal/domain/egress"
	"github.com/stacklok/apiary/internal/domain/progress"
	"github.com/stacklok/apiary/internal/domain/snapshot"
	"github.com/stacklok/apiary/internal/domain/workspace"
	infraagent "github.com/stacklok/apiary/internal/infra/agent"
	infraconfig "github.com/stacklok/apiary/internal/infra/config"
	"github.com/stacklok/apiary/internal/infra/diff"
	"github.com/stacklok/apiary/internal/infra/exclude"
	infragit "github.com/stacklok/apiary/internal/infra/git"
	infralogging "github.com/stacklok/apiary/internal/infra/logging"
	inframcp "github.com/stacklok/apiary/internal/infra/mcp"
	infraprogress "github.com/stacklok/apiary/internal/infra/progress"
	"github.com/stacklok/apiary/internal/infra/review"
	infrassh "github.com/stacklok/apiary/internal/infra/ssh"
	infraterminal "github.com/stacklok/apiary/internal/infra/terminal"
	infravm "github.com/stacklok/apiary/internal/infra/vm"
	infraws "github.com/stacklok/apiary/internal/infra/workspace"
	"github.com/stacklok/apiary/internal/version"
)

// defaultLogFile is the log file name within the per-VM data directory.
const defaultLogFile = "apiary.log"

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
		noMCP         bool
		mcpGroup      string
		mcpPort       uint16
		mcpConfig     string
		noGitToken    bool
		noGitSSHAgent bool
	)

	cmd := &cobra.Command{
		Use:   "apiary <agent-name>",
		Short: "Run coding agents in hardware-isolated sandbox VMs",
		Long: `apiary boots a microVM, mounts your workspace, forwards secrets,
and drops into an interactive terminal session with a coding agent.

By default, the workspace is mounted as a COW snapshot. After the agent
finishes, you review changes per-file before they touch the real workspace.
Use --no-review to disable snapshot isolation and mount the workspace directly.

Supported agents: claude-code, codex, opencode

Example:
  apiary claude-code
  apiary codex --cpus 4 --memory 4096
  apiary opencode --workspace /path/to/project
  apiary claude-code --no-review
  apiary claude-code --exclude "*.log" --exclude "tmp/"
  apiary claude-code --egress-profile locked
  apiary claude-code --allow-host "custom-api.example.com:443"
  apiary claude-code --no-mcp
  apiary claude-code --mcp-group "coding-tools"`,
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
				noMCP:         noMCP,
				mcpGroup:      mcpGroup,
				mcpPort:       mcpPort,
				mcpConfig:     mcpConfig,
				noGitToken:    noGitToken,
				noGitSSHAgent: noGitSSHAgent,
			})
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.Flags().Uint32Var(&cpus, "cpus", 0, "Number of vCPUs (0 = agent default)")
	cmd.Flags().Uint32Var(&memory, "memory", 0, "RAM in MiB (0 = agent default)")
	cmd.Flags().StringVar(&wsPath, "workspace", "", "Workspace directory to mount (default: current directory)")
	cmd.Flags().Uint16Var(&sshPort, "ssh-port", 0, "Host SSH port (0 = auto-pick)")
	cmd.Flags().StringVar(&cfgPath, "config", "", "Config file path (default: ~/.config/apiary/config.yaml)")
	cmd.Flags().StringVar(&image, "image", "", "Override OCI image reference")
	cmd.Flags().BoolVar(&debug, "debug", false, "Enable debug-level logging to file (default: info level)")
	cmd.Flags().BoolVar(&noReview, "no-review", false, "Disable workspace snapshot isolation (mount workspace directly)")
	cmd.Flags().StringSliceVar(&excludes, "exclude", nil, "Additional exclude patterns for workspace snapshot (repeatable)")
	cmd.Flags().StringVar(&logFile, "log-file", "", "Override log file path (default: ~/.config/apiary/vms/<vm-name>/apiary.log)")
	cmd.Flags().StringVar(&egressProfile, "egress-profile", "", "Egress restriction level: permissive, standard, locked (default: agent's built-in default)")
	cmd.Flags().StringSliceVar(&allowHosts, "allow-host", nil, "Additional allowed egress host, format: hostname[:port] (repeatable)")
	cmd.Flags().BoolVar(&noMCP, "no-mcp", false, "Disable MCP tool proxy (enabled by default, discovers servers from ToolHive)")
	cmd.Flags().StringVar(&mcpGroup, "mcp-group", "default", "ToolHive group to discover MCP servers from")
	cmd.Flags().Uint16Var(&mcpPort, "mcp-port", 4483, "Port for MCP proxy on VM gateway")
	cmd.Flags().StringVar(&mcpConfig, "mcp-config", "", "Path to custom vmcp config YAML")
	cmd.Flags().BoolVar(&noGitToken, "no-git-token", false, "Disable forwarding GITHUB_TOKEN/GH_TOKEN into the VM")
	cmd.Flags().BoolVar(&noGitSSHAgent, "no-git-ssh-agent", false, "Disable SSH agent forwarding into the VM")

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
	noMCP         bool
	mcpGroup      string
	mcpPort       uint16
	mcpConfig     string
	noGitToken    bool
	noGitSSHAgent bool
}

func run(parentCtx context.Context, agentName string, flags runFlags) error {
	// Set up signal-aware context.
	ctx, cancel := signal.NotifyContext(parentCtx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Derive VM name early so logs land in the per-VM directory.
	vmName := "sandbox-" + agentName

	// Set up logging: always write to file, debug mode enables DEBUG level.
	logPath, logFile, logCloser, err := openLogFile(flags.logFile, vmName)
	if err != nil {
		// Non-fatal: fall back to discard logging.
		_, _ = fmt.Fprintf(os.Stderr, "Warning: could not open log file: %s\n", err)
	}
	if logCloser != nil {
		defer func() { _ = logCloser.Close() }()
	}

	logger := setupLogger(logFile, flags.debug).With("vm", vmName)
	slog.SetDefault(logger)

	if flags.debug {
		_, _ = fmt.Fprintf(os.Stderr, "Debug logs: %s\n", logPath)
	}

	// Set up progress observer based on mode.
	terminal := infraterminal.NewOSTerminal(os.Stdin, os.Stdout, os.Stderr)
	observer := chooseObserver(terminal)

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

	// Wire MCP proxy (enabled by default, --no-mcp to disable).
	mcpEnabled := !flags.noMCP
	if mcpEnabled && cfg != nil && !cfg.MCP.Enabled {
		mcpEnabled = false
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

	// Resolve git config from config + CLI flags.
	gitTokenEnabled := cfg.Git.GitTokenEnabled() && !flags.noGitToken
	sshAgentEnabled := cfg.Git.SSHAgentEnabled() && !flags.noGitSSHAgent

	// Wire git identity provider (unconditional — used for both review and no-review modes).
	deps.GitIdentityProvider = infragit.NewHostIdentityProvider("")

	// Wire snapshot isolation dependencies only when review is enabled.
	if reviewEnabled {
		deps.WorkspaceCloner = infraws.NewFSWorkspaceCloner(
			infraws.NewPlatformCloner(), logger,
		)
		reviewer = review.NewInteractiveReviewer(os.Stdin, os.Stdout)
		deps.Reviewer = reviewer
		deps.Flusher = review.NewFSFlusher()
		deps.Differ = diff.NewFSDiffer()

		// Wire snapshot post-processors (git config sanitizer).
		deps.SnapshotPostProcessors = []workspace.SnapshotPostProcessor{
			infragit.NewConfigSanitizer(logger),
		}
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
		CPUs:            flags.cpus,
		Memory:          flags.memory,
		Workspace:       ws,
		SSHPort:         flags.sshPort,
		ImageOverride:   flags.image,
		EgressProfile:   flags.egressProfile,
		AllowHosts:      parsedAllowHosts,
		GitTokenEnabled: gitTokenEnabled,
		SSHAgentForward: sshAgentEnabled,
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

	termErr := runner.Attach(ctx, sb, terminal)

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
// When no override is given, the log is placed in the per-VM data directory
// at ~/.config/apiary/vms/<vmName>/apiary.log.
// Returns the resolved path, the file, a closer, and any error.
func openLogFile(override, vmName string) (string, *os.File, io.Closer, error) {
	logPath := override
	if logPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", nil, nil, fmt.Errorf("getting home dir: %w", err)
		}
		logDir := filepath.Join(home, ".config", "apiary", "vms", vmName)
		if err := os.MkdirAll(logDir, 0o750); err != nil {
			return "", nil, nil, fmt.Errorf("creating log dir: %w", err)
		}
		logPath = filepath.Join(logDir, defaultLogFile)
	}

	// Truncate if oversized.
	if info, err := os.Stat(logPath); err == nil && info.Size() > maxLogSize {
		_ = os.Truncate(logPath, 0)
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return logPath, nil, nil, err
	}
	return logPath, f, f, nil
}

// setupLogger creates a slog.Logger that writes to a log file.
// In debug mode the file captures DEBUG-level messages; otherwise only INFO and above.
func setupLogger(logFile *os.File, debug bool) *slog.Logger {
	if logFile == nil {
		return slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	return slog.New(infralogging.NewFileHandler(logFile, level))
}

// chooseObserver selects the appropriate progress observer for the current environment.
func chooseObserver(terminal *infraterminal.OSTerminal) progress.Observer {
	if terminal.IsInteractive() {
		return infraprogress.NewSpinnerObserver(os.Stderr)
	}
	return infraprogress.NewSimpleObserver(os.Stderr)
}
