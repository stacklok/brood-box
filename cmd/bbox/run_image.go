// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	infraagent "github.com/stacklok/brood-box/internal/infra/agent"
	"github.com/stacklok/brood-box/internal/infra/exclude"
	infraprogress "github.com/stacklok/brood-box/internal/infra/progress"
	infraterminal "github.com/stacklok/brood-box/internal/infra/terminal"
	infratracing "github.com/stacklok/brood-box/internal/infra/tracing"
	infravm "github.com/stacklok/brood-box/internal/infra/vm"
	infraws "github.com/stacklok/brood-box/internal/infra/workspace"
	"github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/bytesize"
	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
	"github.com/stacklok/brood-box/pkg/domain/egress"
	"github.com/stacklok/brood-box/pkg/domain/snapshot"
	"github.com/stacklok/brood-box/pkg/sandbox"
)

// runImageFlags holds the flag values for `bbox run-image`. It is a subset of
// runFlags tailored to the ephemeral one-shot UX: MCP defaults to OFF (opt-in
// via --mcp), env forwarding defaults to EMPTY, and credential/settings
// persistence is OFF. The fields map directly onto runFlags when the shared
// runSandbox tail is invoked.
type runImageFlags struct {
	name            string
	envForward      []string
	memory          string
	cpus            uint32
	tmpSize         string
	mcp             bool
	mcpAuthzProfile string
	mcpGroup        string
	mcpPort         uint16
	mcpConfig       string
	mcpSessionTTL   time.Duration
	egressProfile   string
	allowHosts      []string
	workspace       string
	workspaceMode   string
	review          bool
	excludes        []string
	yesAckDirect    bool
	sshPort         uint16
	ports           []string
	cfgPath         string
	debug           bool
	logFile         string
	noFirmwareDL    bool
	noImageCache    bool
	pull            string
	traceEnabled    bool
	timings         bool
	// gitToken / gitSSHAgent are opt-in (default false): the inverse of the
	// root command's --no-git-token / --no-git-ssh-agent. run-image forwards
	// neither the git token nor the SSH agent into an arbitrary image unless
	// explicitly requested (ephemeral, safer-by-default posture).
	gitToken        bool
	gitSSHAgent     bool
	seedCredentials bool
}

