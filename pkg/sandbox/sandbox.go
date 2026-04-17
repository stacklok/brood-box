// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package sandbox

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/bytesize"
	"github.com/stacklok/brood-box/pkg/domain/config"
	"github.com/stacklok/brood-box/pkg/domain/credential"
	"github.com/stacklok/brood-box/pkg/domain/egress"
	domaingit "github.com/stacklok/brood-box/pkg/domain/git"
	"github.com/stacklok/brood-box/pkg/domain/hostservice"
	"github.com/stacklok/brood-box/pkg/domain/progress"
	"github.com/stacklok/brood-box/pkg/domain/session"
	"github.com/stacklok/brood-box/pkg/domain/settings"
	"github.com/stacklok/brood-box/pkg/domain/snapshot"
	domvm "github.com/stacklok/brood-box/pkg/domain/vm"
	"github.com/stacklok/brood-box/pkg/domain/workspace"
)

// SnapshotOpts groups snapshot isolation options.
type SnapshotOpts struct {
	// Enabled controls whether snapshot isolation is active.
	Enabled bool

	// SnapshotMatcher excludes files from the workspace snapshot clone.
	// Nil defaults to snapshot.NopMatcher.
	SnapshotMatcher snapshot.Matcher

	// DiffMatcher excludes files from the diff computation.
	// Nil defaults to snapshot.NopMatcher.
	DiffMatcher snapshot.Matcher
}

// RunOpts holds runtime options for a sandbox execution.
type RunOpts struct {
	// CPUs overrides the agent's default vCPU count (0 = use default).
	CPUs uint32

	// Memory overrides the agent's default RAM in MiB (0 = use default).
	Memory uint32

	// Workspace is the host directory to mount (empty = use CWD).
	Workspace string

	// SSHPort is the host port for SSH (0 = auto-pick).
	SSHPort uint16

	// ImageOverride overrides the agent's OCI image reference.
	ImageOverride string

	// EgressProfile overrides the agent's default egress profile (empty = use default).
	EgressProfile string

	// AllowHosts are additional egress hosts from CLI flags.
	AllowHosts []egress.Host

	// CommandArgs appends arguments to the agent's command.
	// Ignored when CommandOverride is set.
	CommandArgs []string

	// CommandOverride replaces the agent's command entirely when set.
	CommandOverride []string

	// Snapshot holds snapshot isolation options.
	Snapshot SnapshotOpts

	// GitTokenEnabled controls whether git token env vars are forwarded.
	GitTokenEnabled bool

	// SSHAgentForward enables SSH agent forwarding to the VM.
	SSHAgentForward bool

	// SSHAuthSock is the path to the host SSH agent socket.
	// Required when SSHAgentForward is true.
	SSHAuthSock string

	// SessionID uniquely identifies this session so concurrent runs on the
	// same workspace get distinct VM names and data directories. Required;
	// must be a non-empty hex string (max 16 chars).
	SessionID string

	// LogLevel sets the hypervisor log verbosity (0=off, 5=trace).
	LogLevel uint32

	// TmpSizeMiB overrides the /tmp tmpfs size in MiB (0 = use agent/global default).
	TmpSizeMiB uint32

	// EnvForwardExtra holds additional env var patterns from CLI flags.
	// These are merged with the agent's configured patterns.
	EnvForwardExtra []string

	// PullPolicy overrides the image pull policy (empty = use config default).
	PullPolicy string

	// Terminal provides I/O streams for the session. Required for Run().
	Terminal session.Terminal
}

// SandboxConfig holds the subset of configuration that the sandbox
// runner actually needs. SDK consumers construct this directly instead
// of building the full CLI config schema.
type SandboxConfig struct {
	// Defaults specifies fallback resource limits.
	Defaults config.DefaultsConfig

	// AgentOverrides maps agent names to per-agent configuration overrides.
	AgentOverrides map[string]config.AgentOverride

	// ExtraEgressHosts are pre-converted egress hosts from config
	// (network.allow_hosts). CLI consumers call config.ToEgressHosts()
	// before populating this field.
	ExtraEgressHosts []egress.Host

	// Image configures OCI image pulling behavior.
	Image config.ImageConfig

	// MCP configures the in-process MCP proxy.
	MCP config.MCPConfig

	// SettingsImport configures agent settings injection.
	SettingsImport config.SettingsImportConfig
}

