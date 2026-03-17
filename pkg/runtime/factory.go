// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package runtime wires default sandbox dependencies for SDK consumers.
package runtime

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
	"github.com/stacklok/go-microvm/extract"

	infraagent "github.com/stacklok/brood-box/internal/infra/agent"
	infradiff "github.com/stacklok/brood-box/internal/infra/diff"
	infragit "github.com/stacklok/brood-box/internal/infra/git"
	infrareview "github.com/stacklok/brood-box/internal/infra/review"
	infrassh "github.com/stacklok/brood-box/internal/infra/ssh"
	infravm "github.com/stacklok/brood-box/internal/infra/vm"
	infraworkspace "github.com/stacklok/brood-box/internal/infra/workspace"
	domainagent "github.com/stacklok/brood-box/pkg/domain/agent"
	domaingit "github.com/stacklok/brood-box/pkg/domain/git"
	"github.com/stacklok/brood-box/pkg/domain/hostservice"
	"github.com/stacklok/brood-box/pkg/domain/progress"
	domainworkspace "github.com/stacklok/brood-box/pkg/domain/workspace"
	"github.com/stacklok/brood-box/pkg/sandbox"
)

// DefaultSandboxDepsOpts configures dependency wiring for SandboxRunner.
type DefaultSandboxDepsOpts struct {
	// Logger is used by infra implementations. Defaults to a discard logger.
	Logger *slog.Logger

	// RunnerPath overrides the go-microvm runner path (empty uses default lookup).
	RunnerPath string

	// LibDir sets the directory containing bundled shared libraries
	// (e.g. Homebrew libkrun on macOS).
	LibDir string

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

	// RuntimeSource provides embedded go-microvm-runner and libkrun (optional).
	RuntimeSource extract.Source

	// FirmwareSource provides libkrunfw (optional).
	FirmwareSource extract.Source

	// CacheDir is the directory used by bundle-based Sources for extraction.
	CacheDir string

	// SnapshotDir overrides the directory for workspace snapshot temp dirs.
	// Defaults to ~/.cache/broodbox/snapshots/ (XDG_CACHE_HOME), falling
	// back to os.TempDir() if XDG resolution fails.
	SnapshotDir string
}

// NewDefaultSandboxDeps wires Brood Box's standard infrastructure dependencies.
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

	var runnerOpts []infravm.RunnerOption
	if opts.RunnerPath != "" {
		runnerOpts = append(runnerOpts, infravm.WithRunnerPath(opts.RunnerPath))
	}
	if opts.LibDir != "" {
		runnerOpts = append(runnerOpts, infravm.WithLibDir(opts.LibDir))
	}
	if opts.RuntimeSource != nil {
		runnerOpts = append(runnerOpts, infravm.WithRuntimeSource(opts.RuntimeSource))
	}
	if opts.FirmwareSource != nil {
		runnerOpts = append(runnerOpts, infravm.WithFirmwareSource(opts.FirmwareSource))
	}
	if opts.CacheDir != "" {
		runnerOpts = append(runnerOpts, infravm.WithCacheDir(opts.CacheDir))
	}

	// Resolve snapshot base directory.
	snapDir := opts.SnapshotDir
	if snapDir == "" {
		if cacheBase := xdg.CacheHome; cacheBase != "" {
			snapDir = filepath.Join(cacheBase, "broodbox", "snapshots")
		} else {
			snapDir = filepath.Join(os.TempDir(), "broodbox-snapshots")
		}
	}

	return sandbox.SandboxDeps{
		Registry:      infraagent.NewRegistry(),
		VMRunner:      infravm.NewMicroVMRunner(logger, runnerOpts...),
		SessionRunner: infrassh.NewInteractiveSession(logger),
		Config:        cfg,
		EnvProvider:   domainagent.NewOSEnvProvider(os.Environ),
		Logger:        logger,
		Observer:      opts.Observer,
		WorkspaceCloner: infraworkspace.NewFSWorkspaceCloner(
			infraworkspace.NewPlatformCloner(),
			snapDir,
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
