// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/sandbox-agent/internal/domain/agent"
	"github.com/stacklok/sandbox-agent/internal/domain/snapshot"
	infraconfig "github.com/stacklok/sandbox-agent/internal/infra/config"
	"github.com/stacklok/sandbox-agent/internal/infra/exclude"
	infrassh "github.com/stacklok/sandbox-agent/internal/infra/ssh"
	"github.com/stacklok/sandbox-agent/internal/infra/vm"
	"github.com/stacklok/sandbox-agent/internal/infra/workspace"
)

// mockVMRunner records the config it was called with.
type mockVMRunner struct {
	startCfg vm.VMConfig
	startErr error
	vm       *mockVM
}

func (m *mockVMRunner) Start(_ context.Context, cfg vm.VMConfig) (vm.VM, error) {
	m.startCfg = cfg
	if m.startErr != nil {
		return nil, m.startErr
	}
	return m.vm, nil
}

type mockVM struct {
	stopped    bool
	sshPort    uint16
	dataDir    string
	sshKeyPath string
}

func (m *mockVM) Stop(_ context.Context) error {
	m.stopped = true
	return nil
}

func (m *mockVM) SSHPort() uint16    { return m.sshPort }
func (m *mockVM) DataDir() string    { return m.dataDir }
func (m *mockVM) SSHKeyPath() string { return m.sshKeyPath }

// mockTerminal records the session opts it was called with.
type mockTerminal struct {
	runOpts infrassh.SessionOpts
	runErr  error
}

func (m *mockTerminal) Run(_ context.Context, opts infrassh.SessionOpts) error {
	m.runOpts = opts
	return m.runErr
}

// mockEnvProvider returns a fixed set of environment variables.
type mockEnvProvider struct {
	vars []string
}

func (m *mockEnvProvider) Environ() []string { return m.vars }

// mockRegistry is a simple in-memory agent registry for testing.
type mockRegistry struct {
	agents map[string]agent.Agent
}

func (m *mockRegistry) Get(name string) (agent.Agent, error) {
	a, ok := m.agents[name]
	if !ok {
		return agent.Agent{}, &agent.ErrNotFound{Name: name}
	}
	return a, nil
}

func (m *mockRegistry) List() []agent.Agent {
	result := make([]agent.Agent, 0, len(m.agents))
	for _, a := range m.agents {
		result = append(result, a)
	}
	return result
}

// mockWorkspaceCloner records calls and returns a snapshot pointing to a temp dir.
type mockWorkspaceCloner struct {
	createCalled bool
	createErr    error
	snapshot     *workspace.Snapshot
}

func (m *mockWorkspaceCloner) CreateSnapshot(_ context.Context, _ string, _ exclude.Matcher) (*workspace.Snapshot, error) {
	m.createCalled = true
	if m.createErr != nil {
		return nil, m.createErr
	}
	return m.snapshot, nil
}

// mockDiffer returns preconfigured changes.
type mockDiffer struct {
	changes []snapshot.FileChange
	diffErr error
}

func (m *mockDiffer) Diff(_, _ string, _ exclude.Matcher) ([]snapshot.FileChange, error) {
	return m.changes, m.diffErr
}

// mockReviewer accepts all changes.
type mockReviewer struct {
	reviewCalled bool
	result       snapshot.ReviewResult
	reviewErr    error
}

func (m *mockReviewer) Review(changes []snapshot.FileChange) (snapshot.ReviewResult, error) {
	m.reviewCalled = true
	if m.reviewErr != nil {
		return snapshot.ReviewResult{}, m.reviewErr
	}
	if m.result.Accepted != nil || m.result.Rejected != nil {
		return m.result, nil
	}
	// Default: accept all.
	return snapshot.ReviewResult{Accepted: changes}, nil
}

// mockFlusher records flush calls.
type mockFlusher struct {
	flushCalled bool
	flushed     []snapshot.FileChange
	flushErr    error
}