// SandboxDeps holds all dependencies for SandboxRunner.
type SandboxDeps struct {
	Registry      agent.Registry
	VMRunner      domvm.VMRunner
	SessionRunner session.TerminalSession
	Config        *SandboxConfig
	EnvProvider   agent.EnvProvider
	Logger        *slog.Logger
	Observer      progress.Observer

	// Snapshot isolation dependencies (nil = disabled).
	WorkspaceCloner workspace.WorkspaceCloner
	Reviewer        snapshot.Reviewer
	Flusher         snapshot.Flusher
	Differ          snapshot.Differ

	// CredentialStore persists agent credentials between sessions (nil = disabled).
	CredentialStore credential.Store

	// SettingsInjector handles host-to-guest settings injection and extraction (nil = disabled).
	SettingsInjector settings.Injector

	// MCPProvider creates host services for MCP proxy (nil = disabled).
	MCPProvider hostservice.Provider

	// SnapshotPostProcessors run after snapshot creation but before VM start.
	SnapshotPostProcessors []workspace.SnapshotPostProcessor

	// GitIdentityProvider resolves the host git user identity.
	GitIdentityProvider domaingit.IdentityProvider
}

// Sandbox holds the state of a running sandbox session.
// Created by Prepare, consumed by Attach/Stop/Changes/Flush/Cleanup.
type Sandbox struct {
	Agent            agent.Agent
	VM               domvm.VM
	VMConfig         domvm.VMConfig
	Snapshot         *workspace.Snapshot
	WorkspacePath    string
	DiffMatcher      snapshot.Matcher
	EnvVars          map[string]string
	ResolvedCommand  []string
	SSHAgentForward  bool
	SSHAuthSock      string
	SettingsManifest *settings.Manifest
}

// Cleanup releases resources (snapshot dir). Safe to call multiple times.
func (sb *Sandbox) Cleanup() error {
	if sb.Snapshot != nil {
		return sb.Snapshot.Cleanup()
	}
	return nil
}

// SandboxRunner orchestrates the full sandbox VM lifecycle.
//
// Two usage patterns are supported:
//
// Convenience (CLI): Call Run() for sequential prepare->attach->stop->review->cleanup.
//
// Lifecycle (HTTP server, custom control): Call Prepare(), Attach(), Stop(),
// Changes(), Flush(), and Sandbox.Cleanup() individually. This allows the caller
// to control terminal attachment, async review workflows, and concurrent sessions.
type SandboxRunner struct {
	registry               agent.Registry
	vmRunner               domvm.VMRunner
	sessionRunner          session.TerminalSession
	config                 *SandboxConfig
	envProvider            agent.EnvProvider
	logger                 *slog.Logger
	observer               progress.Observer
	workspaceCloner        workspace.WorkspaceCloner
	reviewer               snapshot.Reviewer
	flusher                snapshot.Flusher
	differ                 snapshot.Differ
	credentialStore        credential.Store
	settingsInjector       settings.Injector
	mcpProvider            hostservice.Provider
	snapshotPostProcessors []workspace.SnapshotPostProcessor
	gitIdentityProvider    domaingit.IdentityProvider
}

// NewSandboxRunner creates a new SandboxRunner with the given dependencies.
func NewSandboxRunner(deps SandboxDeps) *SandboxRunner {
	obs := deps.Observer
	if obs == nil {
		obs = progress.Nop()
	}
	return &SandboxRunner{
		registry:               deps.Registry,
		vmRunner:               deps.VMRunner,
		sessionRunner:          deps.SessionRunner,
		config:                 deps.Config,
		envProvider:            deps.EnvProvider,
		logger:                 deps.Logger,
		observer:               obs,
		workspaceCloner:        deps.WorkspaceCloner,
		reviewer:               deps.Reviewer,
		flusher:                deps.Flusher,
		differ:                 deps.Differ,
		credentialStore:        deps.CredentialStore,
		settingsInjector:       deps.SettingsInjector,
		mcpProvider:            deps.MCPProvider,
		snapshotPostProcessors: deps.SnapshotPostProcessors,
		gitIdentityProvider:    deps.GitIdentityProvider,
	}
}

