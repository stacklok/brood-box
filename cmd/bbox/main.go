// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package main is the entrypoint for the bbox CLI.
package main

import (
	"context"
	"crypto/rand"
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
	"time"
	"unicode"

	"github.com/adrg/xdg"
	"github.com/spf13/cobra"
	"github.com/stacklok/go-microvm/extract"
	"github.com/stacklok/go-microvm/image"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	infraagent "github.com/stacklok/brood-box/internal/infra/agent"
	infraconfig "github.com/stacklok/brood-box/internal/infra/config"
	infracredential "github.com/stacklok/brood-box/internal/infra/credential"
	"github.com/stacklok/brood-box/internal/infra/diff"
	"github.com/stacklok/brood-box/internal/infra/exclude"
	infragit "github.com/stacklok/brood-box/internal/infra/git"
	infralogging "github.com/stacklok/brood-box/internal/infra/logging"
	inframcp "github.com/stacklok/brood-box/internal/infra/mcp"
	infraprogress "github.com/stacklok/brood-box/internal/infra/progress"
	"github.com/stacklok/brood-box/internal/infra/review"
	infrasettings "github.com/stacklok/brood-box/internal/infra/settings"
	infrassh "github.com/stacklok/brood-box/internal/infra/ssh"
	infraterminal "github.com/stacklok/brood-box/internal/infra/terminal"
	infratracing "github.com/stacklok/brood-box/internal/infra/tracing"
	infravm "github.com/stacklok/brood-box/internal/infra/vm"
	infraruntime "github.com/stacklok/brood-box/internal/infra/vm/runtimebin"
	infraws "github.com/stacklok/brood-box/internal/infra/workspace"
	"github.com/stacklok/brood-box/internal/version"
	"github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/bytesize"
	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
	"github.com/stacklok/brood-box/pkg/domain/credential"
	"github.com/stacklok/brood-box/pkg/domain/egress"
	"github.com/stacklok/brood-box/pkg/domain/progress"
	"github.com/stacklok/brood-box/pkg/domain/workspace"
	"github.com/stacklok/brood-box/pkg/sandbox"
)

// defaultLogFile is the log file name within the per-VM data directory.
const defaultLogFile = "broodbox.log"

func main() {
	if err := rootCmd().Execute(); err != nil {
		// Cobra won't print the error (SilenceErrors: true), so we do.
		fmt.Fprintf(os.Stderr, "Error: %s\n", err)
		os.Exit(1)
	}
}