// runImageCmd builds the `bbox run-image` subcommand. It boots an arbitrary
// OCI image in a sandbox VM, runs the supplied command, forwards only
// explicitly-requested env, and persists nothing — an ephemeral one-shot with
// safer-than-custom defaults. The image is the first positional arg; the
// command (required) follows a literal `--`.
func runImageCmd() *cobra.Command {
	var (
		f         runImageFlags
		envFwd    []string
		envFwdAlt []string
	)
	cmd := &cobra.Command{
		Use:   "run-image IMAGE [flags] -- CMD [args...]",
		Short: "Run an arbitrary OCI image as an ephemeral sandbox one-shot",
		Long: `bbox run-image boots an arbitrary OCI image in a hardware-isolated
microVM, runs CMD inside it, and persists nothing. It builds an in-memory
agent from the flags (no config-file mutation, no registry persistence) and
runs it through the normal sandbox path.

Ephemeral defaults (safer than a custom agent):
  - credential persistence OFF
  - host settings import OFF
  - env forwarding EMPTY (forward nothing) — opt in with --env
  - git token / SSH agent OFF — opt in with --git-token / --git-ssh-agent
  - MCP proxy OFF — opt in with --mcp
  - egress PERMISSIVE (disclosed on stderr) — tighten with --egress-profile

The image is the first positional argument. The command to run inside the VM
follows a literal "--" and is required.

Example:
  bbox run-image ghcr.io/jbarslox/aider-bbox:latest -- aider
  bbox run-image ubuntu:24.04 --env OPENAI_API_KEY --egress-profile standard --allow-host api.openai.com:443 -- python -m http.server

See: docs/run-image.md (minimum image contract)`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dash := cmd.ArgsLenAtDash()
			if dash < 0 {
				return errors.New("run-image requires a command after \"--\" (e.g. bbox run-image IMAGE -- CMD)")
			}
			image := args[0]
			command := args[dash:]
			if len(command) == 0 {
				return errors.New("run-image requires a non-empty command after \"--\"")
			}
			// --env and --env-forward both append to the same forwarding list.
			f.envForward = append(append([]string{}, envFwd...), envFwdAlt...)
			f.traceEnabled = f.traceEnabled || os.Getenv("BBOX_TRACE") == "1"
			if ignored := mcpSubFlagsWithoutMCP(cmd, f.mcp); len(ignored) > 0 {
				_, _ = fmt.Fprintf(os.Stderr,
					"Warning: %s ignored without --mcp (the MCP proxy is off by default for run-image)\n",
					strings.Join(ignored, ", "))
			}
			return runRunImage(cmd.Context(), image, command, f)
		},
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.Flags().StringVar(&f.name, "name", "", "Agent name (VM name, logs, BBOX_AGENT_NAME); defaults to the image repo basename")
	cmd.Flags().StringSliceVar(&envFwd, "env", nil, "Forward a host env var into the VM (exact name or glob like 'PREFIX_*', repeatable)")
	cmd.Flags().StringSliceVar(&envFwdAlt, "env-forward", nil, "Alias for --env (repeatable)")
	cmd.Flags().Uint32Var(&f.cpus, "cpus", 0, "Number of vCPUs (0 = agent default)")
	cmd.Flags().StringVar(&f.memory, "memory", "", "RAM for the VM, e.g. 4g or 512m (empty = agent default)")
	cmd.Flags().StringVar(&f.tmpSize, "tmp-size", "", "Size of /tmp tmpfs inside the VM, e.g. 512m or 2g (empty = agent default)")
	cmd.Flags().BoolVar(&f.mcp, "mcp", false, "Enable the MCP tool proxy (off by default; forces mcp.mode=env)")
	cmd.Flags().StringVar(&f.mcpAuthzProfile, "mcp-authz-profile", "", "MCP authorization profile: full-access, observe, safe-tools, custom (default: safe-tools for run-image)")
	cmd.Flags().StringVar(&f.mcpGroup, "mcp-group", "default", "ToolHive group to discover MCP servers from")
	cmd.Flags().Uint16Var(&f.mcpPort, "mcp-port", 4483, "Port for MCP proxy on VM gateway")
	cmd.Flags().StringVar(&f.mcpConfig, "mcp-config", "", "Path to MCP config YAML (Cedar policies and aggregation settings)")
	cmd.Flags().DurationVar(&f.mcpSessionTTL, "mcp-session-ttl", 0, "Idle timeout for host MCP sessions, e.g. 12h or 30m (0 = default 12h)")
	cmd.Flags().StringVar(&f.egressProfile, "egress-profile", "permissive", "Egress restriction level: permissive, standard, locked (default: permissive)")
	cmd.Flags().StringSliceVar(&f.allowHosts, "allow-host", nil, "Additional allowed egress DNS hostname[:port] — no IP addresses (repeatable)")
	cmd.Flags().StringVar(&f.workspace, "workspace", "", "Workspace directory to mount (default: current directory)")
	cmd.Flags().StringVar(&f.workspaceMode, "workspace-mode", "", "Workspace isolation mode: snapshot (default), direct")
	cmd.Flags().BoolVar(&f.review, "review", false, "Enable interactive per-file review of workspace changes (snapshot mode only)")
	cmd.Flags().StringSliceVar(&f.excludes, "exclude", nil, "Additional exclude patterns for workspace snapshot (repeatable, snapshot mode only)")
	cmd.Flags().BoolVar(&f.yesAckDirect, "yes", false, "Acknowledge dangerous options without prompting (required on first --workspace-mode=direct run)")
	cmd.Flags().Uint16Var(&f.sshPort, "ssh-port", 0, "Host SSH port (0 = auto-pick)")
	cmd.Flags().StringSliceVar(&f.ports, "port", nil, "Forward an additional TCP port from guest to host as HOST:GUEST, bound to 127.0.0.1 (repeatable)")
	cmd.Flags().StringVar(&f.cfgPath, "config", "", "Config file path (default: ~/.config/broodbox/config.yaml; loaded for defaults)")
	cmd.Flags().BoolVar(&f.debug, "debug", false, "Enable debug-level logging to file (default: info level)")
	cmd.Flags().StringVar(&f.logFile, "log-file", "", "Override log file path (default: ~/.config/broodbox/vms/<vm-name>/broodbox.log)")
	cmd.Flags().BoolVar(&f.noFirmwareDL, "no-firmware-download", false, "Disable firmware download (use system libkrunfw only)")
	cmd.Flags().BoolVar(&f.noImageCache, "no-image-cache", false, "Disable OCI image caching (fresh pull every run)")
	cmd.Flags().StringVar(&f.pull, "pull", "", "Image pull policy: always, background, if-not-present, never (default: background)")
	cmd.Flags().BoolVar(&f.traceEnabled, "trace", false, "Enable OpenTelemetry tracing (writes trace.json to VM data dir)")
	cmd.Flags().BoolVar(&f.timings, "timings", false, "Print per-phase timing summary after run")
	// Opt-in forwarding: defaults to OFF for run-image (safer ephemeral
	// posture) — the inverse of the root command's --no-git-token flag.
	cmd.Flags().BoolVar(&f.gitToken, "git-token", false, "Forward GITHUB_TOKEN/GH_TOKEN into the VM (off by default for run-image)")
	cmd.Flags().BoolVar(&f.gitSSHAgent, "git-ssh-agent", false, "Forward the SSH agent into the VM (off by default for run-image)")
	cmd.Flags().BoolVar(&f.seedCredentials, "seed-credentials", false, "Seed agent credentials from host (e.g. macOS Keychain) into the VM")
	return cmd
}