func (m *mockFlusher) Flush(_, _ string, accepted []snapshot.FileChange) error {
	m.flushCalled = true
	m.flushed = accepted
	return m.flushErr
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestSandboxRunner_Run(t *testing.T) {
	t.Parallel()

	// Write a minimal config file.
	dir := t.TempDir()
	cfgPath := dir + "/config.yaml"
	require.NoError(t, os.WriteFile(cfgPath, []byte("defaults:\n  cpus: 2\n  memory: 2048\n"), 0o644))

	testAgent := agent.Agent{
		Name:          "test-agent",
		Image:         "test-image:latest",
		Command:       []string{"test-cmd"},
		EnvForward:    []string{"TEST_KEY"},
		DefaultCPUs:   2,
		DefaultMemory: 2048,
	}

	mvm := &mockVM{
		sshPort:    2222,
		dataDir:    "/tmp/data",
		sshKeyPath: "/tmp/key",
	}

	vmRunner := &mockVMRunner{vm: mvm}
	terminal := &mockTerminal{}

	runner := NewSandboxRunner(SandboxDeps{
		Registry: &mockRegistry{agents: map[string]agent.Agent{
			"test-agent": testAgent,
		}},
		VMRunner:    vmRunner,
		Terminal:    terminal,
		CfgLoader:   infraconfig.NewLoader(cfgPath),
		EnvProvider: &mockEnvProvider{vars: []string{"TEST_KEY=secret123", "OTHER=foo"}},
		Logger:      testLogger(),
	})

	err := runner.Run(context.Background(), "test-agent", RunOpts{
		Workspace: "/tmp/workspace",
		SSHPort:   2222,
	})

	require.NoError(t, err)

	// Verify VM was started with correct config.
	assert.Equal(t, "sandbox-test-agent", vmRunner.startCfg.Name)
	assert.Equal(t, "test-image:latest", vmRunner.startCfg.Image)
	assert.Equal(t, uint32(2), vmRunner.startCfg.CPUs)
	assert.Equal(t, uint32(2048), vmRunner.startCfg.Memory)
	assert.Equal(t, "/tmp/workspace", vmRunner.startCfg.WorkspacePath)
	assert.Equal(t, map[string]string{"TEST_KEY": "secret123"}, vmRunner.startCfg.EnvVars)

	// Verify terminal session was started.
	assert.Equal(t, "127.0.0.1", terminal.runOpts.Host)
	assert.Equal(t, uint16(2222), terminal.runOpts.Port)
	assert.Equal(t, "sandbox", terminal.runOpts.User)
	assert.Equal(t, []string{"test-cmd"}, terminal.runOpts.Command)

	// Verify VM was stopped.
	assert.True(t, mvm.stopped)
}

func TestSandboxRunner_Run_AgentNotFound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := dir + "/config.yaml"
	require.NoError(t, os.WriteFile(cfgPath, []byte(""), 0o644))

	runner := NewSandboxRunner(SandboxDeps{
		Registry:    &mockRegistry{agents: map[string]agent.Agent{}},
		VMRunner:    &mockVMRunner{},
		Terminal:    &mockTerminal{},
		CfgLoader:   infraconfig.NewLoader(cfgPath),
		EnvProvider: &mockEnvProvider{},
		Logger:      testLogger(),
	})

	err := runner.Run(context.Background(), "nonexistent", RunOpts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent not found")
}

func TestSandboxRunner_Run_CLIOverrides(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := dir + "/config.yaml"
	require.NoError(t, os.WriteFile(cfgPath, []byte(""), 0o644))

	testAgent := agent.Agent{
		Name:          "test",
		Image:         "original:latest",
		Command:       []string{"cmd"},
		DefaultCPUs:   2,
		DefaultMemory: 2048,
	}

	mvm := &mockVM{sshPort: 3333, sshKeyPath: "/tmp/key"}
	vmRunner := &mockVMRunner{vm: mvm}

	runner := NewSandboxRunner(SandboxDeps{
		Registry:    &mockRegistry{agents: map[string]agent.Agent{"test": testAgent}},
		VMRunner:    vmRunner,
		Terminal:    &mockTerminal{},
		CfgLoader:   infraconfig.NewLoader(cfgPath),
		EnvProvider: &mockEnvProvider{},
		Logger:      testLogger(),
	})

	err := runner.Run(context.Background(), "test", RunOpts{
		CPUs:          4,
		Memory:        8192,
		ImageOverride: "custom:v2",
	})

	require.NoError(t, err)
	assert.Equal(t, "custom:v2", vmRunner.startCfg.Image)
	assert.Equal(t, uint32(4), vmRunner.startCfg.CPUs)
	assert.Equal(t, uint32(8192), vmRunner.startCfg.Memory)
}

func TestSandboxRunner_Run_ReviewEnabled_UsesSnapshotPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := dir + "/config.yaml"
	require.NoError(t, os.WriteFile(cfgPath, []byte(""), 0o644))

	// Create a real workspace dir and snapshot dir.
	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	testAgent := agent.Agent{
		Name:    "test",
		Image:   "img:latest",
		Command: []string{"cmd"},
	}

	mvm := &mockVM{sshPort: 4444, sshKeyPath: "/tmp/key"}
	vmRunner := &mockVMRunner{vm: mvm}
	cloner := &mockWorkspaceCloner{
		snapshot: &workspace.Snapshot{
			OriginalPath: workspaceDir,
			SnapshotPath: snapshotDir,
		},
	}
	differ := &mockDiffer{changes: nil} // No changes.

	runner := NewSandboxRunner(SandboxDeps{
		Registry:        &mockRegistry{agents: map[string]agent.Agent{"test": testAgent}},
		VMRunner:        vmRunner,
		Terminal:        &mockTerminal{},
		CfgLoader:       infraconfig.NewLoader(cfgPath),
		EnvProvider:     &mockEnvProvider{},
		Logger:          testLogger(),
		WorkspaceCloner: cloner,
		Differ:          differ,
		Reviewer:        &mockReviewer{},
		Flusher:         &mockFlusher{},
	})

	err := runner.Run(context.Background(), "test", RunOpts{
		Workspace:     workspaceDir,
		ReviewEnabled: true,
	})
	require.NoError(t, err)

	// VM should receive snapshot path, not the original.
	assert.True(t, cloner.createCalled)
	assert.Equal(t, snapshotDir, vmRunner.startCfg.WorkspacePath)
}