func rootCmd() *cobra.Command {
	var (
		cpus              uint32
		memory            string
		wsPath            string
		sshPort           uint16
		cfgPath           string
		image             string
		debug             bool
		review            bool
		excludes          []string
		logFile           string
		egressProfile     string
		allowHosts        []string
		noMCP             bool
		mcpGroup          string
		mcpPort           uint16
		mcpConfig         string
		mcpAuthzProfile   string
		noGitToken        bool
		noGitSSHAgent     bool
		noSaveCredentials bool
		seedCredentials   bool
		tmpSize           string
		noSettings        bool
		noFirmwareDL      bool
		noImageCache      bool
		traceEnabled      bool
		timings           bool
		exec              string
		envForward        []string
	)

	cmd := &cobra.Command{
		Use:   "bbox <agent-name> [flags] [-- <agent-args...>]",
		Short: "Run coding agents in hardware-isolated sandbox VMs",
		Long: `bbox boots a microVM, mounts your workspace, forwards secrets,
and drops into an interactive terminal session with a coding agent.

Workspace snapshot isolation is always active: a COW snapshot is created
before the VM starts, and changes are flushed back after the agent finishes.
Use --review to interactively approve or reject each changed file; without it,
all changes are auto-accepted.

Supported agents: claude-code, codex, opencode

Example:
  bbox claude-code
  bbox codex --cpus 4 --memory 4g
  bbox opencode --workspace /path/to/project
  bbox claude-code --review
  bbox claude-code --review --exclude "*.log" --exclude "tmp/"
  bbox claude-code --egress-profile locked
  bbox claude-code --allow-host "custom-api.example.com:443"
  bbox claude-code --no-mcp
  bbox claude-code --mcp-group "coding-tools"
  bbox claude-code --env-forward ANTHROPIC_API_KEY --env-forward 'OPENCODE_*'
  bbox claude-code -- --help
  bbox claude-code --exec /bin/bash`,
		Args:    cobra.MinimumNArgs(1),
		Version: fmt.Sprintf("%s (%s)", version.Version, version.Commit),
		RunE: func(cmd *cobra.Command, args []string) error {
			commandArgs := []string{}
			if len(args) > 1 {
				commandArgs = args[1:]
			}
			return run(cmd.Context(), args[0], runFlags{
				cpus:              cpus,
				memory:            memory,
				tmpSize:           tmpSize,
				workspace:         wsPath,
				sshPort:           sshPort,
				cfgPath:           cfgPath,
				image:             image,
				debug:             debug,
				review:            review,
				excludes:          excludes,
				logFile:           logFile,
				egressProfile:     egressProfile,
				allowHosts:        allowHosts,
				noMCP:             noMCP,
				mcpGroup:          mcpGroup,
				mcpPort:           mcpPort,
				mcpConfig:         mcpConfig,
				mcpAuthzProfile:   mcpAuthzProfile,
				noGitToken:        noGitToken,
				noGitSSHAgent:     noGitSSHAgent,
				noSaveCredentials: noSaveCredentials,
				seedCredentials:   seedCredentials,
				noSettings:        noSettings,
				noFirmwareDL:      noFirmwareDL,
				noImageCache:      noImageCache,
				traceEnabled:      traceEnabled || os.Getenv("BBOX_TRACE") == "1",
				timings:           timings,
				exec:              exec,
				commandArgs:       commandArgs,
				envForward:        envForward,
			})
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.Flags().Uint32Var(&cpus, "cpus", 0, "Number of vCPUs (0 = agent default)")
	cmd.Flags().StringVar(&memory, "memory", "", "RAM for the VM, e.g. 4g or 512m (empty = agent default)")
	cmd.Flags().StringVar(&tmpSize, "tmp-size", "", "Size of /tmp tmpfs inside the VM, e.g. 512m or 2g (empty = agent default)")
	cmd.Flags().StringVar(&wsPath, "workspace", "", "Workspace directory to mount (default: current directory)")
	cmd.Flags().Uint16Var(&sshPort, "ssh-port", 0, "Host SSH port (0 = auto-pick)")
	cmd.Flags().StringVar(&cfgPath, "config", "", "Config file path (default: ~/.config/broodbox/config.yaml)")
	cmd.Flags().StringVar(&image, "image", "", "Override OCI image reference")
	cmd.Flags().BoolVar(&debug, "debug", false, "Enable debug-level logging to file (default: info level)")
	cmd.Flags().BoolVar(&review, "review", false, "Enable interactive per-file review of workspace changes (snapshot isolation is always active)")
	cmd.Flags().StringSliceVar(&excludes, "exclude", nil, "Additional exclude patterns for workspace snapshot (repeatable)")
	cmd.Flags().StringVar(&logFile, "log-file", "", "Override log file path (default: ~/.config/broodbox/vms/<vm-name>/broodbox.log)")
	cmd.Flags().StringVar(&egressProfile, "egress-profile", "", "Egress restriction level: permissive, standard, locked (default: agent's built-in default)")
	cmd.Flags().StringSliceVar(&allowHosts, "allow-host", nil, "Additional allowed egress DNS hostname[:port] — no IP addresses (repeatable)")
	cmd.Flags().BoolVar(&noMCP, "no-mcp", false, "Disable MCP tool proxy (enabled by default, discovers servers from ToolHive)")
	cmd.Flags().StringVar(&mcpGroup, "mcp-group", "default", "ToolHive group to discover MCP servers from")
	cmd.Flags().Uint16Var(&mcpPort, "mcp-port", 4483, "Port for MCP proxy on VM gateway")
	cmd.Flags().StringVar(&mcpConfig, "mcp-config", "", "Path to MCP config YAML (Cedar policies and aggregation settings)")
	cmd.Flags().StringVar(&mcpAuthzProfile, "mcp-authz-profile", "", "MCP authorization profile: full-access, observe, safe-tools, custom (default: full-access)")
	cmd.Flags().BoolVar(&noGitToken, "no-git-token", false, "Disable forwarding GITHUB_TOKEN/GH_TOKEN into the VM")
	cmd.Flags().BoolVar(&noGitSSHAgent, "no-git-ssh-agent", false, "Disable SSH agent forwarding into the VM")
	cmd.Flags().BoolVar(&noSaveCredentials, "no-save-credentials", false, "Disable saving agent credentials between sessions (enabled by default)")
	cmd.Flags().BoolVar(&seedCredentials, "seed-credentials", false, "Seed agent credentials from host (e.g. macOS Keychain) into the VM")
	cmd.Flags().BoolVar(&noSettings, "no-settings", false, "Disable injecting host agent settings (rules, skills, etc.) into the VM")
	cmd.Flags().BoolVar(&noFirmwareDL, "no-firmware-download", false, "Disable firmware download (use system libkrunfw only)")
	cmd.Flags().BoolVar(&noImageCache, "no-image-cache", false, "Disable OCI image caching (fresh pull every run)")
	cmd.Flags().BoolVar(&traceEnabled, "trace", false, "Enable OpenTelemetry tracing (writes trace.json to VM data dir)")
	cmd.Flags().BoolVar(&timings, "timings", false, "Print per-phase timing summary after run")
	cmd.Flags().StringVar(&exec, "exec", "", "Override the agent command (e.g. /bin/bash for debugging)")
	cmd.Flags().StringSliceVar(&envForward, "env-forward", nil, "Forward host env var into VM (exact name or glob like 'PREFIX_*', repeatable)")

	// Add subcommands.
	cmd.AddCommand(listCmd())
	cmd.AddCommand(authCmd())
	cmd.AddCommand(configCmd())
	cmd.AddCommand(cacheCmd())

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

func authCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage agent authentication",
	}
	cmd.AddCommand(authListCmd())
	cmd.AddCommand(authClearCmd())
	return cmd
}

func authListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List agents with saved credentials",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			baseDir := filepath.Join(xdg.ConfigHome, "broodbox", "agent-state")
			entries, err := os.ReadDir(baseDir)
			if err != nil {
				if os.IsNotExist(err) {
					fmt.Println("No saved credentials")
					return nil
				}
				return fmt.Errorf("reading agent state directory: %w", err)
			}
			var found bool
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				if err := agent.ValidateName(e.Name()); err != nil {
					continue
				}
				fmt.Println(e.Name())
				found = true
			}
			if !found {
				fmt.Println("No saved credentials")
			}
			return nil
		},
	}
}

func authClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear <agent>",
		Short: "Remove saved credentials for an agent",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			agentName := args[0]
			if err := agent.ValidateName(agentName); err != nil {
				return fmt.Errorf("invalid agent name: %w", err)
			}
			stateDir := filepath.Join(xdg.ConfigHome, "broodbox", "agent-state", agentName)
			if err := os.RemoveAll(stateDir); err != nil {
				return fmt.Errorf("removing credentials: %w", err)
			}
			fmt.Printf("Credentials cleared for %s\n", agentName)
			return nil
		},
	}
}

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage Brood Box configuration",
	}
	cmd.AddCommand(configInitCmd())
	return cmd
}

func configInitCmd() *cobra.Command {
	var cfgPath string
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Create a default config file with all options documented",
		Long: `Creates ~/.config/broodbox/config.yaml (or the path given by --config)
with all configuration options documented as YAML comments.

If the file already exists the command fails unless --force is passed.`,
		Args: cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			path := cfgPath
			if path == "" {
				path = infraconfig.NewLoader("").Path()
			}
			if err := infraconfig.WriteDefault(path, force); err != nil {
				return err
			}
			fmt.Printf("Config written to %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", "Config file path (default: ~/.config/broodbox/config.yaml)")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Overwrite the config file if it already exists")
	return cmd
}

type runFlags struct {
	cpus              uint32
	memory            string
	tmpSize           string
	workspace         string
	sshPort           uint16
	cfgPath           string
	image             string
	debug             bool
	review            bool
	excludes          []string
	logFile           string
	egressProfile     string
	allowHosts        []string
	noMCP             bool
	mcpGroup          string
	mcpPort           uint16
	mcpConfig         string
	mcpAuthzProfile   string
	noGitToken        bool
	noGitSSHAgent     bool
	noSaveCredentials bool
	seedCredentials   bool
	noSettings        bool
	noFirmwareDL      bool
	noImageCache      bool
	traceEnabled      bool
	timings           bool
	exec              string
	commandArgs       []string
	envForward        []string
}

