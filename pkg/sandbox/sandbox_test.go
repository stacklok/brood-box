// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package sandbox

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/apiary/pkg/domain/agent"
	"github.com/stacklok/apiary/pkg/domain/egress"
	"github.com/stacklok/apiary/pkg/domain/session"
	"github.com/stacklok/apiary/pkg/domain/snapshot"
	domvm "github.com/stacklok/apiary/pkg/domain/vm"
	"github.com/stacklok/apiary/pkg/domain/workspace"
)

// mockVMRunner records the config it was called with.
type mockVMRunner struct {
	startCfg domvm.VMConfig
	startErr error
	vm       *mockVM
}

func (m *mockVMRunner) Start(_ context.Context, cfg domvm.VMConfig) (domvm.VM, error) {
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

// mockSessionRunner records the session opts it was called with.
type mockSessionRunner struct {
	runOpts session.SessionOpts
	runErr  error
}

func (m *mockSessionRunner) Run(_ context.Context, opts session.SessionOpts) error {
	m.runOpts = opts
	return m.runErr
}

// mockTerminal implements session.Terminal for testing.
type mockTerminal struct{}

func (m *mockTerminal) Stdin() io.Reader                { return strings.NewReader("") }
func (m *mockTerminal) Stdout() io.Writer               { return io.Discard }
func (m *mockTerminal) Stderr() io.Writer               { return io.Discard }
func (m *mockTerminal) IsInteractive() bool             { return false }
func (m *mockTerminal) Size() (session.TermSize, error) { return session.TermSize{}, nil }
func (m *mockTerminal) MakeRaw() (func(), error)        { return func() {}, nil }
func (m *mockTerminal) NotifyResize(_ context.Context) <-chan session.TermSize {
	ch := make(chan session.TermSize)
	close(ch)
	return ch
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
	createCalled  bool
	createErr     error
	snapshot      *workspace.Snapshot
	receivedMatch snapshot.Matcher
}

func (m *mockWorkspaceCloner) CreateSnapshot(_ context.Context, _ string, matcher snapshot.Matcher) (*workspace.Snapshot, error) {
	m.createCalled = true
	m.receivedMatch = matcher
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

func (m *mockDiffer) Diff(_, _ string, _ snapshot.Matcher) ([]snapshot.FileChange, error) {
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
	sessionRunner := &mockSessionRunner{}

	runner := NewSandboxRunner(SandboxDeps{
		Registry: &mockRegistry{agents: map[string]agent.Agent{
			"test-agent": testAgent,
		}},
		VMRunner:      vmRunner,
		SessionRunner: sessionRunner,
		Config:        &SandboxConfig{},
		EnvProvider:   &mockEnvProvider{vars: []string{"TEST_KEY=secret123", "OTHER=foo"}},
		Logger:        testLogger(),
	})

	err := runner.Run(context.Background(), "test-agent", RunOpts{
		Workspace:     "/tmp/workspace",
		SSHPort:       2222,
		EgressProfile: string(egress.ProfilePermissive),
		Terminal:      &mockTerminal{},
	})

	require.NoError(t, err)

	// Verify VM was started with correct config.
	assert.Equal(t, "sandbox-test-agent", vmRunner.startCfg.Name)
	assert.Equal(t, "test-image:latest", vmRunner.startCfg.Image)
	assert.Equal(t, uint32(2), vmRunner.startCfg.CPUs)
	assert.Equal(t, uint32(2048), vmRunner.startCfg.Memory)
	assert.Equal(t, "/tmp/workspace", vmRunner.startCfg.WorkspacePath)
	assert.Equal(t, map[string]string{"TEST_KEY": "secret123", "GIT_TERMINAL_PROMPT": "0"}, vmRunner.startCfg.EnvVars)

	// Verify terminal session was started.
	assert.Equal(t, "127.0.0.1", sessionRunner.runOpts.Host)
	assert.Equal(t, uint16(2222), sessionRunner.runOpts.Port)
	assert.Equal(t, "sandbox", sessionRunner.runOpts.User)
	assert.Equal(t, []string{"test-cmd"}, sessionRunner.runOpts.Command)

	// Verify VM was stopped.
	assert.True(t, mvm.stopped)
}

func TestSandboxRunner_Run_AgentNotFound(t *testing.T) {
	t.Parallel()

	runner := NewSandboxRunner(SandboxDeps{
		Registry:      &mockRegistry{agents: map[string]agent.Agent{}},
		VMRunner:      &mockVMRunner{},
		SessionRunner: &mockSessionRunner{},
		Config:        &SandboxConfig{},
		EnvProvider:   &mockEnvProvider{},
		Logger:        testLogger(),
	})

	err := runner.Run(context.Background(), "nonexistent", RunOpts{
		Terminal: &mockTerminal{},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent not found")
}

func TestSandboxRunner_Run_CLIOverrides(t *testing.T) {
	t.Parallel()

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
		Registry:      &mockRegistry{agents: map[string]agent.Agent{"test": testAgent}},
		VMRunner:      vmRunner,
		SessionRunner: &mockSessionRunner{},
		Config:        &SandboxConfig{},
		EnvProvider:   &mockEnvProvider{},
		Logger:        testLogger(),
	})

	err := runner.Run(context.Background(), "test", RunOpts{
		CPUs:          4,
		Memory:        8192,
		ImageOverride: "custom:v2",
		EgressProfile: string(egress.ProfilePermissive),
		Terminal:      &mockTerminal{},
	})

	require.NoError(t, err)
	assert.Equal(t, "custom:v2", vmRunner.startCfg.Image)
	assert.Equal(t, uint32(4), vmRunner.startCfg.CPUs)
	assert.Equal(t, uint32(8192), vmRunner.startCfg.Memory)
}

func TestSandboxRunner_Run_ReviewEnabled_UsesSnapshotPath(t *testing.T) {
	t.Parallel()

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
			Cleanup:      func() error { return nil },
		},
	}
	differ := &mockDiffer{changes: nil} // No changes.

	runner := NewSandboxRunner(SandboxDeps{
		Registry:        &mockRegistry{agents: map[string]agent.Agent{"test": testAgent}},
		VMRunner:        vmRunner,
		SessionRunner:   &mockSessionRunner{},
		Config:          &SandboxConfig{},
		EnvProvider:     &mockEnvProvider{},
		Logger:          testLogger(),
		WorkspaceCloner: cloner,
		Differ:          differ,
		Reviewer:        &mockReviewer{},
		Flusher:         &mockFlusher{},
	})

	err := runner.Run(context.Background(), "test", RunOpts{
		Workspace:     workspaceDir,
		EgressProfile: string(egress.ProfilePermissive),
		Snapshot:      SnapshotOpts{Enabled: true},
		Terminal:      &mockTerminal{},
	})
	require.NoError(t, err)

	// VM should receive snapshot path, not the original.
	assert.True(t, cloner.createCalled)
	assert.Equal(t, snapshotDir, vmRunner.startCfg.WorkspacePath)
}