func TestSandboxRunner_Run_ReviewDisabled_UsesOriginalPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := dir + "/config.yaml"
	require.NoError(t, os.WriteFile(cfgPath, []byte(""), 0o644))

	testAgent := agent.Agent{
		Name:    "test",
		Image:   "img:latest",
		Command: []string{"cmd"},
	}

	mvm := &mockVM{sshPort: 5555, sshKeyPath: "/tmp/key"}
	vmRunner := &mockVMRunner{vm: mvm}

	runner := NewSandboxRunner(SandboxDeps{
		Registry:    &mockRegistry{agents: map[string]agent.Agent{"test": testAgent}},
		VMRunner:    vmRunner,
		Terminal:    &mockTerminal{},
		CfgLoader:   infraconfig.NewLoader(cfgPath),
		EnvProvider: &mockEnvProvider{},
		Logger:      testLogger(),
	})

	err := runner.Run(context.Background(), "test", RunOpts{
		Workspace:     "/my/workspace",
		ReviewEnabled: false,
	})
	require.NoError(t, err)

	// VM should receive original path.
	assert.Equal(t, "/my/workspace", vmRunner.startCfg.WorkspacePath)
}

func TestSandboxRunner_Run_ReviewWithChanges_FlushesAccepted(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := dir + "/config.yaml"
	require.NoError(t, os.WriteFile(cfgPath, []byte(""), 0o644))

	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	testAgent := agent.Agent{
		Name:    "test",
		Image:   "img:latest",
		Command: []string{"cmd"},
	}

	changes := []snapshot.FileChange{
		{RelPath: "file.go", Kind: snapshot.Modified, Hash: "abc123"},
	}

	mvm := &mockVM{sshPort: 6666, sshKeyPath: "/tmp/key"}
	vmRunner := &mockVMRunner{vm: mvm}
	cloner := &mockWorkspaceCloner{
		snapshot: &workspace.Snapshot{
			OriginalPath: workspaceDir,
			SnapshotPath: snapshotDir,
		},
	}
	differ := &mockDiffer{changes: changes}
	reviewer := &mockReviewer{}
	flusher := &mockFlusher{}

	runner := NewSandboxRunner(SandboxDeps{
		Registry:        &mockRegistry{agents: map[string]agent.Agent{"test": testAgent}},
		VMRunner:        vmRunner,
		Terminal:        &mockTerminal{},
		CfgLoader:       infraconfig.NewLoader(cfgPath),
		EnvProvider:     &mockEnvProvider{},
		Logger:          testLogger(),
		WorkspaceCloner: cloner,
		Differ:          differ,
		Reviewer:        reviewer,
		Flusher:         flusher,
	})

	err := runner.Run(context.Background(), "test", RunOpts{
		Workspace:     workspaceDir,
		ReviewEnabled: true,
	})
	require.NoError(t, err)

	assert.True(t, reviewer.reviewCalled)
	assert.True(t, flusher.flushCalled)
	assert.Len(t, flusher.flushed, 1)
	assert.Equal(t, "file.go", flusher.flushed[0].RelPath)
}