// Prepare resolves the agent, applies config, collects env, sets up the
// workspace snapshot (if enabled), and starts the VM.
// The caller must call Cleanup() on the returned Sandbox when done.
func (s *SandboxRunner) Prepare(ctx context.Context, agentName string, opts RunOpts) (*Sandbox, error) {
	tracer := otel.Tracer("github.com/stacklok/brood-box")
	ctx, rootSpan := tracer.Start(ctx, "bbox.Prepare",
		trace.WithAttributes(attribute.String("bbox.agent", agentName)))
	defer rootSpan.End()

	// 0. Validate session ID.
	if opts.SessionID == "" || len(opts.SessionID) > 16 || !isHexString(opts.SessionID) {
		return nil, fmt.Errorf("session ID must be 1-16 hex characters, got %q", opts.SessionID)
	}

	// Validate SDK-supplied env-forward patterns. The CLI validates the
	// --env-forward flag in run(), but SDK consumers constructing RunOpts
	// directly bypass that path. Re-check here so the footgun is closed
	// at the SDK boundary too.
	if err := agent.ValidateEnvForwardPatterns(opts.EnvForwardExtra); err != nil {
		return nil, fmt.Errorf("invalid EnvForwardExtra: %w", err)
	}

	// 1. Resolve agent from registry.
	s.observer.Start(progress.PhaseResolvingAgent, "Resolving agent...")
	ag, err := s.registry.Get(agentName)
	if err != nil {
		s.observer.Fail("Agent not found")
		return nil, fmt.Errorf("resolving agent: %w", err)
	}

	// 2. Apply config overrides.
	cfg := s.config
	if cfg == nil {
		cfg = &SandboxConfig{}
	}

	override := config.AgentOverride{}
	if cfg.AgentOverrides != nil {
		if o, ok := cfg.AgentOverrides[agentName]; ok {
			override = o
		}
	}

	if opts.CPUs > 0 {
		override.CPUs = opts.CPUs
	}
	if opts.Memory > 0 {
		override.Memory = bytesize.ByteSize(opts.Memory)
	}
	if opts.TmpSizeMiB > 0 {
		override.TmpSize = bytesize.ByteSize(opts.TmpSizeMiB)
	}
	if opts.ImageOverride != "" {
		override.Image = opts.ImageOverride
	}

	ag = config.Merge(ag, override, cfg.Defaults)

	// Resolve settings manifest: filter by enabled categories.
	settingsManifest := s.resolveSettingsManifest(ag, cfg, agentName)

	command, err := resolveCommand(ag.Command, opts.CommandOverride, opts.CommandArgs)
	if err != nil {
		s.observer.Fail("Invalid command")
		return nil, fmt.Errorf("resolving command: %w", err)
	}

	s.observer.Complete(fmt.Sprintf("Resolved agent %s (%d CPUs, %s)",
		ag.Name, ag.DefaultCPUs, ag.DefaultMemory))
	s.logger.Debug("resolved agent",
		"name", ag.Name,
		"image", ag.Image,
		"cpus", ag.DefaultCPUs,
		"memory", ag.DefaultMemory,
	)
	s.logger.Debug("resolved command", "command", command)

	// Resolve egress policy.
	effectiveProfile := ag.DefaultEgressProfile
	if opts.EgressProfile != "" {
		effectiveProfile = egress.ProfileName(opts.EgressProfile)
	}

	egressPolicy, err := egress.Resolve(effectiveProfile, ag.EgressHosts)
	if err != nil {
		s.observer.Fail("Failed to resolve egress policy")
		return nil, fmt.Errorf("resolving egress policy: %w", err)
	}

	// Collect extra hosts: config network hosts + agent override hosts + CLI hosts.
	var extraHosts []egress.Host
	extraHosts = append(extraHosts, cfg.ExtraEgressHosts...)
	if override.AllowHosts != nil {
		overrideHosts, ohErr := config.ToEgressHosts(override.AllowHosts)
		if ohErr != nil {
			s.observer.Fail("Invalid agent egress host in config")
			return nil, fmt.Errorf("agent %q config %w", agentName, ohErr)
		}
		extraHosts = append(extraHosts, overrideHosts...)
	}
	extraHosts = append(extraHosts, opts.AllowHosts...)

	egressPolicy = egress.Merge(egressPolicy, extraHosts)

	s.logger.Debug("resolved egress policy",
		"profile", effectiveProfile,
		"restricted", egressPolicy != nil,
	)
	if egressPolicy != nil {
		s.logger.Debug("egress policy details",
			"allowed_hosts", len(egressPolicy.AllowedHosts),
		)
	}

	// 3. Collect env vars: agent defaults → forwarded host vars → git identity.
	// Agent defaults are applied first so host-side overrides take precedence.
	envVars := make(map[string]string)
	for k, v := range ag.DefaultEnv {
		envVars[k] = v
	}

	// Cap Node.js heap to a percentage of VM memory if configured.
	// This mitigates known memory leaks in Node.js agents (e.g. Claude Code)
	// by letting V8's GC manage memory within bounds rather than growing
	// until the guest OOM killer SIGKILLs the process.
	if ag.NodeHeapPercent > 0 {
		heapMiB := ag.DefaultMemory.MiB() * ag.NodeHeapPercent / 100
		envVars["NODE_OPTIONS"] = fmt.Sprintf("--max-old-space-size=%d", heapMiB)
	}

	allPatterns := ag.EnvForward
	if len(opts.EnvForwardExtra) > 0 {
		allPatterns = mergeEnvPatterns(allPatterns, opts.EnvForwardExtra)
	}
	if opts.GitTokenEnabled {
		allPatterns = mergeEnvPatterns(allPatterns, domaingit.CommonEnvPatterns())
	}
	for k, v := range agent.ForwardEnv(allPatterns, s.envProvider) {
		envVars[k] = v
	}
	if len(envVars) > 0 {
		keys := make([]string, 0, len(envVars))
		for k := range envVars {
			keys = append(keys, k)
		}
		s.logger.Debug("forwarding environment variables", "keys", keys)
	}

	// Resolve git identity (fallback for env vars not already set).
	var gitIdentity domaingit.Identity
	if s.gitIdentityProvider != nil {
		id, idErr := s.gitIdentityProvider.GetIdentity()
		if idErr != nil {
			s.logger.Warn("failed to resolve git identity", "error", idErr)
		} else {
			gitIdentity = id
		}
	}

	// Inject git identity into env vars as fallback when not already present.
	if gitIdentity.Name != "" {
		if _, ok := envVars["GIT_AUTHOR_NAME"]; !ok {
			envVars["GIT_AUTHOR_NAME"] = gitIdentity.Name
		}
		if _, ok := envVars["GIT_COMMITTER_NAME"]; !ok {
			envVars["GIT_COMMITTER_NAME"] = gitIdentity.Name
		}
	}
	if gitIdentity.Email != "" {
		if _, ok := envVars["GIT_AUTHOR_EMAIL"]; !ok {
			envVars["GIT_AUTHOR_EMAIL"] = gitIdentity.Email
		}
		if _, ok := envVars["GIT_COMMITTER_EMAIL"]; !ok {
			envVars["GIT_COMMITTER_EMAIL"] = gitIdentity.Email
		}
	}

	// Prevent git from hanging on interactive credential prompts inside the VM.
	// Public repos work anonymously; private repos fail cleanly without a token.
	envVars["GIT_TERMINAL_PROMPT"] = "0"

	// Determine if a GitHub token is available for credential helper injection.
	hasGitToken := opts.GitTokenEnabled && (envVars["GITHUB_TOKEN"] != "" || envVars["GH_TOKEN"] != "")

	// 4. Set up MCP host services if enabled.
	var hostServices []domvm.HostService
	mcpCfg := s.resolveMCPConfig(cfg, agentName)
	if mcpCfg.IsEnabled() && s.mcpProvider != nil {
		_, mcpSpan := tracer.Start(ctx, "bbox.ConfigureMCP")
		s.observer.Start(progress.PhaseConfiguringMCP, "Discovering MCP servers...")
		services, mcpErr := s.mcpProvider.Services(ctx)
		if mcpErr != nil {
			s.observer.Warn("MCP unavailable, continuing without MCP support")
			s.logger.Warn("failed to configure MCP services", "error", mcpErr)
		} else {
			for _, svc := range services {
				hostServices = append(hostServices, domvm.HostService{
					Name: svc.Name, Port: svc.Port, Handler: svc.Handler,
				})
			}
			s.observer.Complete(fmt.Sprintf("MCP proxy ready on port %d", mcpCfg.Port))
		}
		mcpSpan.End()
	}

	// 5. Set up workspace path (possibly with snapshot isolation).
	workspacePath := opts.Workspace
	var snap *workspace.Snapshot

	snapshotMatcher := opts.Snapshot.SnapshotMatcher
	if snapshotMatcher == nil {
		snapshotMatcher = snapshot.NopMatcher
	}

	diffMatcher := opts.Snapshot.DiffMatcher
	if diffMatcher == nil {
		diffMatcher = snapshot.NopMatcher
	}

	var extraMounts []workspace.MountRequest
	var extraDiffExclude []string

	if opts.Snapshot.Enabled && s.workspaceCloner != nil {
		_, snapSpan := tracer.Start(ctx, "bbox.CreateSnapshot")
		s.observer.Start(progress.PhaseCreatingSnapshot, "Creating workspace snapshot...")

		snap, err = s.workspaceCloner.CreateSnapshot(ctx, workspacePath, snapshotMatcher)
		if err != nil {
			s.observer.Fail("Failed to create snapshot")
			return nil, fmt.Errorf("creating workspace snapshot: %w", err)
		}

		s.observer.Complete("Created workspace snapshot")
		s.logger.Debug("workspace snapshot created",
			"original", snap.OriginalPath,
			"snapshot", snap.SnapshotPath,
		)

		// Run post-processors on the snapshot (e.g., git config sanitizer, worktree processor).
		// Failures abort VM start — post-processors are security-relevant
		// (credential stripping) and must not be silently skipped.
		for _, pp := range s.snapshotPostProcessors {
			ppResult, ppErr := pp.Process(ctx, snap.OriginalPath, snap.SnapshotPath)
			if ppErr != nil {
				s.observer.Fail("Snapshot post-processing failed")
				if cleanErr := snap.Cleanup(); cleanErr != nil {
					s.logger.Error("failed to clean up snapshot after post-processor failure", "error", cleanErr)
				}
				return nil, fmt.Errorf("snapshot post-processing: %w", ppErr)
			}
			if ppResult != nil {
				extraMounts = append(extraMounts, ppResult.Mounts...)
				extraDiffExclude = append(extraDiffExclude, ppResult.DiffExclude...)
			}
		}

		// If post-processors added diff excludes, compose them with the existing matcher.
		if len(extraDiffExclude) > 0 {
			diffMatcher = composeMatcher(diffMatcher, extraDiffExclude)
		}

		workspacePath = snap.SnapshotPath
		snapSpan.End()
	}

	// 6. Start VM with (possibly overridden) workspace path.
	_, vmSpan := tracer.Start(ctx, "bbox.StartVM")
	s.observer.Start(progress.PhaseStartingVM, "Starting sandbox VM...")

	vmCfg := domvm.VMConfig{
		Name:             VMName(ag.Name, workspacePath, opts.SessionID),
		AgentName:        ag.Name,
		Image:            ag.Image,
		CPUs:             ag.DefaultCPUs,
		Memory:           ag.DefaultMemory,
		SSHPort:          opts.SSHPort,
		WorkspacePath:    workspacePath,
		EnvVars:          envVars,
		EgressPolicy:     egressPolicy,
		HostServices:     hostServices,
		MCPConfigFormat:  ag.MCPConfigFormat,
		GitIdentity:      gitIdentity,
		HasGitToken:      hasGitToken,
		SSHAgentForward:  opts.SSHAgentForward,
		CredentialPaths:  ag.CredentialPaths,
		LogLevel:         opts.LogLevel,
		TmpSize:          ag.DefaultTmpSize,
		SettingsManifest: settingsManifest,
		ExtraMounts:      extraMounts,
		PullPolicy:       resolvePullPolicy(opts.PullPolicy, cfg),
	}

	sandboxVM, err := s.vmRunner.Start(ctx, vmCfg)
	if err != nil {
		vmSpan.RecordError(err)
		vmSpan.SetStatus(codes.Error, err.Error())
		vmSpan.End()
		s.observer.Fail("Failed to start VM")
		// Clean up snapshot if we created one before VM start failed.
		if snap != nil {
			if cleanErr := snap.Cleanup(); cleanErr != nil {
				s.logger.Error("failed to clean up snapshot after VM start failure", "error", cleanErr)
			}
		}
		return nil, fmt.Errorf("starting sandbox VM: %w", err)
	}
	vmSpan.End()

	s.observer.Complete("Sandbox ready")

	return &Sandbox{
		Agent:            ag,
		VM:               sandboxVM,
		VMConfig:         vmCfg,
		Snapshot:         snap,
		WorkspacePath:    workspacePath,
		DiffMatcher:      diffMatcher,
		EnvVars:          envVars,
		ResolvedCommand:  command,
		SSHAgentForward:  opts.SSHAgentForward,
		SSHAuthSock:      opts.SSHAuthSock,
		SettingsManifest: settingsManifest,
	}, nil
}

