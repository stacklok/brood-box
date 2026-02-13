// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package app provides the SandboxRunner application service that
// orchestrates the full sandbox VM lifecycle.
package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/stacklok/sandbox-agent/internal/domain/agent"
	"github.com/stacklok/sandbox-agent/internal/domain/config"
	infraconfig "github.com/stacklok/sandbox-agent/internal/infra/config"
	infrassh "github.com/stacklok/sandbox-agent/internal/infra/ssh"
	"github.com/stacklok/sandbox-agent/internal/infra/vm"
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
}

// SandboxDeps holds all dependencies for SandboxRunner.
type SandboxDeps struct {
	Registry    agent.Registry
	VMRunner    vm.VMRunner
	Terminal    infrassh.TerminalSession
	CfgLoader   *infraconfig.Loader
	EnvProvider agent.EnvProvider
	Logger      *slog.Logger
}

// SandboxRunner orchestrates the full sandbox VM lifecycle:
// resolve agent, load config, collect env, start VM, run terminal, stop VM.
type SandboxRunner struct {
	registry    agent.Registry
	vmRunner    vm.VMRunner
	terminal    infrassh.TerminalSession
	cfgLoader   *infraconfig.Loader
	envProvider agent.EnvProvider
	logger      *slog.Logger
}

// NewSandboxRunner creates a new SandboxRunner with the given dependencies.
func NewSandboxRunner(deps SandboxDeps) *SandboxRunner {
	return &SandboxRunner{
		registry:    deps.Registry,
		vmRunner:    deps.VMRunner,
		terminal:    deps.Terminal,
		cfgLoader:   deps.CfgLoader,
		envProvider: deps.EnvProvider,
		logger:      deps.Logger,
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

	// 4. Start VM.
	vmCfg := vm.VMConfig{
		Name:          "sandbox-" + ag.Name,
		Image:         ag.Image,
		CPUs:          ag.DefaultCPUs,
		Memory:        ag.DefaultMemory,
		SSHPort:       opts.SSHPort,
		WorkspacePath: opts.Workspace,
		EnvVars:       envVars,
	}

	sandboxVM, err := s.vmRunner.Start(ctx, vmCfg)
	if err != nil {
		return fmt.Errorf("starting sandbox VM: %w", err)
	}

	// 5. Ensure VM is stopped on exit.
	defer func() {
		s.logger.Info("shutting down sandbox VM")
		if stopErr := sandboxVM.Stop(ctx); stopErr != nil {
			s.logger.Error("failed to stop VM", "error", stopErr)
		}
	}()

	// 6. Run interactive terminal session.
	sessionOpts := infrassh.SessionOpts{
		Host:    "127.0.0.1",
		Port:    sandboxVM.SSHPort(),
		User:    "sandbox",
		KeyPath: sandboxVM.SSHKeyPath(),
		Command: ag.Command,
	}

	s.logger.Info("connecting to sandbox VM",
		"port", sessionOpts.Port,
		"command", ag.Command,
	)

	return s.terminal.Run(ctx, sessionOpts)
}