func TestSandboxRunner_Run_ReviewEmptyDiff_SkipsReview(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := dir + "/config.yaml"
	require.NoError(t, os.WriteFile(cfgPath, []byte(""), 0o644))

	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	testAgent := agent.Agent{
		Name:    "test",
		Image:   "img:latest",
		Command: []string{"cmd"},
	}

	mvm := &mockVM{sshPort: 7777, sshKeyPath: "/tmp/key"}
	cloner := &mockWorkspaceCloner{
		snapshot: &workspace.Snapshot{
			OriginalPath: workspaceDir,
			SnapshotPath: snapshotDir,
		},
	}
	reviewer := &mockReviewer{}

	runner := NewSandboxRunner(SandboxDeps{
		Registry:        &mockRegistry{agents: map[string]agent.Agent{"test": testAgent}},
		VMRunner:        &mockVMRunner{vm: mvm},
		Terminal:        &mockTerminal{},
		CfgLoader:       infraconfig.NewLoader(cfgPath),
		EnvProvider:     &mockEnvProvider{},
		Logger:          testLogger(),
		WorkspaceCloner: cloner,
		Differ:          &mockDiffer{changes: nil},
		Reviewer:        reviewer,
		Flusher:         &mockFlusher{},
	})

	err := runner.Run(context.Background(), "test", RunOpts{
		Workspace:     workspaceDir,
		ReviewEnabled: true,
	})
	require.NoError(t, err)

	// Reviewer should not be called when there are no changes.
	assert.False(t, reviewer.reviewCalled)
}

func TestSandboxRunner_Run_SnapshotCreationFails(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := dir + "/config.yaml"
	require.NoError(t, os.WriteFile(cfgPath, []byte(""), 0o644))

	testAgent := agent.Agent{
		Name:    "test",
		Image:   "img:latest",
		Command: []string{"cmd"},
	}

	cloner := &mockWorkspaceCloner{
		createErr: fmt.Errorf("disk full"),
	}

	runner := NewSandboxRunner(SandboxDeps{
		Registry:        &mockRegistry{agents: map[string]agent.Agent{"test": testAgent}},
		VMRunner:        &mockVMRunner{vm: &mockVM{sshPort: 8888, sshKeyPath: "/tmp/key"}},
		Terminal:        &mockTerminal{},
		CfgLoader:       infraconfig.NewLoader(cfgPath),
		EnvProvider:     &mockEnvProvider{},
		Logger:          testLogger(),
		WorkspaceCloner: cloner,
		Differ:          &mockDiffer{},
		Reviewer:        &mockReviewer{},
		Flusher:         &mockFlusher{},
	})

	err := runner.Run(context.Background(), "test", RunOpts{
		Workspace:     t.TempDir(),
		ReviewEnabled: true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating workspace snapshot")
}

func TestSandboxRunner_Run_VMStoppedBeforeReview(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfgPath := dir + "/config.yaml"
	require.NoError(t, os.WriteFile(cfgPath, []byte(""), 0o644))

	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	testAgent := agent.Agent{
		Name:    "test",
		Image:   "img:latest",
		Command: []string{"cmd"},
	}

	mvm := &mockVM{sshPort: 9999, sshKeyPath: "/tmp/key"}

	changes := []snapshot.FileChange{
		{RelPath: "test.go", Kind: snapshot.Added, Hash: "x"},
	}

	cloner := &mockWorkspaceCloner{
		snapshot: &workspace.Snapshot{
			OriginalPath: workspaceDir,
			SnapshotPath: snapshotDir,
		},
	}

	// Use a reviewer that checks VM state.
	orderCheckReviewer := &orderCheckingReviewer{vm: mvm}

	runner := NewSandboxRunner(SandboxDeps{
		Registry:        &mockRegistry{agents: map[string]agent.Agent{"test": testAgent}},
		VMRunner:        &mockVMRunner{vm: mvm},
		Terminal:        &mockTerminal{},
		CfgLoader:       infraconfig.NewLoader(cfgPath),
		EnvProvider:     &mockEnvProvider{},
		Logger:          testLogger(),
		WorkspaceCloner: cloner,
		Differ:          &mockDiffer{changes: changes},
		Reviewer:        orderCheckReviewer,
		Flusher:         &mockFlusher{},
	})

	err := runner.Run(context.Background(), "test", RunOpts{
		Workspace:     workspaceDir,
		ReviewEnabled: true,
	})
	require.NoError(t, err)

	assert.True(t, mvm.stopped, "VM should be stopped")
	assert.True(t, orderCheckReviewer.vmWasStoppedWhenCalled, "VM should be stopped before review")
}

// orderCheckingReviewer checks if the VM was stopped when Review is called.
type orderCheckingReviewer struct {
	vm                     *mockVM
	vmWasStoppedWhenCalled bool
}

func (r *orderCheckingReviewer) Review(changes []snapshot.FileChange) (snapshot.ReviewResult, error) {
	r.vmWasStoppedWhenCalled = r.vm.stopped
	return snapshot.ReviewResult{Accepted: changes}, nil
}