// Attach runs an interactive terminal session against the sandbox VM.
// It blocks until the remote command exits or the context is cancelled.
// The terminal parameter provides I/O streams and PTY control for this session.
func (s *SandboxRunner) Attach(ctx context.Context, sb *Sandbox, terminal session.Terminal) error {
	command := sb.ResolvedCommand
	if len(command) == 0 {
		command = sb.Agent.Command
	}
	sessionOpts := session.SessionOpts{
		Host:            "127.0.0.1",
		Port:            sb.VM.SSHPort(),
		User:            "sandbox",
		KeyPath:         sb.VM.SSHKeyPath(),
		Command:         command,
		Terminal:        terminal,
		SSHAgentForward: sb.SSHAgentForward,
		SSHAuthSock:     sb.SSHAuthSock,
		HostPublicKey:   sb.VM.SSHHostKey(),
	}

	s.logger.Debug("connecting to sandbox VM",
		"port", sessionOpts.Port,
		"command", sb.Agent.Command,
	)

	return s.sessionRunner.Run(ctx, sessionOpts)
}

// ExtractCredentials saves agent credential files from the guest rootfs.
// Errors are logged as warnings, not fatal — credential save failure should
// not prevent the user from working.
func (s *SandboxRunner) ExtractCredentials(sb *Sandbox) {
	if s.credentialStore == nil || len(sb.Agent.CredentialPaths) == 0 {
		return
	}
	s.observer.Start(progress.PhaseSavingCredentials, "Saving credentials...")
	rootfsPath := sb.VM.RootFSPath()
	if err := s.credentialStore.Extract(rootfsPath, sb.Agent.Name, sb.Agent.CredentialPaths); err != nil {
		s.observer.Warn(fmt.Sprintf("Could not save credentials for %s — see logs with --debug", sb.Agent.Name))
		s.logger.Warn("failed to extract credentials", "agent", sb.Agent.Name, "error", err)
		return
	}
	s.observer.Complete(fmt.Sprintf("Saved credentials for %s", sb.Agent.Name))
}