// runRunImage is the run-image front-end. It resolves the agent name (from
// --name or derived from the image), validates the image ref, builds an
// in-memory AgentOverride with ephemeral defaults via the pure
// buildRunImageOverride, validates it (ValidateCustomAgent), maps it to an
// agent.Agent (AgentFromOverride), registers it as a data-only entry on the
// resolved registry, injects the override into the merged config so the
// sandbox layer observes mcp.mode=env, then drives the shared runSandbox tail.
func runRunImage(parentCtx context.Context, image string, command []string, f runImageFlags) error {
	ctx, cancel := signal.NotifyContext(parentCtx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// run-image is ephemeral: credential persistence is always OFF (the
	// credential store is never created), so --seed-credentials would be a
	// silent no-op. Reject it at parse time rather than mislead the operator.
	if f.seedCredentials {
		return errors.New("--seed-credentials is incompatible with run-image: credential persistence is always off for ephemeral runs")
	}

	// Validate the image reference syntactically (no network I/O).
	if err := imageRefValidator(image); err != nil {
		return fmt.Errorf("invalid image reference %q: %w (OCI references must be lowercase; see docs/run-image.md)", image, err)
	}

	// Resolve agent name: --name wins, otherwise derive from the image ref.
	agentName := f.name
	nameFromFlag := agentName != ""
	if !nameFromFlag {
		agentName = deriveAgentName(image)
	}
	if err := agent.ValidateName(agentName); err != nil {
		if nameFromFlag {
			return fmt.Errorf("invalid agent name %q: %w", agentName, err)
		}
		return fmt.Errorf("invalid agent name %q (derived from image; pass --name to override): %w", agentName, err)
	}
	// Disclose the resolved agent name on stderr. The name drives the VM name,
	// logs, and BBOX_AGENT_NAME, so surfacing it lets the operator catch a
	// surprising derivation before the run starts.
	if nameFromFlag {
		_, _ = fmt.Fprintf(os.Stderr, "Agent: %s\n", agentName)
	} else {
		_, _ = fmt.Fprintf(os.Stderr, "Agent: %s (derived from image; pass --name to override)\n", agentName)
	}

	// Validate --pull, --env-forward, --ports, --workspace-mode up front,
	// mirroring run().
	if f.pull != "" && !domainconfig.IsValidPullPolicy(f.pull) {
		return fmt.Errorf("invalid --pull %q: valid values are %v",
			f.pull, domainconfig.ValidPullPolicies())
	}
	if err := agent.ValidateEnvForwardPatterns(f.envForward); err != nil {
		return fmt.Errorf("invalid --env: %w", err)
	}
	parsedPorts, portsErr := parsePortForwards(f.ports)
	if portsErr != nil {
		return portsErr
	}
	if f.noImageCache && f.pull == domainconfig.PullNever {
		return fmt.Errorf("--no-image-cache and --pull=never are incompatible: "+
			"pull policy %q requires a cache to serve hits", domainconfig.PullNever)
	}
	if f.workspaceMode != "" && !domainconfig.IsValidWorkspaceMode(f.workspaceMode) {
		return fmt.Errorf("invalid --workspace-mode %q: valid values are %v",
			f.workspaceMode, domainconfig.ValidWorkspaceModes())
	}
	if f.workspaceMode == domainconfig.WorkspaceModeDirect {
		if f.review {
			return errors.New("--review has no effect in direct mode: there is no snapshot to review against. " +
				"Remove --review or drop --workspace-mode=direct")
		}
		if len(f.excludes) > 0 {
			return errors.New("--exclude applies to snapshot matching and is ignored in direct mode. " +
				"Remove --exclude or drop --workspace-mode=direct")
		}
	}
	if f.mcpAuthzProfile != "" || f.mcpSessionTTL != 0 {
		if err := validateMCPFlags(f.mcpAuthzProfile, f.mcpSessionTTL); err != nil {
			return err
		}
	}

	// Resolve workspace early for VM naming.
	earlyWs := f.workspace
	if earlyWs == "" {
		var wdErr error
		earlyWs, wdErr = os.Getwd()
		if wdErr != nil {
			return fmt.Errorf("getting current directory: %w", wdErr)
		}
	}

	var sessionBytes [4]byte
	if _, err := rand.Read(sessionBytes[:]); err != nil {
		return fmt.Errorf("generating session ID: %w", err)
	}
	sessionID := fmt.Sprintf("%x", sessionBytes)
	vmName := sandbox.VMName(agentName, earlyWs, sessionID)

	logPath, logFile, logCloser, err := openLogFile(f.logFile, vmName)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "Warning: could not open log file: %s\n", err)
	}
	if logCloser != nil {
		defer func() { _ = logCloser.Close() }()
	}
	logger := setupLogger(logFile, f.debug).With("vm", vmName)
	slog.SetDefault(logger)
	_, _ = fmt.Fprintf(os.Stderr, "Session log: %s\n", logPath)

	terminal := infraterminal.NewOSTerminal(os.Stdin, os.Stdout, os.Stderr)
	observer := chooseObserver(terminal)

	var timingObs *infraprogress.TimingObserver
	if f.timings {
		timingObs = infraprogress.NewTimingObserver(observer)
		observer = timingObs
		defer timingObs.Summary(os.Stderr)
	}

	var tracerProvider *sdktrace.TracerProvider
	if f.traceEnabled {
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

	ws := earlyWs

	snapDir, snapDirErr := snapshotCacheDir()
	if snapDirErr != nil {
		return fmt.Errorf("resolving snapshot cache directory: %w", snapDirErr)
	}
	infraws.CleanupStaleSnapshots(snapDir, logger)

	if os.Getenv("BBOX_KEEP_VM_DATA") == "1" {
		logger.Debug("BBOX_KEEP_VM_DATA set, skipping stale VM directory cleanup")
	} else if home, homeErr := os.UserHomeDir(); homeErr == nil {
		vmsDir := filepath.Join(home, ".config", "broodbox", "vms")
		go func() {
			start := time.Now()
			logger.Debug("starting stale VM directory cleanup", "vms_dir", vmsDir)
			infravm.CleanupStaleVMDirs(vmsDir, vmName, logger)
			logger.Debug("finished stale VM directory cleanup",
				"vms_dir", vmsDir, "elapsed", time.Since(start))
		}()
	}

	// Load global + workspace-local config for defaults (egress hosts, MCP
	// defaults, resource ceilings). Custom agents declared in config are also
	// registered so the run-image agent does not collide with them silently.
	resolved, err := buildResolvedRegistry(f.cfgPath, ws, logger, os.Stderr)
	if err != nil {
		return err
	}
	registry := resolved.registry
	cfg := resolved.merged

	// Reject a name that collides with ANY already-registered agent (built-in
	// OR config-declared custom). A data-only run-image entry would shadow a
	// built-in's plugin (MCP injector / seeder) or silently overwrite a custom
	// agent's definition via registry.Add. Force a distinct name.
	if err := checkAgentNameCollision(registry, agentName, nameFromFlag); err != nil {
		return err
	}

	// Build the ephemeral AgentOverride from the flags (pure).
	override, buildErr := buildRunImageOverride(image, command, f)
	if buildErr != nil {
		return buildErr
	}
	if err := domainconfig.ValidateCustomAgent(agentName, override, imageRefValidator); err != nil {
		return fmt.Errorf("validating run-image agent: %w", err)
	}
	ag, err := domainconfig.AgentFromOverride(agentName, override, cfg.Defaults)
	if err != nil {
		return fmt.Errorf("building run-image agent: %w", err)
	}
	if err := registry.Add(ag); err != nil {
		return fmt.Errorf("registering run-image agent: %w", err)
	}

	// Inject the override into the merged config so the sandbox layer observes
	// the run-image-specific fields it reads per-agent (notably mcp.mode=env,
	// which gates the gateway-only egress fallback in Prepare). This is an
	// in-memory mutation of the resolved config; nothing is written to disk.
	//
	// MergeConfigs shallow-copies: when the workspace-local config declares no
	// agents, merged.Agents aliases global.Agents. Mutating it in place would
	// leak the run-image override back into the global config object. Detach
	// the map first so this run's ephemeral entry stays local to this run.
	detached := make(map[string]domainconfig.AgentOverride, len(cfg.Agents)+1)
	for k, v := range cfg.Agents {
		detached[k] = v
	}
	cfg.Agents = detached
	cfg.Agents[agentName] = override

	// Resolve effective workspace mode / review / excludes, mirroring run().
	effectiveMode := f.workspaceMode
	if effectiveMode == "" {
		if cfg != nil {
			effectiveMode = cfg.Workspace.ResolvedWorkspaceMode()
		} else {
			effectiveMode = domainconfig.WorkspaceModeSnapshot
		}
	}
	directMode := effectiveMode == domainconfig.WorkspaceModeDirect

	interactiveReview := f.review
	if !interactiveReview && cfg != nil && cfg.Review.Enabled != nil && *cfg.Review.Enabled {
		interactiveReview = true
	}
	if directMode {
		if interactiveReview {
			_, _ = fmt.Fprintln(os.Stderr,
				"Warning: review.enabled is ignored in direct mode (no snapshot to review against).")
		}
		interactiveReview = false
		if err := ensureDirectModeAck(f.yesAckDirect, logger); err != nil {
			return err
		}
		_, _ = fmt.Fprintf(os.Stderr,
			"! Direct mode: agent writes directly to %s. No snapshot, no review, no undo.\n",
			ws,
		)
	}

	var excludePatterns []string
	if cfg != nil {
		excludePatterns = append(excludePatterns, cfg.Review.ExcludePatterns...)
	}
	excludePatterns = append(excludePatterns, f.excludes...)

	var snapshotMatcher, diffMatcher snapshot.Matcher
	if !directMode {
		excludeCfg, excludeErr := exclude.LoadExcludeConfig(ws, excludePatterns, logger)
		if excludeErr != nil {
			return fmt.Errorf("loading exclude config: %w", excludeErr)
		}
		snapshotMatcher = exclude.NewMatcherFromConfig(excludeCfg)

		gitignorePatterns, gitignoreErr := exclude.LoadGitignorePatterns(ws, logger)
		if gitignoreErr != nil {
			logger.Warn("failed to load .gitignore patterns", "error", gitignoreErr)
		}
		diffMatcher = exclude.NewDiffMatcher(excludeCfg, gitignorePatterns)
	}

	configEgressHosts, egressErr := domainconfig.ToEgressHosts(cfg.Network.AllowHosts)
	if egressErr != nil {
		return fmt.Errorf("config network.%w", egressErr)
	}

	// Disclose the permissive egress default so the operator notices. Printed
	// to stderr unconditionally (even when piped) because it is a security
	// disclosure, not progress noise.
	if f.egressProfile == "" || f.egressProfile == string(egress.ProfilePermissive) {
		_, _ = fmt.Fprintln(os.Stderr,
			"Egress: permissive — the agent has unrestricted outbound network. "+
				"Use --egress-profile standard --allow-host ... to restrict.")
	}

	// Disclose opt-in secret forwarding into an arbitrary image. Both are OFF
	// by default for run-image; only print when the operator opted in.
	if f.gitToken {
		_, _ = fmt.Fprintln(os.Stderr, "Forwarding GITHUB_TOKEN/GH_TOKEN into the VM")
	}
	if f.gitSSHAgent {
		_, _ = fmt.Fprintln(os.Stderr, "Forwarding SSH agent into the VM")
	}

	// Map run-image flags onto the shared runFlags and drive runSandbox.
	flags := runFlags{
		cpus:          f.cpus,
		memory:        f.memory,
		tmpSize:       f.tmpSize,
		workspace:     f.workspace,
		sshPort:       f.sshPort,
		cfgPath:       f.cfgPath,
		image:         "", // image is already baked into the registered agent
		debug:         f.debug,
		review:        f.review,
		workspaceMode: f.workspaceMode,
		yesAckDirect:  f.yesAckDirect,
		excludes:      f.excludes,
		logFile:       f.logFile,
		egressProfile: f.egressProfile,
		// allowHosts is intentionally nil here: buildRunImageOverride already
		// filed --allow-host under override.EgressHosts[profile], which
		// runSandbox reads via ag.EgressHosts. Leaving this set would land the
		// same hosts a second time via opts.AllowHosts (sandbox.go extraHosts).
		allowHosts:      nil,
		noMCP:           !f.mcp,
		mcpGroup:        f.mcpGroup,
		mcpPort:         f.mcpPort,
		mcpConfig:       f.mcpConfig,
		mcpAuthzProfile: f.mcpAuthzProfile,
		mcpSessionTTL:   f.mcpSessionTTL,
		// Opt-in inversion: run-image forwards neither by default, so the
		// shared runSandbox path (which negates these against the config
		// default) sees noGitToken/noGitSSHAgent true unless the operator
		// explicitly opted in.
		noGitToken:        !f.gitToken,
		noGitSSHAgent:     !f.gitSSHAgent,
		noSaveCredentials: true,  // ephemeral: never persist credentials
		seedCredentials:   false, // ephemeral: --seed-credentials already rejected above
		noSettings:        true,  // ephemeral: never inject host settings
		noFirmwareDL:      f.noFirmwareDL,
		noImageCache:      f.noImageCache,
		pull:              f.pull,
		traceEnabled:      f.traceEnabled,
		timings:           f.timings,
		exec:              "", // command is baked into the registered agent
		commandArgs:       nil,
		envForward:        f.envForward,
		ports:             f.ports,
	}

	return runSandbox(ctx, sandboxRunInput{
		agentName:         agentName,
		flags:             flags,
		registry:          registry,
		cfg:               cfg,
		ws:                ws,
		snapDir:           snapDir,
		vmName:            vmName,
		sessionID:         sessionID,
		logPath:           logPath,
		logFile:           logFile,
		logger:            logger,
		observer:          observer,
		timingObs:         timingObs,
		tracerProvider:    tracerProvider,
		terminal:          terminal,
		directMode:        directMode,
		interactiveReview: interactiveReview,
		snapshotMatcher:   snapshotMatcher,
		diffMatcher:       diffMatcher,
		configEgressHosts: configEgressHosts,
		parsedPorts:       parsedPorts,
	})
}

