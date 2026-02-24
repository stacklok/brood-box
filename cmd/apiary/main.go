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
	"maps"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"unicode"

	"github.com/spf13/cobra"

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
	"github.com/stacklok/apiary/pkg/domain/agent"
	domainconfig "github.com/stacklok/apiary/pkg/domain/config"
	"github.com/stacklok/apiary/pkg/domain/egress"
	"github.com/stacklok/apiary/pkg/domain/progress"
	"github.com/stacklok/apiary/pkg/domain/snapshot"
	"github.com/stacklok/apiary/pkg/domain/workspace"
	"github.com/stacklok/apiary/pkg/sandbox"
)

// defaultLogFile is the log file name within the per-VM data directory.
const defaultLogFile = "apiary.log"

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
		Use:   "apiary <agent-name> [flags] [-- <agent-args...>]",
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
  apiary claude-code --mcp-group "coding-tools"
  apiary claude-code -- --help`,
		Args:    cobra.MinimumNArgs(1),
		Version: fmt.Sprintf("%s (%s)", version.Version, version.Commit),
		RunE: func(cmd *cobra.Command, args []string) error {
			commandArgs := []string{}
			if len(args) > 1 {
				commandArgs = args[1:]
			}
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
				commandArgs:   commandArgs,
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
	cmd.Flags().StringSliceVar(&allowHosts, "allow-host", nil, "Additional allowed egress DNS hostname[:port] — no IP addresses (repeatable)")
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
	commandArgs   []string
}

func run(parentCtx context.Context, agentName string, flags runFlags) error {
	// Set up signal-aware context.
	ctx, cancel := signal.NotifyContext(parentCtx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Validate agent name before using it in filesystem paths or VM names.
	if err := agent.ValidateName(agentName); err != nil {
		return fmt.Errorf("invalid agent name: %w", err)
	}

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
	warnLocalConfigOverrides(os.Stderr, localCfg, cfg)
	cfg = domainconfig.MergeConfigs(cfg, localCfg)

	if cfg.Agents != nil {
		// Register custom agents from config (only those not already built-in).
		// Warnings use fmt.Fprintf(os.Stderr) instead of slog because the logger
		// writes to a file — users would not see invalid agent name warnings
		// unless they manually inspected the log.
		for name, override := range cfg.Agents {
			if err := agent.ValidateName(name); err != nil {
				_, _ = fmt.Fprintf(os.Stderr, "Warning: skipping custom agent %q: %s\n", name, err)
				continue
			}
			if _, err := registry.Get(name); err != nil {
				// Not a built-in agent — register as custom if it has an image.
				if override.Image != "" {
					if addErr := registry.Add(agent.Agent{
						Name:          name,
						Image:         override.Image,
						Command:       override.Command,
						EnvForward:    override.EnvForward,
						DefaultCPUs:   override.CPUs,
						DefaultMemory: override.Memory,
					}); addErr != nil {
						_, _ = fmt.Fprintf(os.Stderr, "Warning: skipping custom agent %q: %s\n", name, addErr)
					}
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

	// Warn if review is disabled and git config contains credentials.
	if !reviewEnabled {
		gitConfigPath := filepath.Join(ws, ".git", "config")
		if hasCreds, credErr := infragit.ContainsCredentials(gitConfigPath); credErr != nil {
			logger.Warn("failed to check git config for credentials", "error", credErr)
		} else if hasCreds {
			_, _ = fmt.Fprintf(os.Stderr, "\nSecurity: .git/config contains credentials that will be exposed inside the VM.\n")
			_, _ = fmt.Fprintf(os.Stderr, "  Snapshot isolation is disabled, so git credential sanitization is skipped.\n")
			_, _ = fmt.Fprintf(os.Stderr, "  Consider enabling review mode to enable credential sanitization.\n\n")
		}
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

	// Validate and convert config-file egress hosts.
	configEgressHosts, egressErr := domainconfig.ToEgressHosts(cfg.Network.AllowHosts)
	if egressErr != nil {
		return fmt.Errorf("config network.%w", egressErr)
	}

	// Build SandboxConfig from the loaded config.
	sandboxCfg := &sandbox.SandboxConfig{
		Defaults:         cfg.Defaults,
		AgentOverrides:   cfg.Agents,
		ExtraEgressHosts: configEgressHosts,
		MCP:              cfg.MCP,
	}

	// Wire dependencies.
	var reviewer *review.InteractiveReviewer
	deps := sandbox.SandboxDeps{
		Registry:      registry,
		VMRunner:      infravm.NewPropolisRunner(logger),
		SessionRunner: infrassh.NewInteractiveSession(logger),
		Config:        sandboxCfg,
		EnvProvider:   agent.NewOSEnvProvider(os.Environ),
		Logger:        logger,
		Observer:      observer,
	}

	// Wire MCP proxy (enabled by default, --no-mcp to disable).
	mcpEnabled := !flags.noMCP
	if mcpEnabled && cfg != nil && cfg.MCP.Enabled != nil && !*cfg.MCP.Enabled {
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
		// Ensure sandbox config reflects MCP enabled state for the application layer.
		enabled := true
		sandboxCfg.MCP.Enabled = &enabled
		sandboxCfg.MCP.Group = mcpGroup
		sandboxCfg.MCP.Port = mcpPort
		sandboxCfg.MCP.ConfigPath = mcpConfigPath
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
			return fmt.Errorf("--allow-host %q: %w", h, parseErr)
		}
		parsedAllowHosts = append(parsedAllowHosts, parsed)
	}

	if flags.egressProfile != "" {
		logger.Info("egress profile override", "profile", flags.egressProfile)
	}

	runner := sandbox.NewSandboxRunner(deps)

	opts := sandbox.RunOpts{
		CPUs:            flags.cpus,
		Memory:          flags.memory,
		Workspace:       ws,
		SSHPort:         flags.sshPort,
		ImageOverride:   flags.image,
		EgressProfile:   flags.egressProfile,
		AllowHosts:      parsedAllowHosts,
		GitTokenEnabled: gitTokenEnabled,
		SSHAgentForward: sshAgentEnabled,
		CommandArgs:     flags.commandArgs,
		Snapshot: sandbox.SnapshotOpts{
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

// openLogFile opens (or creates) the log file, truncating it each session.
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
		if err := os.MkdirAll(logDir, 0o700); err != nil {
			return "", nil, nil, fmt.Errorf("creating log dir: %w", err)
		}
		logPath = filepath.Join(logDir, defaultLogFile)
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
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

// warnLocalConfigOverrides prints warnings to w for each security-sensitive
// field set in the local per-workspace config. This makes supply-chain-style
// overrides visible to the user without blocking execution.
//
// globalCfg is used for context (e.g. comparing egress profile strictness)
// but is never modified. Both configs are inspected before merge.
func warnLocalConfigOverrides(w io.Writer, localCfg, globalCfg *domainconfig.Config) {
	if localCfg == nil {
		return
	}

	var warnings []string

	// Review.Enabled — always ignored for security, warn if set.
	if localCfg.Review.Enabled != nil {
		warnings = append(warnings, "review.enabled is ignored for security — use --no-review or global config")
	}

	// Review.ExcludePatterns — can hide changes from diff review.
	if len(localCfg.Review.ExcludePatterns) > 0 {
		warnings = append(warnings, fmt.Sprintf("adds review exclude patterns: %s",
			strings.Join(sanitizeAll(localCfg.Review.ExcludePatterns), ", ")))
	}

	// Defaults.EgressProfile — local can only tighten.
	if localCfg.Defaults.EgressProfile != "" {
		effectiveGlobal := egress.ProfileName(globalCfg.Defaults.EgressProfile)
		if effectiveGlobal == "" {
			effectiveGlobal = egress.ProfileStandard
		}
		localProfile := egress.ProfileName(localCfg.Defaults.EgressProfile)
		if localProfile.IsValid() && egress.Stricter(effectiveGlobal, localProfile) == effectiveGlobal {
			warnings = append(warnings, fmt.Sprintf("default egress profile %q cannot widen %q — ignored",
				sanitizeValue(string(localProfile)), sanitizeValue(string(effectiveGlobal))))
		} else {
			warnings = append(warnings, fmt.Sprintf("sets default egress profile: %s",
				sanitizeValue(localCfg.Defaults.EgressProfile)))
		}
	}

	// Defaults.CPUs / Memory — resource overrides.
	if localCfg.Defaults.CPUs > 0 {
		if localCfg.Defaults.CPUs > domainconfig.MaxCPUs {
			warnings = append(warnings, fmt.Sprintf("sets default CPUs: %d (clamped to %d)",
				localCfg.Defaults.CPUs, domainconfig.MaxCPUs))
		} else {
			warnings = append(warnings, fmt.Sprintf("sets default CPUs: %d", localCfg.Defaults.CPUs))
		}
	}
	if localCfg.Defaults.Memory > 0 {
		if localCfg.Defaults.Memory > domainconfig.MaxMemory {
			warnings = append(warnings, fmt.Sprintf("sets default memory: %d MiB (clamped to %d MiB)",
				localCfg.Defaults.Memory, domainconfig.MaxMemory))
		} else {
			warnings = append(warnings, fmt.Sprintf("sets default memory: %d MiB", localCfg.Defaults.Memory))
		}
	}

	// Network.AllowHosts — extra egress destinations.
	if len(localCfg.Network.AllowHosts) > 0 {
		names := make([]string, len(localCfg.Network.AllowHosts))
		for i, h := range localCfg.Network.AllowHosts {
			names[i] = sanitizeValue(h.Name)
		}
		warnings = append(warnings, fmt.Sprintf("adds egress hosts: %s", strings.Join(names, ", ")))
	}

	// Git — tighten-only but still worth surfacing.
	if localCfg.Git.ForwardToken != nil {
		warnings = append(warnings, fmt.Sprintf("sets git token forwarding: %t", *localCfg.Git.ForwardToken))
	}
	if localCfg.Git.ForwardSSHAgent != nil {
		warnings = append(warnings, fmt.Sprintf("sets git SSH agent forwarding: %t", *localCfg.Git.ForwardSSHAgent))
	}

	// Per-agent overrides — sorted for deterministic output.
	for _, name := range slices.Sorted(maps.Keys(localCfg.Agents)) {
		safeName := sanitizeValue(name)
		override := localCfg.Agents[name]
		if override.Image != "" {
			warnings = append(warnings, fmt.Sprintf("overrides %s image: %s", safeName, sanitizeValue(override.Image)))
		}
		if len(override.Command) > 0 {
			warnings = append(warnings, fmt.Sprintf("overrides %s command", safeName))
		}
		if len(override.EnvForward) > 0 {
			warnings = append(warnings, fmt.Sprintf("overrides %s env forwarding", safeName))
		}
		if len(override.AllowHosts) > 0 {
			names := make([]string, len(override.AllowHosts))
			for i, h := range override.AllowHosts {
				names[i] = sanitizeValue(h.Name)
			}
			warnings = append(warnings, fmt.Sprintf("adds %s egress hosts: %s", safeName, strings.Join(names, ", ")))
		}
		if override.EgressProfile != "" {
			warnings = append(warnings, fmt.Sprintf("sets %s egress profile: %s", safeName, sanitizeValue(override.EgressProfile)))
		}
		if override.CPUs > 0 {
			if override.CPUs > domainconfig.MaxCPUs {
				warnings = append(warnings, fmt.Sprintf("sets %s CPUs: %d (clamped to %d)",
					safeName, override.CPUs, domainconfig.MaxCPUs))
			} else {
				warnings = append(warnings, fmt.Sprintf("sets %s CPUs: %d", safeName, override.CPUs))
			}
		}
		if override.Memory > 0 {
			if override.Memory > domainconfig.MaxMemory {
				warnings = append(warnings, fmt.Sprintf("sets %s memory: %d MiB (clamped to %d MiB)",
					safeName, override.Memory, domainconfig.MaxMemory))
			} else {
				warnings = append(warnings, fmt.Sprintf("sets %s memory: %d MiB", safeName, override.Memory))
			}
		}
		if override.MCP != nil {
			if override.MCP.Enabled != nil {
				warnings = append(warnings, fmt.Sprintf("sets %s MCP enabled: %t", safeName, *override.MCP.Enabled))
			}
			if override.MCP.Group != "" {
				warnings = append(warnings, fmt.Sprintf("sets %s MCP group: %s", safeName, sanitizeValue(override.MCP.Group)))
			}
			if override.MCP.Port != 0 {
				warnings = append(warnings, fmt.Sprintf("sets %s MCP port: %d", safeName, override.MCP.Port))
			}
			if override.MCP.ConfigPath != "" {
				warnings = append(warnings, fmt.Sprintf("sets %s MCP config path: %s", safeName, sanitizeValue(override.MCP.ConfigPath)))
			}
		}
	}

	if len(warnings) == 0 {
		return
	}

	_, _ = fmt.Fprintf(w, "\n")
	_, _ = fmt.Fprintf(w, "Security: .apiary.yaml in this workspace modifies sandbox settings:\n")
	for _, msg := range warnings {
		_, _ = fmt.Fprintf(w, "  - %s\n", msg)
	}
	_, _ = fmt.Fprintf(w, "Review .apiary.yaml before proceeding if this is unexpected.\n")
	_, _ = fmt.Fprintf(w, "\n")
}

// sanitizeValue strips control characters (including ANSI escape sequences)
// from a string before it is printed to the terminal.
func sanitizeValue(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

// sanitizeAll applies sanitizeValue to every element in a slice.
func sanitizeAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = sanitizeValue(s)
	}
	return out
}