// ExtractSettings saves agent settings from the guest rootfs back to the host.
// Errors are logged as warnings, not fatal — settings save failure should
// not prevent the user from working.
func (s *SandboxRunner) ExtractSettings(sb *Sandbox) {
	if s.settingsInjector == nil || sb.SettingsManifest == nil || len(sb.SettingsManifest.Entries) == 0 {
		return
	}
	s.observer.Start(progress.PhaseSavingSettings, "Saving settings...")
	rootfsPath := sb.VM.RootFSPath()

	hostHome, err := os.UserHomeDir()
	if err != nil {
		s.observer.Warn("Could not resolve home directory for settings extraction")
		s.logger.Warn("failed to resolve host home for settings extraction", "error", err)
		return
	}

	result, err := s.settingsInjector.Extract(rootfsPath, hostHome, *sb.SettingsManifest)
	if err != nil {
		s.observer.Warn(fmt.Sprintf("Could not save settings for %s — see logs with --debug", sb.Agent.Name))
		s.logger.Warn("failed to extract settings", "agent", sb.Agent.Name, "error", err)
		return
	}

	if result.FileCount > 0 {
		s.observer.Complete(fmt.Sprintf("Saved %d settings file(s) for %s", result.FileCount, sb.Agent.Name))
	} else {
		s.observer.Complete("No settings to save")
	}
}