// buildRunImageOverride maps run-image flags into an AgentOverride carrying
// the ephemeral defaults. It is pure (no I/O, no go-containerregistry) so it
// can be table-tested directly. The image is accepted pre-validated; image-ref
// parsing stays in the caller (runRunImage via imageRefValidator).
// egress.ParseHostFlag is pure string parsing (no I/O), so folding
// --allow-host into EgressHosts below preserves purity.
//
// Ephemeral defaults baked in here:
//   - EnvForward: from --env / --env-forward only (empty by default)
//   - EgressProfile: "permissive" unless overridden (so the bare
//     `bbox run-image IMG -- cmd` example boots without declaring egress_hosts)
//   - EgressHosts: --allow-host entries, filed under the selected profile.
//     run-image has no config file to declare egress_hosts, so --allow-host is
//     the only way to satisfy ValidateCustomAgent's "non-permissive profile
//     needs hosts" gate (pkg/domain/config/custom_agent.go).
//   - MCP: when --mcp is set, MCP.Mode is forced to "env" (no config-file
//     injector exists for an arbitrary image; the agent discovers the proxy via
//     BBOX_MCP_URL). When --mcp is unset, MCP is left nil and runSandbox gates
//     it off via the noMCP flag.
//   - Credential persistence and settings import are OFF (handled in runSandbox
//     via noSaveCredentials/noSettings on runFlags, not via the override).
func buildRunImageOverride(image string, command []string, f runImageFlags) (domainconfig.AgentOverride, error) {
	if image == "" {
		return domainconfig.AgentOverride{}, errors.New("image is required")
	}
	if len(command) == 0 {
		return domainconfig.AgentOverride{}, errors.New("command is required")
	}

	o := domainconfig.AgentOverride{
		Image:       image,
		Description: "ephemeral run-image agent",
		Command:     append([]string(nil), command...),
		EnvForward:  append([]string(nil), f.envForward...),
	}

	// Egress profile defaults to permissive for the ephemeral UX; an explicit
	// --egress-profile (validated upstream) wins. An empty f.egressProfile
	// cannot happen (the flag default is "permissive"), but guard anyway.
	profile := f.egressProfile
	if profile == "" {
		profile = string(egress.ProfilePermissive)
	}
	o.EgressProfile = profile

	// File --allow-host entries under the selected profile so
	// ValidateCustomAgent sees them: run-image has no config file to declare
	// egress_hosts, so --allow-host is the only host source available here.
	if len(f.allowHosts) > 0 {
		cfgs := make([]domainconfig.EgressHostConfig, 0, len(f.allowHosts))
		for _, h := range f.allowHosts {
			parsed, err := egress.ParseHostFlag(h)
			if err != nil {
				return domainconfig.AgentOverride{}, fmt.Errorf("--allow-host %q: %w", h, err)
			}
			cfgs = append(cfgs, domainconfig.EgressHostConfig{
				Name: parsed.Name, Ports: parsed.Ports, Protocol: parsed.Protocol,
			})
		}
		o.EgressHosts = map[string][]domainconfig.EgressHostConfig{profile: cfgs}
	}

	// Resource overrides: only set when the operator supplied them so the
	// custom-agent floor (2 vCPU / 4096 MiB) applies otherwise.
	if f.cpus > 0 {
		o.CPUs = f.cpus
	}
	if f.memory != "" {
		parsed, err := bytesize.ParseByteSize(f.memory)
		if err != nil {
			return domainconfig.AgentOverride{}, fmt.Errorf("--memory: %w", err)
		}
		o.Memory = parsed
	}
	if f.tmpSize != "" {
		parsed, err := bytesize.ParseByteSize(f.tmpSize)
		if err != nil {
			return domainconfig.AgentOverride{}, fmt.Errorf("--tmp-size: %w", err)
		}
		o.TmpSize = parsed
	}

	// MCP: --mcp forces mode=env so the proxy is exposed only via BBOX_MCP_URL
	// (no config-file injector for an arbitrary image). Authz is resolved at
	// runSandbox wiring; the per-agent Authz override is set only when the
	// operator supplied --mcp-authz-profile.
	if f.mcp {
		o.MCP = &domainconfig.MCPAgentOverride{
			Mode: domainconfig.MCPModeEnv,
		}
		if f.mcpAuthzProfile != "" {
			o.MCP.Authz = &domainconfig.MCPAuthzConfig{Profile: f.mcpAuthzProfile}
		}
	}

	return o, nil
}

