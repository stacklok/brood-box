// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package app provides the SandboxRunner application service that
// orchestrates the full sandbox VM lifecycle.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/stacklok/sandbox-agent/internal/domain/agent"
	"github.com/stacklok/sandbox-agent/internal/domain/config"
	"github.com/stacklok/sandbox-agent/internal/domain/egress"
	"github.com/stacklok/sandbox-agent/internal/domain/hostservice"
	"github.com/stacklok/sandbox-agent/internal/domain/progress"
	"github.com/stacklok/sandbox-agent/internal/domain/session"
	"github.com/stacklok/sandbox-agent/internal/domain/snapshot"
	domvm "github.com/stacklok/sandbox-agent/internal/domain/vm"
	"github.com/stacklok/sandbox-agent/internal/domain/workspace"
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

	// Snapshot holds snapshot isolation options.
	Snapshot SnapshotOpts

	// Terminal provides I/O streams for the session. Required for Run().
	Terminal session.Terminal
}

// SandboxDeps holds all dependencies for SandboxRunner.
type SandboxDeps struct {
	Registry      agent.Registry
	VMRunner      domvm.VMRunner
	SessionRunner session.TerminalSession
	Config        *config.Config
	EnvProvider   agent.EnvProvider
	Logger        *slog.Logger
	Observer      progress.Observer

	// Snapshot isolation dependencies (nil = disabled).
	WorkspaceCloner workspace.WorkspaceCloner
	Reviewer        snapshot.Reviewer
	Flusher         snapshot.Flusher
	Differ          snapshot.Differ

	// MCPProvider creates host services for MCP proxy (nil = disabled).
	MCPProvider hostservice.Provider
}

// Sandbox holds the state of a running sandbox session.
// Created by Prepare, consumed by Attach/Stop/Changes/Flush/Cleanup.
type Sandbox struct {
	Agent         agent.Agent
	VM            domvm.VM
	VMConfig      domvm.VMConfig
	Snapshot      *workspace.Snapshot
	WorkspacePath string
	DiffMatcher   snapshot.Matcher
	EnvVars       map[string]string
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
	registry        agent.Registry
	vmRunner        domvm.VMRunner
	sessionRunner   session.TerminalSession
	config          *config.Config
	envProvider     agent.EnvProvider
	logger          *slog.Logger
	observer        progress.Observer
	workspaceCloner workspace.WorkspaceCloner
	reviewer        snapshot.Reviewer
	flusher         snapshot.Flusher
	differ          snapshot.Differ
	mcpProvider     hostservice.Provider
}

// NewSandboxRunner creates a new SandboxRunner with the given dependencies.
func NewSandboxRunner(deps SandboxDeps) *SandboxRunner {
	obs := deps.Observer
	if obs == nil {
		obs = progress.Nop()
	}
	return &SandboxRunner{
		registry:        deps.Registry,
		vmRunner:        deps.VMRunner,
		sessionRunner:   deps.SessionRunner,
		config:          deps.Config,
		envProvider:     deps.EnvProvider,
		logger:          deps.Logger,
		observer:        obs,
		workspaceCloner: deps.WorkspaceCloner,
		reviewer:        deps.Reviewer,
		flusher:         deps.Flusher,
		differ:          deps.Differ,
		mcpProvider:     deps.MCPProvider,
	}
}

