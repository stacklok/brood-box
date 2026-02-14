// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package app provides the SandboxRunner application service that
// orchestrates the full sandbox VM lifecycle.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/stacklok/sandbox-agent/internal/domain/agent"
	"github.com/stacklok/sandbox-agent/internal/domain/config"
	infraconfig "github.com/stacklok/sandbox-agent/internal/infra/config"
	"github.com/stacklok/sandbox-agent/internal/infra/diff"
	"github.com/stacklok/sandbox-agent/internal/infra/exclude"
	"github.com/stacklok/sandbox-agent/internal/infra/review"
	infrassh "github.com/stacklok/sandbox-agent/internal/infra/ssh"
	"github.com/stacklok/sandbox-agent/internal/infra/vm"
	"github.com/stacklok/sandbox-agent/internal/infra/workspace"
)

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

	// ReviewEnabled controls whether snapshot isolation is active.
	ReviewEnabled bool

	// ExcludePatterns are additional gitignore-style patterns to exclude.
	ExcludePatterns []string
}

// SandboxDeps holds all dependencies for SandboxRunner.
type SandboxDeps struct {
	Registry    agent.Registry
	VMRunner    vm.VMRunner
	Terminal    infrassh.TerminalSession
	CfgLoader   *infraconfig.Loader
	EnvProvider agent.EnvProvider
	Logger      *slog.Logger

	// Snapshot isolation dependencies (nil = disabled).
	WorkspaceCloner workspace.WorkspaceCloner
	Reviewer        review.Reviewer
	Flusher         review.Flusher
	Differ          diff.Differ

	// Stdin, Stdout, Stderr are the terminal file descriptors for the
	// interactive SSH session. These must be real *os.File values (not
	// arbitrary io.Reader/Writer) because the PTY layer needs file
	// descriptors for term.MakeRaw and term.IsTerminal.
	Stdin  *os.File
	Stdout *os.File
	Stderr *os.File
}

// SandboxRunner orchestrates the full sandbox VM lifecycle:
// resolve agent, load config, collect env, start VM, run terminal, stop VM.
type SandboxRunner struct {
	registry        agent.Registry
	vmRunner        vm.VMRunner
	terminal        infrassh.TerminalSession
	cfgLoader       *infraconfig.Loader
	envProvider     agent.EnvProvider
	logger          *slog.Logger
	workspaceCloner workspace.WorkspaceCloner
	reviewer        review.Reviewer
	flusher         review.Flusher
	differ          diff.Differ
	stdin           *os.File
	stdout          *os.File
	stderr          *os.File
}

// NewSandboxRunner creates a new SandboxRunner with the given dependencies.
func NewSandboxRunner(deps SandboxDeps) *SandboxRunner {
	return &SandboxRunner{
		registry:        deps.Registry,
		vmRunner:        deps.VMRunner,
		terminal:        deps.Terminal,
		cfgLoader:       deps.CfgLoader,
		envProvider:     deps.EnvProvider,
		logger:          deps.Logger,
		workspaceCloner: deps.WorkspaceCloner,
		reviewer:        deps.Reviewer,
		flusher:         deps.Flusher,
		differ:          deps.Differ,
		stdin:           deps.Stdin,
		stdout:          deps.Stdout,
		stderr:          deps.Stderr,
	}
}

// Run executes the full sandbox lifecycle for the named agent.
func (s *SandboxRunner) Run(ctx context.Context, agentName string, opts RunOpts) error {
	// 1. Resolve agent from registry.
	ag, err := s.registry.Get(agentName)
	if err != nil {
		return fmt.Errorf("resolving agent: %w", err)
	}

	// 2. Load config and apply overrides.
	cfg, err := s.cfgLoader.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
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
	var matcher exclude.Matcher

	if opts.ReviewEnabled && s.workspaceCloner != nil {
		s.logger.Info("creating workspace snapshot for review isolation")

		excludeCfg, err := exclude.LoadExcludeConfig(workspacePath, opts.ExcludePatterns, s.logger)
		if err != nil {
			return fmt.Errorf("loading exclude config: %w", err)
		}

		matcher = exclude.NewMatcherFromConfig(excludeCfg)

		snap, err = s.workspaceCloner.CreateSnapshot(ctx, workspacePath, matcher)
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
	vmCfg := vm.VMConfig{
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
	sessionOpts := infrassh.SessionOpts{
		Host:    "127.0.0.1",
		Port:    sandboxVM.SSHPort(),
		User:    "sandbox",
		KeyPath: sandboxVM.SSHKeyPath(),
		Command: ag.Command,
		Stdin:   s.stdin,
		Stdout:  s.stdout,
		Stderr:  s.stderr,
	}

	s.logger.Info("connecting to sandbox VM",
		"port", sessionOpts.Port,
		"command", ag.Command,
	)

	termErr := s.terminal.Run(ctx, sessionOpts)

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
	if snap != nil && s.differ != nil && s.reviewer != nil && s.flusher != nil {
		if reviewErr := s.runReview(snap, matcher); reviewErr != nil {
			s.logger.Error("review/flush failed", "error", reviewErr)
			// Don't mask terminal errors with review errors.
		}
	}

	return termErr
}

// runReview performs the diff → review → flush sequence after the VM is stopped.
func (s *SandboxRunner) runReview(snap *workspace.Snapshot, matcher exclude.Matcher) error {
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