// validateMCPFlags validates the MCP authz-profile and session-TTL flag values.
// It is pure and shared by run() and runRunImage front-ends so the two commands
// don't drift; runSandbox re-checks the same as defence-in-depth (it is the
// primary guard for the run() path, which does not validate up front).
func validateMCPFlags(profile string, ttl time.Duration) error {
	if profile != "" && !domainconfig.IsValidMCPAuthzProfile(profile) {
		return fmt.Errorf("invalid --mcp-authz-profile %q: valid values are %v",
			profile, domainconfig.ValidMCPAuthzProfiles())
	}
	if ttl < 0 {
		return fmt.Errorf("--mcp-session-ttl must be non-negative, got %s", ttl)
	}
	return nil
}

// mcpSubFlagsWithoutMCP returns the MCP sub-flag names the operator explicitly
// set (via cmd.Flags().Changed) when mcp is false. --mcp-group and --mcp-port
// have non-zero defaults, so detecting "set" requires Changed rather than a
// zero-value comparison. An empty/nil result means either mcp is true or no
// sub-flag was set, in which case the caller prints nothing.
func mcpSubFlagsWithoutMCP(cmd *cobra.Command, mcp bool) []string {
	if mcp {
		return nil
	}
	var set []string
	for _, name := range []string{"mcp-authz-profile", "mcp-group", "mcp-port", "mcp-config", "mcp-session-ttl"} {
		if cmd.Flags().Changed(name) {
			set = append(set, "--"+name)
		}
	}
	return set
}