// Prepare resolves the agent, applies config, collects env, sets up the
// workspace snapshot (if enabled), and starts the VM.
// The caller must call Cleanup() on the returned Sandbox when done.
func (s *SandboxRunner) Prepare(ctx context.Context, agentName string, opts RunOpts) (*Sandbox, error) {
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
		cfg = &config.Config{}
	}

	override := config.AgentOverride{}
	if cfg.Agents != nil {
		if o, ok := cfg.Agents[agentName]; ok {
			override = o
		}
	}

	if opts.CPUs > 0 {
		override.CPUs = opts.CPUs
	}
	if opts.Memory > 0 {
		override.Memory = opts.Memory
	}
	if opts.ImageOverride != "" {
		override.Image = opts.ImageOverride
	}

	ag = config.Merge(ag, override, cfg.Defaults)

	s.observer.Complete(fmt.Sprintf("Resolved agent %s (%d CPUs, %d MiB)",
		ag.Name, ag.DefaultCPUs, ag.DefaultMemory))
	s.logger.Debug("resolved agent",
		"name", ag.Name,
		"image", ag.Image,
		"cpus", ag.DefaultCPUs,
		"memory", ag.DefaultMemory,
	)

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
	extraHosts = append(extraHosts, config.ToEgressHosts(cfg.Network.AllowHosts)...)
	if override.AllowHosts != nil {
		extraHosts = append(extraHosts, config.ToEgressHosts(override.AllowHosts)...)
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

	// 3. Collect env vars.
	envVars := agent.ForwardEnv(ag.EnvForward, s.envProvider)
	if len(envVars) > 0 {
		keys := make([]string, 0, len(envVars))
		for k := range envVars {
			keys = append(keys, k)
		}
		s.logger.Debug("forwarding environment variables", "keys", keys)
	}

	// 4. Set up MCP host services if enabled.
	var hostServices []domvm.HostService
	mcpCfg := s.resolveMCPConfig(cfg, agentName)
	if mcpCfg.Enabled && s.mcpProvider != nil {
		s.observer.Start(progress.PhaseConfiguringMCP, "Discovering MCP servers...")
		services, mcpErr := s.mcpProvider.Services(ctx)
		if mcpErr != nil {
			s.observer.Fail("Failed to configure MCP")
			return nil, fmt.Errorf("configuring MCP services: %w", mcpErr)
		}
		for _, svc := range services {
			hostServices = append(hostServices, domvm.HostService{
				Name: svc.Name, Port: svc.Port, Handler: svc.Handler,
			})
		}
		s.observer.Complete(fmt.Sprintf("MCP proxy ready on port %d", mcpCfg.Port))
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

	if opts.Snapshot.Enabled && s.workspaceCloner != nil {
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
		workspacePath = snap.SnapshotPath
	}

	// 6. Start VM with (possibly overridden) workspace path.
	s.observer.Start(progress.PhaseStartingVM, "Starting sandbox VM...")

	vmCfg := domvm.VMConfig{
		Name:            "sandbox-" + ag.Name,
		Image:           ag.Image,
		CPUs:            ag.DefaultCPUs,
		Memory:          ag.DefaultMemory,
		SSHPort:         opts.SSHPort,
		WorkspacePath:   workspacePath,
		EnvVars:         envVars,
		EgressPolicy:    egressPolicy,
		HostServices:    hostServices,
		MCPConfigFormat: ag.MCPConfigFormat,
	}

	sandboxVM, err := s.vmRunner.Start(ctx, vmCfg)
	if err != nil {
		s.observer.Fail("Failed to start VM")
		// Clean up snapshot if we created one before VM start failed.
		if snap != nil {
			if cleanErr := snap.Cleanup(); cleanErr != nil {
				s.logger.Error("failed to clean up snapshot after VM start failure", "error", cleanErr)
			}
		}
		return nil, fmt.Errorf("starting sandbox VM: %w", err)
	}

	s.observer.Complete("Sandbox ready")

	return &Sandbox{
		Agent:         ag,
		VM:            sandboxVM,
		VMConfig:      vmCfg,
		Snapshot:      snap,
		WorkspacePath: workspacePath,
		DiffMatcher:   diffMatcher,
		EnvVars:       envVars,
	}, nil
}

// Attach runs an interactive terminal session against the sandbox VM.
// It blocks until the remote command exits or the context is cancelled.
// The terminal parameter provides I/O streams and PTY control for this session.
func (s *SandboxRunner) Attach(ctx context.Context, sb *Sandbox, terminal session.Terminal) error {
	sessionOpts := session.SessionOpts{
		Host:     "127.0.0.1",
		Port:     sb.VM.SSHPort(),
		User:     "sandbox",
		KeyPath:  sb.VM.SSHKeyPath(),
		Command:  sb.Agent.Command,
		Terminal: terminal,
	}

	s.logger.Debug("connecting to sandbox VM",
		"port", sessionOpts.Port,
		"command", sb.Agent.Command,
	)

	return s.sessionRunner.Run(ctx, sessionOpts)
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
		s.observer.Start(progress.PhaseCleaning, "Cleaning up...")
		if cleanErr := sb.Cleanup(); cleanErr != nil {
			s.observer.Fail("Failed to clean up snapshot")
			s.logger.Error("failed to clean up snapshot", "error", cleanErr)
		} else {
			s.observer.Complete("Cleaned up snapshot")
		}
	}()

	termErr := s.Attach(ctx, sb, opts.Terminal)

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

// resolveMCPConfig returns the effective MCP configuration by merging
// global config with any agent-specific override.
func (s *SandboxRunner) resolveMCPConfig(cfg *config.Config, agentName string) config.MCPConfig {
	mcpCfg := cfg.MCP

	// Apply agent-specific override if present.
	if override, ok := cfg.Agents[agentName]; ok && override.MCP != nil {
		if override.MCP.Enabled {
			mcpCfg.Enabled = true
		}
		if override.MCP.Group != "" {
			mcpCfg.Group = override.MCP.Group
		}
		if override.MCP.Port != 0 {
			mcpCfg.Port = override.MCP.Port
		}
		if override.MCP.ConfigPath != "" {
			mcpCfg.ConfigPath = override.MCP.ConfigPath
		}
	}

	// Apply defaults.
	if mcpCfg.Group == "" {
		mcpCfg.Group = "default"
	}
	if mcpCfg.Port == 0 {
		mcpCfg.Port = 4483
	}

	return mcpCfg
}