func run(parentCtx context.Context, agentName string, flags runFlags) error {
	// Set up signal-aware context.
	ctx, cancel := signal.NotifyContext(parentCtx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Validate agent name before using it in filesystem paths or VM names.
	if err := agent.ValidateName(agentName); err != nil {
		return fmt.Errorf("invalid agent name: %w", err)
	}

	// Resolve workspace early so we can derive a deterministic VM name.
	earlyWs := flags.workspace
	if earlyWs == "" {
		var wdErr error
		earlyWs, wdErr = os.Getwd()
		if wdErr != nil {
			return fmt.Errorf("getting current directory: %w", wdErr)
		}
	}

	// Generate a unique session ID so concurrent sessions on the same
	// workspace get separate VM names and data directories.
	var sessionBytes [4]byte
	if _, err := rand.Read(sessionBytes[:]); err != nil {
		return fmt.Errorf("generating session ID: %w", err)
	}
	sessionID := fmt.Sprintf("%x", sessionBytes)

	// Derive VM name from agent + workspace hash + session ID so concurrent
	// sessions get separate data directories.
	vmName := sandbox.VMName(agentName, earlyWs, sessionID)

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

	// Always show log path since VM names include a random session ID.
	_, _ = fmt.Fprintf(os.Stderr, "Session log: %s\n", logPath)

	// Set up progress observer based on mode.
	terminal := infraterminal.NewOSTerminal(os.Stdin, os.Stdout, os.Stderr)
	observer := chooseObserver(terminal)

	// Wrap with timing observer when --timings is set.
	// The defer is registered here — before the cleanup defer below — so LIFO
	// ordering ensures the summary prints after the cleanup phase completes.
	var timingObs *infraprogress.TimingObserver
	if flags.timings {
		timingObs = infraprogress.NewTimingObserver(observer)
		observer = timingObs
		defer timingObs.Summary(os.Stderr)
	}

	// Set up OpenTelemetry tracing when --trace or BBOX_TRACE=1.
	var tracerProvider *sdktrace.TracerProvider
	if flags.traceEnabled {
		tracePath := filepath.Join(filepath.Dir(logPath), "trace.json")
		tp, tpErr := infratracing.NewProvider(tracePath)
		if tpErr != nil {
			logger.Warn("failed to initialize tracing", "error", tpErr)
		} else {
			tracerProvider = tp
			otel.SetTracerProvider(tp)
			defer infratracing.Shutdown(context.Background(), tp)
			_, _ = fmt.Fprintf(os.Stderr, "Trace output: %s\n", tracePath)
		}
	}

	// Use the workspace resolved above for VM naming.
	ws := earlyWs

	// Resolve snapshot cache directory (XDG cache).
	snapDir, snapDirErr := snapshotCacheDir()
	if snapDirErr != nil {
		return fmt.Errorf("resolving snapshot cache directory: %w", snapDirErr)
	}

	// Clean up stale snapshot dirs from previous crashes.
	infraws.CleanupStaleSnapshots(snapDir, logger)

	// Clean up stale VM log directories from previous crashes.
	if home, homeErr := os.UserHomeDir(); homeErr == nil {
		infravm.CleanupStaleLogs(filepath.Join(home, ".config", "broodbox", "vms"), logger)
	}

	// Build registry with config-based custom agents.
	registry := infraagent.NewRegistry()
	cfgLoader := infraconfig.NewLoader(flags.cfgPath)

	cfg, err := cfgLoader.Load()
	if err != nil {
		logger.Warn("failed to load config, using defaults", "error", err)
	}
	if cfg == nil {
		cfg = &domainconfig.Config{}
	}

	// Load per-workspace config and merge.
	localCfg, err := infraconfig.LoadFromPath(filepath.Join(ws, domainconfig.LocalConfigFile))
	if err != nil {
		logger.Warn("failed to load local config, ignoring", "error", err)
	}
	warnLocalConfigOverrides(os.Stderr, localCfg, cfg)
	cfg = domainconfig.MergeConfigs(cfg, localCfg)
	if cfg == nil {
		cfg = &domainconfig.Config{}
	}

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

	// Determine interactive review mode. Default is disabled unless --review
	// is set or config explicitly enables it. Snapshot isolation is always on.
	interactiveReview := flags.review
	if !interactiveReview && cfg != nil && cfg.Review.Enabled != nil && *cfg.Review.Enabled {
		interactiveReview = true
	}

	// Merge exclude patterns from config and CLI.
	var excludePatterns []string
	if cfg != nil {
		excludePatterns = append(excludePatterns, cfg.Review.ExcludePatterns...)
	}
	excludePatterns = append(excludePatterns, flags.excludes...)

	// Build exclude matchers — always needed since snapshot isolation is always active.
	excludeCfg, err := exclude.LoadExcludeConfig(ws, excludePatterns, logger)
	if err != nil {
		return fmt.Errorf("loading exclude config: %w", err)
	}
	snapshotMatcher := exclude.NewMatcherFromConfig(excludeCfg)

	gitignorePatterns, err := exclude.LoadGitignorePatterns(ws, logger)
	if err != nil {
		logger.Warn("failed to load .gitignore patterns", "error", err)
	}
	diffMatcher := exclude.NewDiffMatcher(excludeCfg, gitignorePatterns)

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
		SettingsImport:   cfg.SettingsImport,
	}

	firmwareDownloadEnabled := true
	if cfg.Runtime.FirmwareDownload != nil {
		firmwareDownloadEnabled = *cfg.Runtime.FirmwareDownload
	}
	if flags.noFirmwareDL {
		firmwareDownloadEnabled = false
	}

	// Wire VM runner options (embedded runtime when available).
	var vmRunnerOpts []infravm.RunnerOption
	var firmwareCachePath string
	if firmwareDownloadEnabled {
		var fwErr error
		firmwareCachePath, fwErr = firmwareCacheDir()
		if fwErr != nil {
			return fmt.Errorf("resolving firmware cache directory: %w", fwErr)
		}
	}
	firmwareRes, fwErr := infravm.ResolveFirmware(ctx, infravm.FirmwareResolveOpts{
		CacheDir:        firmwareCachePath,
		Version:         infraruntime.Version,
		DownloadEnabled: firmwareDownloadEnabled,
		Logger:          logger,
	})
	if fwErr != nil {
		return fmt.Errorf("resolving firmware: %w", fwErr)
	}
	logger.Debug("resolved firmware",
		"source", firmwareRes.Source,
		"dir", firmwareRes.Dir,
		"version", firmwareRes.Version,
		"url", firmwareRes.URL,
	)
	dataDir, dataErr := infravm.VMDataDir(vmName)
	if dataErr != nil {
		return fmt.Errorf("resolving VM data directory: %w", dataErr)
	}
	refPath := filepath.Join(dataDir, "firmware.ref.json")
	if err := infravm.WriteFirmwareReference(refPath, infravm.FirmwareReference{
		Version:   firmwareRes.Version,
		Source:    firmwareRes.Source,
		Path:      firmwareRes.Dir,
		URL:       firmwareRes.URL,
		Timestamp: firmwareRes.Timestamp,
	}); err != nil {
		return fmt.Errorf("writing firmware reference: %w", err)
	}
	vmRunnerOpts = append(vmRunnerOpts, infravm.WithFirmwareSource(extract.Dir(firmwareRes.Dir)))
	if infraruntime.Available() {
		cacheDir, cdErr := runtimeCacheDir()
		if cdErr != nil {
			return fmt.Errorf("resolving runtime cache directory: %w", cdErr)
		}
		vmRunnerOpts = append(vmRunnerOpts,
			infravm.WithRuntimeSource(infraruntime.RuntimeSource()),
			infravm.WithCacheDir(cacheDir),
		)
		logger.Info("using embedded go-microvm runtime", "version", infraruntime.Version)
	}

	// Wire external OCI image cache (unless --no-image-cache is set).
	if !flags.noImageCache {
		imgCacheDir, imgCacheErr := imageCacheDir()
		if imgCacheErr != nil {
			return fmt.Errorf("resolving image cache directory: %w", imgCacheErr)
		}
		vmRunnerOpts = append(vmRunnerOpts, infravm.WithImageCacheDir(imgCacheDir))

		// Best-effort stale cache eviction (entries older than 7 days).
		cache := image.NewCache(imgCacheDir)
		if removed, evictErr := cache.Evict(7 * 24 * time.Hour); evictErr != nil {
			logger.Warn("failed to evict stale image cache entries", "error", evictErr)
		} else if removed > 0 {
			logger.Info("evicted stale image cache entries", "count", removed)
		}
	}

	// Resolve credential persistence setting.
	saveCredentials := cfg.Auth.SaveCredentialsEnabled() && !flags.noSaveCredentials

	// Resolve host credential seeding: opt-in via flag or config.
	seedCreds := flags.seedCredentials || cfg.Auth.SeedHostCredentialsEnabled()

	var credentialStore credential.Store
	if saveCredentials {
		credStateDir := filepath.Join(xdg.ConfigHome, "broodbox", "agent-state")
		fsStore := infracredential.NewFSStore(credStateDir, logger)
		credentialStore = fsStore

		if seedCreds {
			if seeder := credentialSeederForAgent(agentName, logger); seeder != nil {
				if err := seeder.Seed(fsStore); err != nil {
					logger.Warn("credential seeding failed", "agent", agentName, "error", err)
				}
			}
		}
	}

	if credentialStore != nil {
		vmRunnerOpts = append(vmRunnerOpts, infravm.WithCredentialStore(credentialStore))
	}

	// Wire settings injector (enabled by default, --no-settings to disable).
	var settingsInjector *infrasettings.FSInjector
	settingsEnabled := cfg.SettingsImport.IsEnabled() && !flags.noSettings
	if settingsEnabled {
		settingsInjector = infrasettings.NewFSInjector(logger)
		vmRunnerOpts = append(vmRunnerOpts, infravm.WithSettingsInjector(settingsInjector))
	} else if flags.noSettings {
		// Ensure sandbox config reflects disabled state for the application layer.
		disabled := false
		sandboxCfg.SettingsImport.Enabled = &disabled
	}

	// Wire dependencies.
	deps := sandbox.SandboxDeps{
		Registry:         registry,
		VMRunner:         infravm.NewMicroVMRunner(logger, vmRunnerOpts...),
		SessionRunner:    infrassh.NewInteractiveSession(logger),
		Config:           sandboxCfg,
		EnvProvider:      agent.NewOSEnvProvider(os.Environ),
		Logger:           logger,
		Observer:         observer,
		CredentialStore:  credentialStore,
		SettingsInjector: settingsInjector,
	}

	// Validate MCP authz profile flag early (before wiring).
	if flags.mcpAuthzProfile != "" && !domainconfig.IsValidMCPAuthzProfile(flags.mcpAuthzProfile) {
		return fmt.Errorf("invalid --mcp-authz-profile %q: valid values are %v",
			flags.mcpAuthzProfile, domainconfig.ValidMCPAuthzProfiles())
	}

	// Wire MCP proxy (enabled by default, --no-mcp to disable).
	mcpEnabled := !flags.noMCP
	if mcpEnabled && cfg != nil && cfg.MCP.Enabled != nil && !*cfg.MCP.Enabled {
		mcpEnabled = false
	}
	if mcpEnabled {
		mcpGroup := flags.mcpGroup
		mcpPort := flags.mcpPort
		if cfg != nil {
			if mcpGroup == "default" && cfg.MCP.Group != "" {
				mcpGroup = cfg.MCP.Group
			}
			if mcpPort == 4483 && cfg.MCP.Port != 0 {
				mcpPort = cfg.MCP.Port
			}
		}

		// Resolve MCP file config: CLI --mcp-config flag overrides config file.
		var mcpFileConfig *domainconfig.MCPFileConfig
		if flags.mcpConfig != "" {
			var loadErr error
			mcpFileConfig, loadErr = inframcp.LoadMCPFileConfig(flags.mcpConfig)
			if loadErr != nil {
				return loadErr
			}
		} else if cfg != nil && cfg.MCP.Config != nil {
			mcpFileConfig = cfg.MCP.Config
		}

		// Resolve MCP authz config: CLI flag overrides config file.
		var authzCfg *domainconfig.MCPAuthzConfig
		if flags.mcpAuthzProfile != "" {
			authzCfg = &domainconfig.MCPAuthzConfig{Profile: flags.mcpAuthzProfile}
		} else if cfg != nil && cfg.MCP.Authz != nil {
			authzCfg = cfg.MCP.Authz
		}

		// Implicit custom profile: if config has policies but no explicit
		// profile, infer custom so the user doesn't have to specify both.
		if mcpFileConfig != nil && mcpFileConfig.Authz != nil &&
			len(mcpFileConfig.Authz.Policies) > 0 && authzCfg == nil {
			authzCfg = &domainconfig.MCPAuthzConfig{Profile: domainconfig.MCPAuthzProfileCustom}
		}

		// Merge per-agent authz override (tighten-only) before constructing
		// the VMCPProvider singleton.
		if cfg != nil {
			if ao, ok := cfg.Agents[agentName]; ok && ao.MCP != nil && ao.MCP.Authz != nil {
				authzCfg = domainconfig.MergeMCPAuthzConfig(authzCfg, ao.MCP.Authz)
			}
		}

		mcpProvider := inframcp.NewVMCPProvider(mcpGroup, mcpPort, mcpFileConfig, authzCfg, logger, logFile)
		deps.MCPProvider = mcpProvider
		defer func() { _ = mcpProvider.Close() }()
		// Ensure sandbox config reflects MCP enabled state for the application layer.
		enabled := true
		sandboxCfg.MCP.Enabled = &enabled
		sandboxCfg.MCP.Group = mcpGroup
		sandboxCfg.MCP.Port = mcpPort
		sandboxCfg.MCP.Config = mcpFileConfig
		sandboxCfg.MCP.Authz = authzCfg
	}

	// Resolve git config from config + CLI flags.
	gitTokenEnabled := cfg.Git.GitTokenEnabled() && !flags.noGitToken
	sshAgentEnabled := cfg.Git.SSHAgentEnabled() && !flags.noGitSSHAgent

	// Wire git identity provider (unconditional — used for both review and no-review modes).
	deps.GitIdentityProvider = infragit.NewHostIdentityProvider("")

	// Wire snapshot isolation dependencies (always active).
	deps.WorkspaceCloner = infraws.NewFSWorkspaceCloner(
		infraws.NewPlatformCloner(), snapDir, logger,
	)
	if interactiveReview {
		deps.Reviewer = review.NewInteractiveReviewer(os.Stdin, os.Stdout)
	} else {
		deps.Reviewer = review.NewAutoAcceptReviewer(logger, os.Stderr)
	}
	deps.Flusher = review.NewFSFlusher()
	deps.Differ = diff.NewFSDiffer()

	// Wire snapshot post-processors (worktree reconstruction, then git config sanitizer).
	deps.SnapshotPostProcessors = []workspace.SnapshotPostProcessor{
		infragit.NewWorktreeProcessor(logger),
		infragit.NewConfigSanitizer(logger),
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

	var commandOverride []string
	if flags.exec != "" {
		commandOverride = []string{flags.exec}
	}

	// Parse --memory flag (human-readable string → MiB).
	var memoryMiB uint32
	if flags.memory != "" {
		parsed, parseErr := bytesize.ParseByteSize(flags.memory)
		if parseErr != nil {
			return fmt.Errorf("--memory: %w", parseErr)
		}
		memoryMiB = parsed.MiB()
	}

	// Parse --tmp-size flag (human-readable string → MiB).
	var tmpSizeMiB uint32
	if flags.tmpSize != "" {
		parsed, parseErr := bytesize.ParseByteSize(flags.tmpSize)
		if parseErr != nil {
			return fmt.Errorf("--tmp-size: %w", parseErr)
		}
		tmpSizeMiB = parsed.MiB()
	}

	// Enable libkrun trace logging when --debug is set so vm.log
	// captures hypervisor-level diagnostics.
	var logLevel uint32
	if flags.debug {
		logLevel = 5 // trace
	}

	opts := sandbox.RunOpts{
		CPUs:            flags.cpus,
		Memory:          memoryMiB,
		TmpSizeMiB:      tmpSizeMiB,
		Workspace:       ws,
		SSHPort:         flags.sshPort,
		ImageOverride:   flags.image,
		EgressProfile:   flags.egressProfile,
		AllowHosts:      parsedAllowHosts,
		GitTokenEnabled: gitTokenEnabled,
		SSHAgentForward: sshAgentEnabled,
		SSHAuthSock:     os.Getenv("SSH_AUTH_SOCK"),
		SessionID:       sessionID,
		CommandOverride: commandOverride,
		LogLevel:        logLevel,
		CommandArgs:     flags.commandArgs,
		EnvForwardExtra: flags.envForward,
		Snapshot: sandbox.SnapshotOpts{
			Enabled:         true,
			SnapshotMatcher: snapshotMatcher,
			DiffMatcher:     diffMatcher,
		},
	}

	sb, err := runner.Prepare(ctx, agentName, opts)
	if err != nil {
		return err
	}
	defer func() {
		if sb.Snapshot != nil {
			observer.Start(progress.PhaseCleaning, "Cleaning up...")
			if cleanErr := sb.Cleanup(); cleanErr != nil {
				observer.Fail("Failed to clean up snapshot")
				logger.Error("failed to clean up snapshot", "error", cleanErr)
			} else {
				observer.Complete("Cleaned up snapshot")
			}
		}
	}()

	termErr := runner.Attach(ctx, sb, terminal)

	runner.ExtractCredentials(sb)

	if stopErr := runner.Stop(sb); stopErr != nil {
		logger.Error("failed to stop VM", "error", stopErr)
	}

	var reviewErr error
	if sb.Snapshot != nil {
		changes, chErr := runner.Changes(sb)
		if chErr != nil {
			reviewErr = chErr
		} else if len(changes) > 0 {
			result, revErr := deps.Reviewer.Review(changes)
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
		// Propagate the agent's exit code.
		var exitErr *infrassh.ExitError
		if errors.As(err, &exitErr) {
			// Print a diagnostic for unexpected terminations (OOM, crash, etc.)
			// but stay quiet for normal exits and user-initiated signals.
			if hint := exitErr.SignalHint(); hint != "" {
				_, _ = fmt.Fprintf(os.Stderr, "\nSession ended unexpectedly: %s\n", hint)
			}
			// os.Exit bypasses defers, so clean up snapshot and flush
			// the timing summary now.
			if sb.Snapshot != nil {
				if cleanErr := sb.Cleanup(); cleanErr != nil {
					logger.Error("failed to clean up snapshot", "error", cleanErr)
				}
			}
			if timingObs != nil {
				timingObs.Summary(os.Stderr)
			}
			infratracing.Shutdown(context.Background(), tracerProvider)
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
// at ~/.config/broodbox/vms/<vmName>/broodbox.log.
// Returns the resolved path, the file, a closer, and any error.
func openLogFile(override, vmName string) (string, *os.File, io.Closer, error) {
	logPath := override
	if logPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", nil, nil, fmt.Errorf("getting home dir: %w", err)
		}
		logDir := filepath.Join(home, ".config", "broodbox", "vms", vmName)
		if err := os.MkdirAll(logDir, 0o700); err != nil {
			return "", nil, nil, fmt.Errorf("creating log dir: %w", err)
		}
		// Write PID sentinel to mark ownership so stale cleanup can
		// identify directories from dead processes.
		if err := infravm.WriteSentinel(logDir); err != nil {
			return "", nil, nil, err
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
		warnings = append(warnings, "review.enabled (interactive review) is ignored for security — use --review or global config")
	}

	// Auth.SaveCredentials — always ignored for security, warn if set.
	if localCfg.Auth.SaveCredentials != nil {
		warnings = append(warnings, "auth.save_credentials is ignored in workspace config — use --no-save-credentials flag or global config")
	}

	// Auth.SeedHostCredentials — always ignored for security, warn if set.
	if localCfg.Auth.SeedHostCredentials != nil {
		warnings = append(warnings, "auth.seed_host_credentials is ignored in workspace config — use --seed-credentials flag or global config")
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
			effectiveGlobal = egress.ProfilePermissive
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
			warnings = append(warnings, fmt.Sprintf("sets default memory: %s (clamped to %s)",
				localCfg.Defaults.Memory, domainconfig.MaxMemory))
		} else {
			warnings = append(warnings, fmt.Sprintf("sets default memory: %s", localCfg.Defaults.Memory))
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

	// Runtime — firmware download preference (local cannot re-enable if global disables).
	if localCfg.Runtime.FirmwareDownload != nil {
		if *localCfg.Runtime.FirmwareDownload &&
			globalCfg.Runtime.FirmwareDownload != nil && !*globalCfg.Runtime.FirmwareDownload {
			warnings = append(warnings, "firmware download re-enable is ignored — global config disables it")
		} else {
			warnings = append(warnings, fmt.Sprintf("sets firmware download: %t", *localCfg.Runtime.FirmwareDownload))
		}
	}

	// SettingsImport — local can only disable (tighten).
	if localCfg.SettingsImport.Enabled != nil {
		if *localCfg.SettingsImport.Enabled {
			warnings = append(warnings, "settings_import.enabled=true is ignored — local config can only disable settings import")
		} else {
			warnings = append(warnings, "disables agent settings import (rules, skills, etc.)")
		}
	}
	if localCfg.SettingsImport.Categories != nil {
		warnings = append(warnings, "modifies settings import categories (can only disable)")
	}

	// MCP authz profile — local can only tighten (tighten-only merge).
	// "custom" from local config is silently ignored by merge, but we still warn.
	if localCfg.MCP.Authz != nil && localCfg.MCP.Authz.Profile != "" {
		profile := sanitizeValue(localCfg.MCP.Authz.Profile)
		if localCfg.MCP.Authz.Profile == domainconfig.MCPAuthzProfileCustom {
			warnings = append(warnings, fmt.Sprintf("MCP authz profile %q is ignored — custom profiles cannot be set from workspace config", profile))
		} else {
			warnings = append(warnings, fmt.Sprintf("sets MCP authz profile: %s (can only tighten, not widen)", profile))
		}
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
				warnings = append(warnings, fmt.Sprintf("sets %s memory: %s (clamped to %s)",
					safeName, override.Memory, domainconfig.MaxMemory))
			} else {
				warnings = append(warnings, fmt.Sprintf("sets %s memory: %s", safeName, override.Memory))
			}
		}
		if override.MCP != nil {
			if override.MCP.Enabled != nil {
				warnings = append(warnings, fmt.Sprintf("sets %s MCP enabled: %t", safeName, *override.MCP.Enabled))
			}
			if override.MCP.Authz != nil && override.MCP.Authz.Profile != "" {
				profile := sanitizeValue(override.MCP.Authz.Profile)
				if override.MCP.Authz.Profile == domainconfig.MCPAuthzProfileCustom {
					warnings = append(warnings, fmt.Sprintf("%s MCP authz profile %q is ignored — custom profiles cannot be set from workspace config", safeName, profile))
				} else {
					warnings = append(warnings, fmt.Sprintf("sets %s MCP authz profile: %s (can only tighten, not widen)", safeName, profile))
				}
			}
		}
	}

	if len(warnings) == 0 {
		return
	}

	_, _ = fmt.Fprintf(w, "\n")
	_, _ = fmt.Fprintf(w, "Security: .broodbox.yaml in this workspace modifies sandbox settings:\n")
	for _, msg := range warnings {
		_, _ = fmt.Fprintf(w, "  - %s\n", msg)
	}
	_, _ = fmt.Fprintf(w, "Review .broodbox.yaml before proceeding if this is unexpected.\n")
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

// credentialSeederForAgent returns a Seeder for the given agent,
// or nil if no seeder is available.
func credentialSeederForAgent(name string, logger *slog.Logger) credential.Seeder {
	switch name {
	case "claude-code":
		return infracredential.NewClaudeCodeSeeder(logger)
	default:
		return nil
	}
}

// runtimeCacheDir returns the directory used for extracting embedded runtime
// binaries. Follows XDG_CACHE_HOME, defaulting to ~/.cache/broodbox/runtime/.
func runtimeCacheDir() (string, error) {
	cacheBase := xdg.CacheHome
	if cacheBase == "" {
		return "", errors.New("xdg cache home is empty")
	}
	return filepath.Join(cacheBase, "broodbox", "runtime"), nil
}

// firmwareCacheDir returns the directory used for caching libkrunfw artifacts.
// Follows XDG_CACHE_HOME, defaulting to ~/.cache/broodbox/firmware/.
func firmwareCacheDir() (string, error) {
	cacheBase := xdg.CacheHome
	if cacheBase == "" {
		return "", errors.New("xdg cache home is empty")
	}
	return filepath.Join(cacheBase, "broodbox", "firmware"), nil
}

// snapshotCacheDir returns the directory used for workspace snapshot temp dirs.
// Follows XDG_CACHE_HOME, defaulting to ~/.cache/broodbox/snapshots/.
func snapshotCacheDir() (string, error) {
	cacheBase := xdg.CacheHome
	if cacheBase == "" {
		return "", errors.New("xdg cache home is empty")
	}
	return filepath.Join(cacheBase, "broodbox", "snapshots"), nil
}

// imageCacheDir returns the directory used for caching extracted OCI rootfs
// images. Follows XDG_CACHE_HOME, defaulting to ~/.cache/broodbox/images/.
func imageCacheDir() (string, error) {
	cacheBase := xdg.CacheHome
	if cacheBase == "" {
		return "", errors.New("xdg cache home is empty")
	}
	return filepath.Join(cacheBase, "broodbox", "images"), nil
}
