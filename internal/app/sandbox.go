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

	// Snapshot holds snapshot isolation options.
	Snapshot SnapshotOpts
}

// SandboxDeps holds all dependencies for SandboxRunner.
type SandboxDeps struct {
	Registry      agent.Registry
	VMRunner      domvm.VMRunner
	SessionRunner session.TerminalSession
	Terminal      session.Terminal
	Config        *config.Config
	EnvProvider   agent.EnvProvider
	Logger        *slog.Logger

	// Snapshot isolation dependencies (nil = disabled).
	WorkspaceCloner workspace.WorkspaceCloner
	Reviewer        snapshot.Reviewer
	Flusher         snapshot.Flusher
	Differ          snapshot.Differ
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

// SandboxRunner orchestrates the full sandbox VM lifecycle:
// resolve agent, load config, collect env, start VM, run terminal, stop VM.
type SandboxRunner struct {
	registry        agent.Registry
	vmRunner        domvm.VMRunner
	sessionRunner   session.TerminalSession
	terminal        session.Terminal
	config          *config.Config
	envProvider     agent.EnvProvider
	logger          *slog.Logger
	workspaceCloner workspace.WorkspaceCloner
	reviewer        snapshot.Reviewer
	flusher         snapshot.Flusher
	differ          snapshot.Differ
}

// NewSandboxRunner creates a new SandboxRunner with the given dependencies.
func NewSandboxRunner(deps SandboxDeps) *SandboxRunner {
	return &SandboxRunner{
		registry:        deps.Registry,
		vmRunner:        deps.VMRunner,
		sessionRunner:   deps.SessionRunner,
		terminal:        deps.Terminal,
		config:          deps.Config,
		envProvider:     deps.EnvProvider,
		logger:          deps.Logger,
		workspaceCloner: deps.WorkspaceCloner,
		reviewer:        deps.Reviewer,
		flusher:         deps.Flusher,
		differ:          deps.Differ,
	}
}

// Prepare resolves the agent, applies config, collects env, sets up the
// workspace snapshot (if enabled), and starts the VM.
// The caller must call Cleanup() on the returned Sandbox when done.
func (s *SandboxRunner) Prepare(ctx context.Context, agentName string, opts RunOpts) (*Sandbox, error) {
	// 1. Resolve agent from registry.
	ag, err := s.registry.Get(agentName)
	if err != nil {
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

	s.logger.Info("resolved agent",
		"name", ag.Name,
		"image", ag.Image,
		"cpus", ag.DefaultCPUs,
		"memory", ag.DefaultMemory,
	)

	// 3. Collect env vars.
	envVars := agent.ForwardEnv(ag.EnvForward, s.envProvider)
	if len(envVars) > 0 {
		keys := make([]string, 0, len(envVars))
		for k := range envVars {
			keys = append(keys, k)
		}
		s.logger.Info("forwarding environment variables", "keys", keys)
	}

	// 4. Set up workspace path (possibly with snapshot isolation).
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
		s.logger.Info("creating workspace snapshot for review isolation")

		snap, err = s.workspaceCloner.CreateSnapshot(ctx, workspacePath, snapshotMatcher)
		if err != nil {
			return nil, fmt.Errorf("creating workspace snapshot: %w", err)
		}

		s.logger.Info("workspace snapshot created",
			"original", snap.OriginalPath,
			"snapshot", snap.SnapshotPath,
		)
		workspacePath = snap.SnapshotPath
	}

	// 5. Start VM with (possibly overridden) workspace path.
	vmCfg := domvm.VMConfig{
		Name:          "sandbox-" + ag.Name,
		Image:         ag.Image,
		CPUs:          ag.DefaultCPUs,
		Memory:        ag.DefaultMemory,
		SSHPort:       opts.SSHPort,
		WorkspacePath: workspacePath,
		EnvVars:       envVars,
	}

	sandboxVM, err := s.vmRunner.Start(ctx, vmCfg)
	if err != nil {
		// Clean up snapshot if we created one before VM start failed.
		if snap != nil {
			if cleanErr := snap.Cleanup(); cleanErr != nil {
				s.logger.Error("failed to clean up snapshot after VM start failure", "error", cleanErr)
			}
		}
		return nil, fmt.Errorf("starting sandbox VM: %w", err)
	}

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

// Run executes the full sandbox lifecycle for the named agent.
func (s *SandboxRunner) Run(ctx context.Context, agentName string, opts RunOpts) error {
	// 1. Resolve agent from registry.
	ag, err := s.registry.Get(agentName)
	if err != nil {
		return fmt.Errorf("resolving agent: %w", err)
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

	// Apply CLI flag overrides.
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

	s.logger.Info("resolved agent",
		"name", ag.Name,
		"image", ag.Image,
		"cpus", ag.DefaultCPUs,
		"memory", ag.DefaultMemory,
	)

	// 3. Collect env vars.
	envVars := agent.ForwardEnv(ag.EnvForward, s.envProvider)
	if len(envVars) > 0 {
		keys := make([]string, 0, len(envVars))
		for k := range envVars {
			keys = append(keys, k)
		}
		s.logger.Info("forwarding environment variables", "keys", keys)
	}

	// 4. Set up workspace path (possibly with snapshot isolation).
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
		s.logger.Info("creating workspace snapshot for review isolation")

		snap, err = s.workspaceCloner.CreateSnapshot(ctx, workspacePath, snapshotMatcher)
		if err != nil {
			return fmt.Errorf("creating workspace snapshot: %w", err)
		}
		// Cleanup runs LAST in LIFO defer order.
		defer func() {
			s.logger.Info("cleaning up workspace snapshot")
			if cleanErr := snap.Cleanup(); cleanErr != nil {
				s.logger.Error("failed to clean up snapshot", "error", cleanErr)
			}
		}()

		s.logger.Info("workspace snapshot created",
			"original", snap.OriginalPath,
			"snapshot", snap.SnapshotPath,
		)
		workspacePath = snap.SnapshotPath
	}

	// 5. Start VM with (possibly overridden) workspace path.
	vmCfg := domvm.VMConfig{
		Name:          "sandbox-" + ag.Name,
		Image:         ag.Image,
		CPUs:          ag.DefaultCPUs,
		Memory:        ag.DefaultMemory,
		SSHPort:       opts.SSHPort,
		WorkspacePath: workspacePath,
		EnvVars:       envVars,
	}

	sandboxVM, err := s.vmRunner.Start(ctx, vmCfg)
	if err != nil {
		return fmt.Errorf("starting sandbox VM: %w", err)
	}

	// 6. Run interactive terminal session.
	sessionOpts := session.SessionOpts{
		Host:     "127.0.0.1",
		Port:     sandboxVM.SSHPort(),
		User:     "sandbox",
		KeyPath:  sandboxVM.SSHKeyPath(),
		Command:  ag.Command,
		Terminal: s.terminal,
	}

	s.logger.Info("connecting to sandbox VM",
		"port", sessionOpts.Port,
		"command", ag.Command,
	)

	// Save terminal state BEFORE the SSH session so we can force-restore
	// it after. The SSH session puts stdin in raw mode; its internal defer
	// may race with the stdin-reading goroutine, leaving the terminal in
	// a broken state.
	restore, _ := s.terminal.MakeRaw()
	termErr := s.sessionRunner.Run(ctx, sessionOpts)

	// Force-restore terminal to cooked mode for the interactive review.
	// This must happen before any stdin reads (review prompts, etc.).
	// The restore func is idempotent (via sync.Once), so it's safe even
	// though the SSH session's defer also calls it.
	restore()

	// 7. Stop VM EXPLICITLY before diff/review.
	// This prevents the agent from modifying snapshot files between diff and flush.
	// Use a fresh context because the parent ctx may already be cancelled
	// (e.g. SIGINT triggered shutdown).
	s.logger.Info("shutting down sandbox VM")
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	if stopErr := sandboxVM.Stop(stopCtx); stopErr != nil {
		s.logger.Error("failed to stop VM", "error", stopErr)
	}

	// 8. Diff + review + flush (only when snapshot isolation is active).
	var reviewErr error
	if snap != nil && s.differ != nil && s.reviewer != nil && s.flusher != nil {
		reviewErr = s.runReview(snap, diffMatcher)
		if reviewErr != nil {
			s.logger.Error("review/flush failed", "error", reviewErr)
		}
	}

	// Return terminal error if present; otherwise surface review error.
	if termErr != nil {
		return termErr
	}
	return reviewErr
}

// runReview performs the diff → review → flush sequence after the VM is stopped.
func (s *SandboxRunner) runReview(snap *workspace.Snapshot, matcher snapshot.Matcher) error {
	s.logger.Info("computing workspace diff")
	changes, err := s.differ.Diff(snap.OriginalPath, snap.SnapshotPath, matcher)
	if err != nil {
		return fmt.Errorf("computing diff: %w", err)
	}

	if len(changes) == 0 {
		s.logger.Info("no workspace changes detected")
		return nil
	}

	s.logger.Info("workspace changes detected", "count", len(changes))

	result, err := s.reviewer.Review(changes)
	if err != nil {
		return fmt.Errorf("reviewing changes: %w", err)
	}

	if len(result.Accepted) == 0 {
		s.logger.Info("no changes accepted")
		return nil
	}

	s.logger.Info("flushing accepted changes",
		"accepted", len(result.Accepted),
		"rejected", len(result.Rejected),
	)

	if err := s.flusher.Flush(snap.OriginalPath, snap.SnapshotPath, result.Accepted); err != nil {
		return fmt.Errorf("flushing changes: %w", err)
	}

	s.logger.Info("changes flushed successfully")
	return nil
}