// Stop gracefully shuts down the sandbox VM.
// Uses a fresh context with timeout to ensure shutdown completes even if the
// parent context is already cancelled.
func (s *SandboxRunner) Stop(sb *Sandbox) error {
	s.observer.Start(progress.PhaseShuttingDown, "Shutting down VM...")
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	if err := sb.VM.Stop(stopCtx); err != nil {
		s.observer.Fail("Failed to stop VM")
		return err
	}
	s.observer.Complete("VM stopped")
	return nil
}

// Changes computes the diff between the original workspace and the snapshot.
// Returns nil with no error if snapshot isolation was not active or differ is nil.
func (s *SandboxRunner) Changes(sb *Sandbox) ([]snapshot.FileChange, error) {
	if sb.Snapshot == nil || s.differ == nil {
		return nil, nil
	}
	s.observer.Start(progress.PhaseComputingDiff, "Computing workspace changes...")
	changes, err := s.differ.Diff(sb.Snapshot.OriginalPath, sb.Snapshot.SnapshotPath, sb.DiffMatcher)
	if err != nil {
		s.observer.Fail("Failed to compute diff")
		return nil, fmt.Errorf("computing diff: %w", err)
	}
	s.observer.Complete(fmt.Sprintf("%d file(s) changed", len(changes)))
	return changes, nil
}