func TestSandboxRunner_Run_ReviewDisabled_UsesOriginalPath(t *testing.T) {
	t.Parallel()

	testAgent := agent.Agent{
		Name:    "test",
		Image:   "img:latest",
		Command: []string{"cmd"},
	}

	mvm := &mockVM{sshPort: 5555, sshKeyPath: "/tmp/key"}
	vmRunner := &mockVMRunner{vm: mvm}

	runner := NewSandboxRunner(SandboxDeps{
		Registry:      &mockRegistry{agents: map[string]agent.Agent{"test": testAgent}},
		VMRunner:      vmRunner,
		SessionRunner: &mockSessionRunner{},
		Config:        &SandboxConfig{},
		EnvProvider:   &mockEnvProvider{},
		Logger:        testLogger(),
	})

	err := runner.Run(context.Background(), "test", RunOpts{
		Workspace:     "/my/workspace",
		EgressProfile: string(egress.ProfilePermissive),
		Snapshot:      SnapshotOpts{Enabled: false},
		Terminal:      &mockTerminal{},
	})
	require.NoError(t, err)

	// VM should receive original path.
	assert.Equal(t, "/my/workspace", vmRunner.startCfg.WorkspacePath)
}

func TestSandboxRunner_Run_ReviewWithChanges_FlushesAccepted(t *testing.T) {
	t.Parallel()

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
			Cleanup:      func() error { return nil },
		},
	}
	differ := &mockDiffer{changes: changes}
	reviewer := &mockReviewer{}
	flusher := &mockFlusher{}

	runner := NewSandboxRunner(SandboxDeps{
		Registry:        &mockRegistry{agents: map[string]agent.Agent{"test": testAgent}},
		VMRunner:        vmRunner,
		SessionRunner:   &mockSessionRunner{},
		Config:          &SandboxConfig{},
		EnvProvider:     &mockEnvProvider{},
		Logger:          testLogger(),
		WorkspaceCloner: cloner,
		Differ:          differ,
		Reviewer:        reviewer,
		Flusher:         flusher,
	})

	err := runner.Run(context.Background(), "test", RunOpts{
		Workspace:     workspaceDir,
		EgressProfile: string(egress.ProfilePermissive),
		Snapshot:      SnapshotOpts{Enabled: true},
		Terminal:      &mockTerminal{},
	})
	require.NoError(t, err)

	assert.True(t, reviewer.reviewCalled)
	assert.True(t, flusher.flushCalled)
	assert.Len(t, flusher.flushed, 1)
	assert.Equal(t, "file.go", flusher.flushed[0].RelPath)
}

