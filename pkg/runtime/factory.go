// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package runtime wires default sandbox dependencies for SDK consumers.
package runtime

import (
	"io"
	"log/slog"
	"os"

	infraagent "github.com/stacklok/apiary/internal/infra/agent"
	infradiff "github.com/stacklok/apiary/internal/infra/diff"
	infragit "github.com/stacklok/apiary/internal/infra/git"
	infrareview "github.com/stacklok/apiary/internal/infra/review"
	infrassh "github.com/stacklok/apiary/internal/infra/ssh"
	infravm "github.com/stacklok/apiary/internal/infra/vm"
	infraworkspace "github.com/stacklok/apiary/internal/infra/workspace"
	domainagent "github.com/stacklok/apiary/pkg/domain/agent"
	domaingit "github.com/stacklok/apiary/pkg/domain/git"
	"github.com/stacklok/apiary/pkg/domain/hostservice"
	"github.com/stacklok/apiary/pkg/domain/progress"
	domainworkspace "github.com/stacklok/apiary/pkg/domain/workspace"
	"github.com/stacklok/apiary/pkg/sandbox"
)

// DefaultSandboxDepsOpts configures dependency wiring for SandboxRunner.
type DefaultSandboxDepsOpts struct {
	// Logger is used by infra implementations. Defaults to a discard logger.
	Logger *slog.Logger

	// RunnerPath overrides the propolis runner path (empty uses default lookup).
	RunnerPath string

	// Config provides optional sandbox config overrides.
	Config *sandbox.SandboxConfig

	// Observer receives lifecycle progress updates.
	Observer progress.Observer

	// MCPProvider supplies host services for the MCP proxy (optional).
	MCPProvider hostservice.Provider

	// GitIdentityProvider overrides the host git identity provider.
	GitIdentityProvider domaingit.IdentityProvider

	// SnapshotPostProcessors run after snapshot creation but before VM start.
	SnapshotPostProcessors []domainworkspace.SnapshotPostProcessor
}

// NewDefaultSandboxDeps wires apiary's standard infrastructure dependencies.
// Consumers can override fields on the returned SandboxDeps as needed.
func NewDefaultSandboxDeps(opts DefaultSandboxDepsOpts) sandbox.SandboxDeps {
	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	cfg := opts.Config
	if cfg == nil {
		cfg = &sandbox.SandboxConfig{}
	}

	gitIdentityProvider := opts.GitIdentityProvider
	if gitIdentityProvider == nil {
		gitIdentityProvider = infragit.NewHostIdentityProvider("")
	}

	return sandbox.SandboxDeps{
		Registry:      infraagent.NewRegistry(),
		VMRunner:      infravm.NewPropolisRunner(opts.RunnerPath, logger),
		SessionRunner: infrassh.NewInteractiveSession(logger),
		Config:        cfg,
		EnvProvider:   domainagent.NewOSEnvProvider(os.Environ),
		Logger:        logger,
		Observer:      opts.Observer,
		WorkspaceCloner: infraworkspace.NewFSWorkspaceCloner(
			infraworkspace.NewPlatformCloner(),
			logger,
		),
		Differ:                 infradiff.NewFSDiffer(),
		Flusher:                infrareview.NewFSFlusher(),
		MCPProvider:            opts.MCPProvider,
		SnapshotPostProcessors: opts.SnapshotPostProcessors,
		GitIdentityProvider:    gitIdentityProvider,
	}
}

// NewDefaultSandboxRunner constructs a SandboxRunner using default infra wiring.
func NewDefaultSandboxRunner(opts DefaultSandboxDepsOpts) *sandbox.SandboxRunner {
	deps := NewDefaultSandboxDeps(opts)
	return sandbox.NewSandboxRunner(deps)
}