// Flush applies the accepted file changes from the snapshot to the original workspace.
// Returns nil if snapshot isolation was not active, flusher is nil, or no changes provided.
func (s *SandboxRunner) Flush(sb *Sandbox, accepted []snapshot.FileChange) error {
	if sb.Snapshot == nil || s.flusher == nil || len(accepted) == 0 {
		return nil
	}
	s.observer.Start(progress.PhaseFlushingChanges, "Flushing accepted changes...")
	if err := s.flusher.Flush(sb.Snapshot.OriginalPath, sb.Snapshot.SnapshotPath, accepted); err != nil {
		s.observer.Fail("Failed to flush changes")
		return fmt.Errorf("flushing changes: %w", err)
	}
	s.observer.Complete(fmt.Sprintf("Flushed %d change(s)", len(accepted)))
	return nil
}

// Run executes the full sandbox lifecycle for the named agent:
// Prepare -> Attach -> Stop -> review/flush -> Cleanup.
// opts.Terminal must be set to provide I/O streams for the session.
func (s *SandboxRunner) Run(ctx context.Context, agentName string, opts RunOpts) error {
	sb, err := s.Prepare(ctx, agentName, opts)
	if err != nil {
		return err
	}
	defer func() {
		if sb.Snapshot != nil {
			s.observer.Start(progress.PhaseCleaning, "Cleaning up...")
			if cleanErr := sb.Cleanup(); cleanErr != nil {
				s.observer.Fail("Failed to clean up snapshot")
				s.logger.Error("failed to clean up snapshot", "error", cleanErr)
			} else {
				s.observer.Complete("Cleaned up snapshot")
			}
		}
	}()

	termErr := s.Attach(ctx, sb, opts.Terminal)

	s.ExtractCredentials(sb)
	s.ExtractSettings(sb)

	if stopErr := s.Stop(sb); stopErr != nil {
		s.logger.Error("failed to stop VM", "error", stopErr)
	}

	var reviewErr error
	if sb.Snapshot != nil && s.reviewer != nil {
		changes, chErr := s.Changes(sb)
		if chErr != nil {
			reviewErr = chErr
		} else if len(changes) > 0 {
			result, revErr := s.reviewer.Review(changes)
			if revErr != nil {
				reviewErr = fmt.Errorf("reviewing changes: %w", revErr)
			} else if len(result.Accepted) > 0 {
				reviewErr = s.Flush(sb, result.Accepted)
			} else {
				s.observer.Warn("No changes accepted")
			}
		} else {
			s.observer.Warn("No workspace changes detected")
		}
		if reviewErr != nil {
			s.logger.Error("review/flush failed", "error", reviewErr)
		}
	}

	if termErr != nil {
		return termErr
	}
	return reviewErr
}

// mergeEnvPatterns combines two pattern lists, deduplicating entries.
func mergeEnvPatterns(base, extra []string) []string {
	seen := make(map[string]bool, len(base))
	for _, p := range base {
		seen[p] = true
	}
	merged := make([]string, len(base))
	copy(merged, base)
	for _, p := range extra {
		if !seen[p] {
			merged = append(merged, p)
			seen[p] = true
		}
	}
	return merged
}

func resolveCommand(base, override, args []string) ([]string, error) {
	var command []string
	if len(override) > 0 {
		command = append([]string{}, override...)
	} else {
		command = append([]string{}, base...)
	}
	if len(args) > 0 {
		command = append(command, args...)
	}
	if len(command) == 0 {
		return nil, fmt.Errorf("command cannot be empty")
	}
	for _, arg := range command {
		if strings.ContainsRune(arg, '\x00') {
			return nil, fmt.Errorf("command contains NUL byte")
		}
	}
	return command, nil
}