func TestSandboxRunner_Run_ReviewEmptyDiff_SkipsReview(t *testing.T) {
	t.Parallel()

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
			Cleanup:      func() error { return nil },
		},
	}
	reviewer := &mockReviewer{}

	runner := NewSandboxRunner(SandboxDeps{
		Registry:        &mockRegistry{agents: map[string]agent.Agent{"test": testAgent}},
		VMRunner:        &mockVMRunner{vm: mvm},
		SessionRunner:   &mockSessionRunner{},
		Config:          &SandboxConfig{},
		EnvProvider:     &mockEnvProvider{},
		Logger:          testLogger(),
		WorkspaceCloner: cloner,
		Differ:          &mockDiffer{changes: nil},
		Reviewer:        reviewer,
		Flusher:         &mockFlusher{},
	})

	err := runner.Run(context.Background(), "test", RunOpts{
		Workspace:     workspaceDir,
		EgressProfile: string(egress.ProfilePermissive),
		Snapshot:      SnapshotOpts{Enabled: true},
		Terminal:      &mockTerminal{},
	})
	require.NoError(t, err)

	// Reviewer should not be called when there are no changes.
	assert.False(t, reviewer.reviewCalled)
}

func TestSandboxRunner_Run_SnapshotCreationFails(t *testing.T) {
	t.Parallel()

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
		SessionRunner:   &mockSessionRunner{},
		Config:          &SandboxConfig{},
		EnvProvider:     &mockEnvProvider{},
		Logger:          testLogger(),
		WorkspaceCloner: cloner,
		Differ:          &mockDiffer{},
		Reviewer:        &mockReviewer{},
		Flusher:         &mockFlusher{},
	})

	err := runner.Run(context.Background(), "test", RunOpts{
		Workspace:     t.TempDir(),
		EgressProfile: string(egress.ProfilePermissive),
		Snapshot:      SnapshotOpts{Enabled: true},
		Terminal:      &mockTerminal{},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "creating workspace snapshot")
}

func TestSandboxRunner_Run_VMStoppedBeforeReview(t *testing.T) {
	t.Parallel()

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
			Cleanup:      func() error { return nil },
		},
	}

	// Use a reviewer that checks VM state.
	orderCheckReviewer := &orderCheckingReviewer{vm: mvm}

	runner := NewSandboxRunner(SandboxDeps{
		Registry:        &mockRegistry{agents: map[string]agent.Agent{"test": testAgent}},
		VMRunner:        &mockVMRunner{vm: mvm},
		SessionRunner:   &mockSessionRunner{},
		Config:          &SandboxConfig{},
		EnvProvider:     &mockEnvProvider{},
		Logger:          testLogger(),
		WorkspaceCloner: cloner,
		Differ:          &mockDiffer{changes: changes},
		Reviewer:        orderCheckReviewer,
		Flusher:         &mockFlusher{},
	})

	err := runner.Run(context.Background(), "test", RunOpts{
		Workspace:     workspaceDir,
		EgressProfile: string(egress.ProfilePermissive),
		Snapshot:      SnapshotOpts{Enabled: true},
		Terminal:      &mockTerminal{},
	})
	require.NoError(t, err)

	assert.True(t, mvm.stopped, "VM should be stopped")
	assert.True(t, orderCheckReviewer.vmWasStoppedWhenCalled, "VM should be stopped before review")
}