// checkAgentNameCollision rejects a run-image agent name that collides with any
// already-registered agent (built-in OR config-declared custom). A data-only
// run-image entry would shadow a built-in's plugin or silently overwrite a
// custom agent via registry.Add. The message distinguishes an explicit --name
// from a derived name so the operator knows how to fix it. Pure with respect to
// the registry (only calls Get).
func checkAgentNameCollision(registry *infraagent.Registry, agentName string, nameFromFlag bool) error {
	if _, lookupErr := registry.Get(agentName); lookupErr == nil {
		if nameFromFlag {
			return fmt.Errorf("--name %q is already in use; choose a different name", agentName)
		}
		return fmt.Errorf("derived agent name %q collides with an existing agent; pass --name to override", agentName)
	}
	return nil
}

// deriveAgentName produces an agent name from an OCI image reference by taking
// the repository basename (last path segment, after stripping any tag/digest),
// lower-casing it, replacing runs of non-[a-z0-9-] with '-', and collapsing /
// trimming surrounding '-'. It is pure string manipulation — it does NOT call
// go-containerregistry — so refs that ParseReference rejects as invalid
// (uppercase, ad-hoc registries) still yield a usable name. When the result is
// empty or degenerate the caller should fall back.
//
// Example: "ghcr.io/me/Aider-Bbox:latest" -> "aider-bbox".
func deriveAgentName(imageRef string) string {
	s := imageRef
	// Strip a digest suffix ("@sha256:...").
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[:i]
	}
	// Strip a tag (":tag") — but only the colon after the last '/', so a
	// registry port ("registry:5000/...") is not mistaken for a tag.
	if lastSlash := strings.LastIndex(s, "/"); lastSlash >= 0 {
		tail := s[lastSlash+1:]
		if i := strings.LastIndex(tail, ":"); i >= 0 {
			tail = tail[:i]
		}
		s = tail
	} else if i := strings.LastIndex(s, ":"); i >= 0 {
		s = s[:i]
	}
	s = strings.ToLower(s)
	// Replace runs of invalid chars with '-'.
	var b strings.Builder
	b.Grow(len(s))
	prevDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
		} else {
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "run-image"
	}
	return out
}