// VMName returns a VM name derived from the agent name, workspace path,
// and session ID. The session ID makes each concurrent session unique.
// sessionID must be a non-empty hex string.
func VMName(agentName, workspacePath, sessionID string) string {
	if workspacePath == "" {
		return fmt.Sprintf("sandbox-%s-%s", agentName, sessionID)
	}
	h := sha256.Sum256([]byte(workspacePath))
	return fmt.Sprintf("sandbox-%s-%x-%s", agentName, h[:4], sessionID)
}

// isHexString returns true if s consists entirely of lowercase hex digits.
// resolvePullPolicy returns the effective pull policy.
// CLI flag takes precedence over config file; empty defaults to background.
func resolvePullPolicy(cliOverride string, cfg *SandboxConfig) string {
	if cliOverride != "" {
		return cliOverride
	}
	if cfg != nil && cfg.Image.Pull != "" {
		return cfg.Image.Pull
	}
	return config.PullBackground
}

func isHexString(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// resolveSettingsManifest returns the agent's settings manifest filtered by
// enabled categories. Returns nil if settings import is disabled or the agent
// has no manifest.
func (s *SandboxRunner) resolveSettingsManifest(
	ag agent.Agent, cfg *SandboxConfig, agentName string,
) *settings.Manifest {
	if ag.SettingsManifest == nil || len(ag.SettingsManifest.Entries) == 0 {
		return nil
	}

	// Resolve settings import config: global + per-agent override.
	// Uses the domain's TightenSettingsCategories to ensure the same
	// tighten-only security policy as workspace config merging.
	importCfg := cfg.SettingsImport
	if override, ok := cfg.AgentOverrides[agentName]; ok && override.SettingsImport != nil {
		// Per-agent override can only disable (tighten).
		if override.SettingsImport.Enabled != nil && !*override.SettingsImport.Enabled {
			importCfg.Enabled = override.SettingsImport.Enabled
		}
		if override.SettingsImport.Categories != nil {
			importCfg.Categories = config.TightenSettingsCategories(
				importCfg.Categories, override.SettingsImport.Categories,
			)
		}
	}

	if !importCfg.IsEnabled() {
		return nil
	}

	// Filter entries by enabled categories.
	filtered := settings.FilterEntries(ag.SettingsManifest.Entries, func(e settings.Entry) bool {
		return importCfg.Categories.IsCategoryEnabled(e.Category)
	})

	if len(filtered) == 0 {
		return nil
	}

	return &settings.Manifest{Entries: filtered}
}

// resolveMCPConfig returns the effective MCP configuration by merging
// global config with any agent-specific Enabled override.
// Per-agent authz is handled at the composition root (cmd/bbox/main.go)
// where the VMCPProvider is constructed.
func (s *SandboxRunner) resolveMCPConfig(cfg *SandboxConfig, agentName string) config.MCPConfig {
	mcpCfg := cfg.MCP

	// Apply agent-specific Enabled override if present.
	if override, ok := cfg.AgentOverrides[agentName]; ok && override.MCP != nil {
		if override.MCP.Enabled != nil {
			mcpCfg.Enabled = override.MCP.Enabled
		}
	}

	// Apply defaults for log message.
	if mcpCfg.Group == "" {
		mcpCfg.Group = "default"
	}
	if mcpCfg.Port == 0 {
		mcpCfg.Port = 4483
	}

	return mcpCfg
}

// composeMatcher wraps an existing snapshot.Matcher with additional gitignore-style
// exclude patterns. Paths matching either the original matcher or any of the extra
// patterns are excluded from the diff.
func composeMatcher(base snapshot.Matcher, extraPatterns []string) snapshot.Matcher {
	return &compositeMatcher{base: base, extra: extraPatterns}
}

// compositeMatcher combines a base matcher with a set of literal path prefixes.
type compositeMatcher struct {
	base  snapshot.Matcher
	extra []string
}

func (c *compositeMatcher) Match(path string) bool {
	if c.base != nil && c.base.Match(path) {
		return true
	}
	for _, pattern := range c.extra {
		if path == pattern || strings.HasPrefix(path, strings.TrimSuffix(pattern, "/")+"/") {
			return true
		}
	}
	return false
}