func TestSandboxRunner_Run_NilMatcherDefaultsToNop(t *testing.T) {
	t.Parallel()

	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	testAgent := agent.Agent{
		Name:    "test",
		Image:   "img:latest",
		Command: []string{"cmd"},
	}

	mvm := &mockVM{sshPort: 1111, sshKeyPath: "/tmp/key"}
	cloner := &mockWorkspaceCloner{
		snapshot: &workspace.Snapshot{
			OriginalPath: workspaceDir,
			SnapshotPath: snapshotDir,
			Cleanup:      func() error { return nil },
		},
	}

	runner := NewSandboxRunner(SandboxDeps{
		Registry:        &mockRegistry{agents: map[string]agent.Agent{"test": testAgent}},
		VMRunner:        &mockVMRunner{vm: mvm},
		SessionRunner:   &mockSessionRunner{},
		Config:          &SandboxConfig{},
		EnvProvider:     &mockEnvProvider{},
		Logger:          testLogger(),
		WorkspaceCloner: cloner,
		Differ:          &mockDiffer{},
		Reviewer:        &mockReviewer{},
		Flusher:         &mockFlusher{},
	})

	// Pass nil matchers — they should default to NopMatcher.
	err := runner.Run(context.Background(), "test", RunOpts{
		Workspace:     workspaceDir,
		EgressProfile: string(egress.ProfilePermissive),
		Terminal:      &mockTerminal{},
		Snapshot: SnapshotOpts{
			Enabled:         true,
			SnapshotMatcher: nil,
			DiffMatcher:     nil,
		},
	})
	require.NoError(t, err)

	// The cloner should have received NopMatcher (not nil).
	assert.Equal(t, snapshot.NopMatcher, cloner.receivedMatch)
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

// orderCheckingDiffer records whether the VM was stopped when Diff is called.
type orderCheckingDiffer struct {
	vm                     *mockVM
	vmWasStoppedWhenCalled bool
	changes                []snapshot.FileChange
}

func (d *orderCheckingDiffer) Diff(_, _ string, _ snapshot.Matcher) ([]snapshot.FileChange, error) {
	d.vmWasStoppedWhenCalled = d.vm.stopped
	return d.changes, nil
}

// ---------------------------------------------------------------------------
// Lifecycle method tests
// ---------------------------------------------------------------------------

func TestSandboxRunner_Prepare_Success(t *testing.T) {
	t.Parallel()

	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	testAgent := agent.Agent{
		Name:          "test-agent",
		Image:         "test-image:latest",
		Command:       []string{"test-cmd"},
		EnvForward:    []string{"TEST_KEY"},
		DefaultCPUs:   2,
		DefaultMemory: 2048,
	}

	mvm := &mockVM{sshPort: 2222, sshKeyPath: "/tmp/key"}
	vmRunner := &mockVMRunner{vm: mvm}
	cloner := &mockWorkspaceCloner{
		snapshot: &workspace.Snapshot{
			OriginalPath: workspaceDir,
			SnapshotPath: snapshotDir,
			Cleanup:      func() error { return nil },
		},
	}

	runner := NewSandboxRunner(SandboxDeps{
		Registry: &mockRegistry{agents: map[string]agent.Agent{
			"test-agent": testAgent,
		}},
		VMRunner:        vmRunner,
		SessionRunner:   &mockSessionRunner{},
		Config:          &SandboxConfig{},
		EnvProvider:     &mockEnvProvider{vars: []string{"TEST_KEY=secret123"}},
		Logger:          testLogger(),
		WorkspaceCloner: cloner,
		Differ:          &mockDiffer{},
		Reviewer:        &mockReviewer{},
		Flusher:         &mockFlusher{},
	})

	sb, err := runner.Prepare(context.Background(), "test-agent", RunOpts{
		Workspace:     workspaceDir,
		EgressProfile: string(egress.ProfilePermissive),
		Snapshot:      SnapshotOpts{Enabled: true},
	})
	require.NoError(t, err)
	defer func() { _ = sb.Cleanup() }()

	assert.Equal(t, "test-agent", sb.Agent.Name)
	assert.NotNil(t, sb.VM)
	assert.Equal(t, "sandbox-test-agent", sb.VMConfig.Name)
	assert.Equal(t, snapshotDir, sb.WorkspacePath)
	assert.NotNil(t, sb.Snapshot)
	assert.Equal(t, map[string]string{"TEST_KEY": "secret123", "GIT_TERMINAL_PROMPT": "0"}, sb.EnvVars)
	assert.Equal(t, snapshot.NopMatcher, sb.DiffMatcher)
	assert.Equal(t, snapshotDir, vmRunner.startCfg.WorkspacePath)
	assert.True(t, cloner.createCalled)
}

func TestSandboxRunner_Prepare_AgentNotFound(t *testing.T) {
	t.Parallel()

	vmRunner := &mockVMRunner{}

	runner := NewSandboxRunner(SandboxDeps{
		Registry:      &mockRegistry{agents: map[string]agent.Agent{}},
		VMRunner:      vmRunner,
		SessionRunner: &mockSessionRunner{},
		Config:        &SandboxConfig{},
		EnvProvider:   &mockEnvProvider{},
		Logger:        testLogger(),
	})

	_, err := runner.Prepare(context.Background(), "nonexistent", RunOpts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolving agent")

	// VM should never have been started.
	assert.Equal(t, domvm.VMConfig{}, vmRunner.startCfg)
}

func TestSandboxRunner_Attach_CallsSessionRunner(t *testing.T) {
	t.Parallel()

	mvm := &mockVM{sshPort: 2222, sshKeyPath: "/tmp/key"}
	sessionRunner := &mockSessionRunner{}
	terminal := &mockTerminal{}

	runner := NewSandboxRunner(SandboxDeps{
		SessionRunner: sessionRunner,
		Logger:        testLogger(),
	})

	sb := &Sandbox{
		Agent: agent.Agent{Command: []string{"test-cmd"}},
		VM:    mvm,
	}

	err := runner.Attach(context.Background(), sb, terminal)
	require.NoError(t, err)

	assert.Equal(t, "127.0.0.1", sessionRunner.runOpts.Host)
	assert.Equal(t, uint16(2222), sessionRunner.runOpts.Port)
	assert.Equal(t, "sandbox", sessionRunner.runOpts.User)
	assert.Equal(t, "/tmp/key", sessionRunner.runOpts.KeyPath)
	assert.Equal(t, []string{"test-cmd"}, sessionRunner.runOpts.Command)
	assert.Equal(t, terminal, sessionRunner.runOpts.Terminal)
}

func TestSandboxRunner_Stop_StopsVM(t *testing.T) {
	t.Parallel()

	mvm := &mockVM{}

	runner := NewSandboxRunner(SandboxDeps{
		Logger: testLogger(),
	})

	sb := &Sandbox{VM: mvm}

	err := runner.Stop(sb)
	require.NoError(t, err)
	assert.True(t, mvm.stopped)
}

func TestSandboxRunner_Changes_ReturnsDiff(t *testing.T) {
	t.Parallel()

	origDir := t.TempDir()
	snapDir := t.TempDir()

	differ := &mockDiffer{
		changes: []snapshot.FileChange{
			{RelPath: "main.go", Kind: snapshot.Modified, Hash: "abc"},
		},
	}

	runner := NewSandboxRunner(SandboxDeps{
		Differ: differ,
		Logger: testLogger(),
	})

	sb := &Sandbox{
		Snapshot: &workspace.Snapshot{
			OriginalPath: origDir,
			SnapshotPath: snapDir,
			Cleanup:      func() error { return nil },
		},
		DiffMatcher: snapshot.NopMatcher,
	}

	changes, err := runner.Changes(sb)
	require.NoError(t, err)
	require.Len(t, changes, 1)
	assert.Equal(t, "main.go", changes[0].RelPath)
}

func TestSandboxRunner_Changes_NilSnapshot_ReturnsNil(t *testing.T) {
	t.Parallel()

	differ := &mockDiffer{
		changes: []snapshot.FileChange{
			{RelPath: "should-not-appear.go"},
		},
	}

	runner := NewSandboxRunner(SandboxDeps{
		Differ: differ,
		Logger: testLogger(),
	})

	sb := &Sandbox{Snapshot: nil}

	changes, err := runner.Changes(sb)
	assert.NoError(t, err)
	assert.Nil(t, changes)
}

func TestSandboxRunner_Flush_AppliesAccepted(t *testing.T) {
	t.Parallel()

	origDir := t.TempDir()
	snapDir := t.TempDir()

	flusher := &mockFlusher{}

	runner := NewSandboxRunner(SandboxDeps{
		Flusher: flusher,
		Logger:  testLogger(),
	})

	sb := &Sandbox{
		Snapshot: &workspace.Snapshot{
			OriginalPath: origDir,
			SnapshotPath: snapDir,
			Cleanup:      func() error { return nil },
		},
	}

	accepted := []snapshot.FileChange{
		{RelPath: "file.go", Kind: snapshot.Modified, Hash: "abc123"},
	}

	err := runner.Flush(sb, accepted)
	require.NoError(t, err)
	assert.True(t, flusher.flushCalled)
	assert.Len(t, flusher.flushed, 1)
}

func TestSandboxRunner_Flush_NilSnapshot_Noop(t *testing.T) {
	t.Parallel()

	flusher := &mockFlusher{}

	runner := NewSandboxRunner(SandboxDeps{
		Flusher: flusher,
		Logger:  testLogger(),
	})

	sb := &Sandbox{Snapshot: nil}

	err := runner.Flush(sb, []snapshot.FileChange{{RelPath: "file.go"}})
	assert.NoError(t, err)
	assert.False(t, flusher.flushCalled)
}

func TestSandboxRunner_LifecycleEndToEnd(t *testing.T) {
	t.Parallel()

	workspaceDir := t.TempDir()
	snapshotDir := t.TempDir()

	testAgent := agent.Agent{
		Name:          "test-agent",
		Image:         "test-image:latest",
		Command:       []string{"test-cmd"},
		EnvForward:    []string{"KEY"},
		DefaultCPUs:   2,
		DefaultMemory: 2048,
	}

	mvm := &mockVM{sshPort: 3333, sshKeyPath: "/tmp/key"}
	vmRunner := &mockVMRunner{vm: mvm}
	sessionRunner := &mockSessionRunner{}
	cloner := &mockWorkspaceCloner{
		snapshot: &workspace.Snapshot{
			OriginalPath: workspaceDir,
			SnapshotPath: snapshotDir,
			Cleanup:      func() error { return nil },
		},
	}
	changes := []snapshot.FileChange{
		{RelPath: "new.go", Kind: snapshot.Added, Hash: "xyz"},
	}
	orderDiffer := &orderCheckingDiffer{vm: mvm, changes: changes}
	reviewer := &mockReviewer{}
	flusher := &mockFlusher{}
	terminal := &mockTerminal{}

	runner := NewSandboxRunner(SandboxDeps{
		Registry: &mockRegistry{agents: map[string]agent.Agent{
			"test-agent": testAgent,
		}},
		VMRunner:        vmRunner,
		SessionRunner:   sessionRunner,
		Config:          &SandboxConfig{},
		EnvProvider:     &mockEnvProvider{vars: []string{"KEY=val"}},
		Logger:          testLogger(),
		WorkspaceCloner: cloner,
		Differ:          orderDiffer,
		Reviewer:        reviewer,
		Flusher:         flusher,
	})

	// 1. Prepare
	sb, err := runner.Prepare(context.Background(), "test-agent", RunOpts{
		Workspace:     workspaceDir,
		EgressProfile: string(egress.ProfilePermissive),
		Snapshot:      SnapshotOpts{Enabled: true},
	})
	require.NoError(t, err)
	defer func() { _ = sb.Cleanup() }()

	// 2. Attach
	err = runner.Attach(context.Background(), sb, terminal)
	require.NoError(t, err)
	assert.NotZero(t, sessionRunner.runOpts.Port, "session should have been called")

	// 3. Stop
	err = runner.Stop(sb)
	require.NoError(t, err)
	assert.True(t, mvm.stopped)

	// 4. Changes
	gotChanges, err := runner.Changes(sb)
	require.NoError(t, err)
	require.Len(t, gotChanges, 1)
	assert.True(t, orderDiffer.vmWasStoppedWhenCalled, "VM should be stopped before diff")

	// 5. Review
	result, err := reviewer.Review(gotChanges)
	require.NoError(t, err)

	// 6. Flush
	err = runner.Flush(sb, result.Accepted)
	require.NoError(t, err)
	assert.True(t, flusher.flushCalled)
	assert.Len(t, flusher.flushed, 1)

	// 7. Cleanup
	err = sb.Cleanup()
	assert.NoError(t, err)
}
