// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package sandbox

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"

	"github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/bytesize"
	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
	"github.com/stacklok/brood-box/pkg/domain/egress"
	"github.com/stacklok/brood-box/pkg/domain/hostservice"
	"github.com/stacklok/brood-box/pkg/domain/session"
	"github.com/stacklok/brood-box/pkg/domain/snapshot"
	domvm "github.com/stacklok/brood-box/pkg/domain/vm"
	"github.com/stacklok/brood-box/pkg/domain/workspace"
)

func boolPtr(b bool) *bool { return &b }

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
	rootFSPath string
	sshKeyPath string
	sshHostKey ssh.PublicKey
}

func (m *mockVM) Stop(_ context.Context) error {
	m.stopped = true
	return nil
}

func (m *mockVM) SSHPort() uint16           { return m.sshPort }
func (m *mockVM) DataDir() string           { return m.dataDir }
func (m *mockVM) SSHKeyPath() string        { return m.sshKeyPath }
func (m *mockVM) SSHHostKey() ssh.PublicKey { return m.sshHostKey }
func (m *mockVM) RootFSPath() string        { return m.rootFSPath }

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

// mockMCPProvider implements hostservice.Provider for testing.
type mockMCPProvider struct {
	services []hostservice.Service
	err      error
	called   bool
}

func (m *mockMCPProvider) Services(_ context.Context) ([]hostservice.Service, error) {
	m.called = true
	return m.services, m.err
}

func (m *mockMCPProvider) Close() error { return nil }

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
		DefaultMemory: bytesize.ByteSize(2048),
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
		SessionID:     "abcd1234",
		Terminal:      &mockTerminal{},
	})

	require.NoError(t, err)

	// Verify VM was started with correct config (name includes workspace hash + session ID).
	assert.Equal(t, VMName("test-agent", "/tmp/workspace", "abcd1234"), vmRunner.startCfg.Name)
	assert.Equal(t, "test-image:latest", vmRunner.startCfg.Image)
	assert.Equal(t, uint32(2), vmRunner.startCfg.CPUs)
	assert.Equal(t, bytesize.ByteSize(2048), vmRunner.startCfg.Memory)
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
		SessionID: "abcd1234",
		Terminal:  &mockTerminal{},
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
		DefaultMemory: bytesize.ByteSize(2048),
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
		SessionID:     "abcd1234",
		Terminal:      &mockTerminal{},
	})

	require.NoError(t, err)
	assert.Equal(t, "custom:v2", vmRunner.startCfg.Image)
	assert.Equal(t, uint32(4), vmRunner.startCfg.CPUs)
	assert.Equal(t, bytesize.ByteSize(8192), vmRunner.startCfg.Memory)
}

func TestSandboxRunner_Run_CommandResolution(t *testing.T) {
	t.Parallel()

	baseCommand := []string{"cmd"}

	testAgent := agent.Agent{
		Name:    "test",
		Image:   "img:latest",
		Command: baseCommand,
	}

	newRunner := func() (*SandboxRunner, *mockSessionRunner) {
		sessionRunner := &mockSessionRunner{}
		runner := NewSandboxRunner(SandboxDeps{
			Registry:      &mockRegistry{agents: map[string]agent.Agent{"test": testAgent}},
			VMRunner:      &mockVMRunner{vm: &mockVM{sshPort: 7777, sshKeyPath: "/tmp/key"}},
			SessionRunner: sessionRunner,
			Config:        &SandboxConfig{},
			EnvProvider:   &mockEnvProvider{},
			Logger:        testLogger(),
		})
		return runner, sessionRunner
	}

	tests := []struct {
		name            string
		overrideCommand []string
		commandArgs     []string
		expected        []string
		expectErr       bool
	}{
		{
			name:        "append args",
			commandArgs: []string{"--flag", "value"},
			expected:    []string{"cmd", "--flag", "value"},
		},
		{
			name:            "override command",
			overrideCommand: []string{"other", "--mode"},
			expected:        []string{"other", "--mode"},
		},
		{
			name:            "override with args",
			overrideCommand: []string{"other"},
			commandArgs:     []string{"--fast"},
			expected:        []string{"other", "--fast"},
		},
		{
			name:        "reject nul",
			commandArgs: []string{"bad\x00arg"},
			expectErr:   true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			runner, sessionRunner := newRunner()

			err := runner.Run(context.Background(), "test", RunOpts{
				Terminal:        &mockTerminal{},
				CommandArgs:     tt.commandArgs,
				CommandOverride: tt.overrideCommand,
				EgressProfile:   string(egress.ProfilePermissive),
				SessionID:       "abcd1234",
			})

			if tt.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, sessionRunner.runOpts.Command)
		})
	}
}

func TestSandboxRunner_Run_CommandArgs_DoesNotMutateBase(t *testing.T) {
	t.Parallel()

	baseCommand := make([]string, 1, 2)
	baseCommand[0] = "cmd"

	testAgent := agent.Agent{
		Name:    "test",
		Image:   "img:latest",
		Command: baseCommand,
	}

	sessionRunner := &mockSessionRunner{}
	runner := NewSandboxRunner(SandboxDeps{
		Registry:      &mockRegistry{agents: map[string]agent.Agent{"test": testAgent}},
		VMRunner:      &mockVMRunner{vm: &mockVM{sshPort: 8888, sshKeyPath: "/tmp/key"}},
		SessionRunner: sessionRunner,
		Config:        &SandboxConfig{},
		EnvProvider:   &mockEnvProvider{},
		Logger:        testLogger(),
	})

	err := runner.Run(context.Background(), "test", RunOpts{
		Terminal:      &mockTerminal{},
		CommandArgs:   []string{"--flag"},
		EgressProfile: string(egress.ProfilePermissive),
		SessionID:     "abcd1234",
	})
	require.NoError(t, err)

	// Ensure the original command backing array wasn't modified.
	extra := baseCommand[:2]
	assert.Equal(t, "", extra[1])
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
		SessionID:     "abcd1234",
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
		SessionID:     "abcd1234",
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
		SessionID:     "abcd1234",
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
		SessionID:     "abcd1234",
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
		SessionID:     "abcd1234",
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
		SessionID:     "abcd1234",
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
		SessionID:     "abcd1234",
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
		DefaultMemory: bytesize.ByteSize(2048),
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
		SessionID:     "abcd1234",
	})
	require.NoError(t, err)
	defer func() { _ = sb.Cleanup() }()

	assert.Equal(t, "test-agent", sb.Agent.Name)
	assert.NotNil(t, sb.VM)
	assert.Equal(t, VMName("test-agent", snapshotDir, "abcd1234"), sb.VMConfig.Name)
	assert.Equal(t, snapshotDir, sb.WorkspacePath)
	assert.NotNil(t, sb.Snapshot)
	assert.Equal(t, map[string]string{"TEST_KEY": "secret123", "GIT_TERMINAL_PROMPT": "0"}, sb.EnvVars)
	assert.Equal(t, snapshot.NopMatcher, sb.DiffMatcher)
	assert.Equal(t, snapshotDir, vmRunner.startCfg.WorkspacePath)
	assert.True(t, cloner.createCalled)
}

func TestSandboxRunner_Prepare_EnvForwardExtra(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		agentEnvForward []string
		envForwardExtra []string
		envVars         []string
		wantKeys        []string
		desc            string
	}{
		{
			name:            "extra patterns merge with agent patterns",
			agentEnvForward: []string{"AGENT_KEY"},
			envForwardExtra: []string{"CLI_KEY"},
			envVars:         []string{"AGENT_KEY=a", "CLI_KEY=b"},
			wantKeys:        []string{"AGENT_KEY", "CLI_KEY"},
			desc:            "both agent and CLI-forwarded vars appear",
		},
		{
			name:            "duplicate pattern is deduplicated",
			agentEnvForward: []string{"SHARED"},
			envForwardExtra: []string{"SHARED", "EXTRA"},
			envVars:         []string{"SHARED=s", "EXTRA=e"},
			wantKeys:        []string{"SHARED", "EXTRA"},
			desc:            "overlapping pattern doesn't cause duplicate matching",
		},
		{
			name:            "nil extra does not alter agent patterns",
			agentEnvForward: []string{"ONLY_AGENT"},
			envForwardExtra: nil,
			envVars:         []string{"ONLY_AGENT=x"},
			wantKeys:        []string{"ONLY_AGENT"},
			desc:            "nil EnvForwardExtra is a no-op",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			testAgent := agent.Agent{
				Name:          "test-agent",
				Image:         "test-image:latest",
				Command:       []string{"test-cmd"},
				EnvForward:    tt.agentEnvForward,
				DefaultCPUs:   2,
				DefaultMemory: bytesize.ByteSize(2048),
			}

			mvm := &mockVM{sshPort: 2222, sshKeyPath: "/tmp/key"}
			runner := NewSandboxRunner(SandboxDeps{
				Registry: &mockRegistry{agents: map[string]agent.Agent{
					"test-agent": testAgent,
				}},
				VMRunner:      &mockVMRunner{vm: mvm},
				SessionRunner: &mockSessionRunner{},
				Config:        &SandboxConfig{},
				EnvProvider:   &mockEnvProvider{vars: tt.envVars},
				Logger:        testLogger(),
			})

			sb, err := runner.Prepare(context.Background(), "test-agent", RunOpts{
				Workspace:       t.TempDir(),
				EgressProfile:   string(egress.ProfilePermissive),
				SessionID:       "abcd1234",
				EnvForwardExtra: tt.envForwardExtra,
			})
			require.NoError(t, err)
			defer func() { _ = sb.Cleanup() }()

			for _, key := range tt.wantKeys {
				assert.Contains(t, sb.EnvVars, key, tt.desc)
			}
		})
	}
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

	_, err := runner.Prepare(context.Background(), "nonexistent", RunOpts{SessionID: "abcd1234"})
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
	assert.Nil(t, sessionRunner.runOpts.HostPublicKey, "nil host key should be forwarded as nil")
}

func TestSandboxRunner_Attach_PlumbsHostKey(t *testing.T) {
	t.Parallel()

	// Generate a real ECDSA key so we have a non-nil ssh.PublicKey.
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	hostPub, err := ssh.NewPublicKey(&ecKey.PublicKey)
	require.NoError(t, err)

	mvm := &mockVM{sshPort: 3333, sshKeyPath: "/tmp/key", sshHostKey: hostPub}
	sessionRunner := &mockSessionRunner{}

	runner := NewSandboxRunner(SandboxDeps{
		SessionRunner: sessionRunner,
		Logger:        testLogger(),
	})

	sb := &Sandbox{
		Agent: agent.Agent{Command: []string{"cmd"}},
		VM:    mvm,
	}

	err = runner.Attach(context.Background(), sb, &mockTerminal{})
	require.NoError(t, err)

	require.NotNil(t, sessionRunner.runOpts.HostPublicKey, "host key should be plumbed to session opts")
	assert.Equal(t, hostPub.Marshal(), sessionRunner.runOpts.HostPublicKey.Marshal())
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
		DefaultMemory: bytesize.ByteSize(2048),
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
		SessionID:     "abcd1234",
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

func TestVMName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		agent     string
		workspace string
		sessionID string
		want      string
	}{
		{
			name:      "empty workspace uses agent and session only",
			agent:     "claude-code",
			workspace: "",
			sessionID: "abcd1234",
			want:      "sandbox-claude-code-abcd1234",
		},
		{
			name:      "workspace path produces hash and session suffix",
			agent:     "claude-code",
			workspace: "/home/user/project",
			sessionID: "abcd1234",
			want:      VMName("claude-code", "/home/user/project", "abcd1234"),
		},
		{
			name:      "different workspaces produce different names",
			agent:     "claude-code",
			workspace: "/home/user/other",
			sessionID: "abcd1234",
			want:      VMName("claude-code", "/home/user/other", "abcd1234"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := VMName(tt.agent, tt.workspace, tt.sessionID)
			assert.Equal(t, tt.want, got)
		})
	}

	// Verify different workspaces produce different VM names.
	name1 := VMName("claude-code", "/project-a", "abcd1234")
	name2 := VMName("claude-code", "/project-b", "abcd1234")
	assert.NotEqual(t, name1, name2)

	// Verify same inputs are deterministic.
	assert.Equal(t, VMName("claude-code", "/project-a", "abcd1234"), VMName("claude-code", "/project-a", "abcd1234"))

	// Verify hash suffix format (8 hex workspace hash + session ID).
	name := VMName("test", "/workspace", "abcd1234")
	assert.Regexp(t, `^sandbox-test-[0-9a-f]{8}-[0-9a-f]{8}$`, name)
}

func TestVMName_ConcurrentSessionsUnique(t *testing.T) {
	t.Parallel()

	// Same agent and workspace with different session IDs must produce different names.
	name1 := VMName("claude-code", "/home/user/project", "aaaaaaaa")
	name2 := VMName("claude-code", "/home/user/project", "bbbbbbbb")
	assert.NotEqual(t, name1, name2)
}

func TestSandboxRunner_Prepare_MissingSessionID(t *testing.T) {
	t.Parallel()

	runner := NewSandboxRunner(SandboxDeps{
		Registry:      &mockRegistry{agents: map[string]agent.Agent{"test": {Name: "test", Image: "img", Command: []string{"cmd"}}}},
		VMRunner:      &mockVMRunner{},
		SessionRunner: &mockSessionRunner{},
		Config:        &SandboxConfig{},
		EnvProvider:   &mockEnvProvider{},
		Logger:        testLogger(),
	})

	_, err := runner.Prepare(context.Background(), "test", RunOpts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "session ID must be 1-16 hex characters")
}

func TestSandboxRunner_Prepare_InvalidSessionID(t *testing.T) {
	t.Parallel()

	runner := NewSandboxRunner(SandboxDeps{
		Registry:      &mockRegistry{agents: map[string]agent.Agent{"test": {Name: "test", Image: "img", Command: []string{"cmd"}}}},
		VMRunner:      &mockVMRunner{},
		SessionRunner: &mockSessionRunner{},
		Config:        &SandboxConfig{},
		EnvProvider:   &mockEnvProvider{},
		Logger:        testLogger(),
	})

	tests := []struct {
		name      string
		sessionID string
	}{
		{name: "uppercase hex", sessionID: "ABCD1234"},
		{name: "non-hex chars", sessionID: "ghijklmn"},
		{name: "too long", sessionID: "abcdef0123456789a"},
		{name: "special chars", sessionID: "abcd-123"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := runner.Prepare(context.Background(), "test", RunOpts{SessionID: tt.sessionID})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "session ID must be 1-16 hex characters")
		})
	}
}

func TestSandboxRunner_Prepare_MCPFailure_WarnsAndContinues(t *testing.T) {
	t.Parallel()

	testAgent := agent.Agent{
		Name:          "test",
		Image:         "img:latest",
		Command:       []string{"cmd"},
		DefaultCPUs:   2,
		DefaultMemory: bytesize.ByteSize(2048),
	}

	mvm := &mockVM{sshPort: 2222, sshKeyPath: "/tmp/key"}
	mcpProvider := &mockMCPProvider{
		err: fmt.Errorf("no available runtime found"),
	}

	runner := NewSandboxRunner(SandboxDeps{
		Registry:      &mockRegistry{agents: map[string]agent.Agent{"test": testAgent}},
		VMRunner:      &mockVMRunner{vm: mvm},
		SessionRunner: &mockSessionRunner{},
		Config:        &SandboxConfig{},
		EnvProvider:   &mockEnvProvider{},
		Logger:        testLogger(),
		MCPProvider:   mcpProvider,
	})

	sb, err := runner.Prepare(t.Context(), "test", RunOpts{
		Workspace:     "/tmp/workspace",
		EgressProfile: string(egress.ProfilePermissive),
		SessionID:     "abcd1234",
	})
	require.NoError(t, err, "MCP failure should not be fatal")
	defer func() { _ = sb.Cleanup() }()

	assert.True(t, mcpProvider.called, "MCP provider should have been called")
	assert.Empty(t, sb.VMConfig.HostServices, "no host services should be configured on MCP failure")
}

// ---------------------------------------------------------------------------
// Helper function unit tests
// ---------------------------------------------------------------------------

func TestMergeEnvPatterns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		base     []string
		extra    []string
		expected []string
	}{
		{
			name:     "empty base",
			base:     nil,
			extra:    []string{"A", "B"},
			expected: []string{"A", "B"},
		},
		{
			name:     "empty extra",
			base:     []string{"X", "Y"},
			extra:    nil,
			expected: []string{"X", "Y"},
		},
		{
			name:     "both nil",
			base:     nil,
			extra:    nil,
			expected: []string{},
		},
		{
			name:     "dedup preserves first occurrence",
			base:     []string{"A", "B"},
			extra:    []string{"B", "C"},
			expected: []string{"A", "B", "C"},
		},
		{
			name:     "order preserved",
			base:     []string{"Z", "A"},
			extra:    []string{"M", "Z"},
			expected: []string{"Z", "A", "M"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := mergeEnvPatterns(tt.base, tt.extra)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestResolveCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		base      []string
		override  []string
		args      []string
		expected  []string
		expectErr bool
	}{
		{
			name:     "base only",
			base:     []string{"cmd"},
			override: nil,
			args:     nil,
			expected: []string{"cmd"},
		},
		{
			name:     "override only",
			base:     []string{"cmd"},
			override: []string{"other"},
			args:     nil,
			expected: []string{"other"},
		},
		{
			name:     "override with args",
			base:     []string{"cmd"},
			override: []string{"other"},
			args:     []string{"--flag"},
			expected: []string{"other", "--flag"},
		},
		{
			name:     "base with args",
			base:     []string{"cmd"},
			override: nil,
			args:     []string{"--flag", "val"},
			expected: []string{"cmd", "--flag", "val"},
		},
		{
			name:      "both empty returns error",
			base:      nil,
			override:  nil,
			args:      nil,
			expectErr: true,
		},
		{
			name:      "NUL byte returns error",
			base:      []string{"cmd"},
			override:  nil,
			args:      []string{"bad\x00arg"},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := resolveCommand(tt.base, tt.override, tt.args)
			if tt.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestResolveCommand_DoesNotMutateInputSlices(t *testing.T) {
	t.Parallel()

	base := []string{"cmd"}
	override := []string{"other"}
	args := []string{"--flag"}

	// Save copies.
	baseCopy := append([]string{}, base...)
	overrideCopy := append([]string{}, override...)
	argsCopy := append([]string{}, args...)

	_, err := resolveCommand(base, override, args)
	require.NoError(t, err)

	assert.Equal(t, baseCopy, base, "base should not be mutated")
	assert.Equal(t, overrideCopy, override, "override should not be mutated")
	assert.Equal(t, argsCopy, args, "args should not be mutated")
}

func TestResolveMCPConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		cfg         *SandboxConfig
		agentName   string
		wantGroup   string
		wantPort    uint16
		wantEnabled bool
	}{
		{
			name:        "zero config applies defaults",
			cfg:         &SandboxConfig{},
			agentName:   "test",
			wantGroup:   "default",
			wantPort:    4483,
			wantEnabled: true,
		},
		{
			name: "global config preserved",
			cfg: &SandboxConfig{
				MCP: domainconfig.MCPConfig{
					Group: "custom-group",
					Port:  9999,
				},
			},
			agentName:   "test",
			wantGroup:   "custom-group",
			wantPort:    9999,
			wantEnabled: true,
		},
		{
			name: "agent override disables MCP",
			cfg: &SandboxConfig{
				MCP: domainconfig.MCPConfig{
					Group: "global-group",
					Port:  5555,
				},
				AgentOverrides: map[string]domainconfig.AgentOverride{
					"test": {
						MCP: &domainconfig.MCPAgentOverride{
							Enabled: boolPtr(false),
						},
					},
				},
			},
			agentName:   "test",
			wantGroup:   "global-group",
			wantPort:    5555,
			wantEnabled: false,
		},
		{
			name: "empty override does not change enabled",
			cfg: &SandboxConfig{
				MCP: domainconfig.MCPConfig{
					Group: "global-group",
					Port:  5555,
				},
				AgentOverrides: map[string]domainconfig.AgentOverride{
					"test": {
						MCP: &domainconfig.MCPAgentOverride{},
					},
				},
			},
			agentName:   "test",
			wantGroup:   "global-group",
			wantPort:    5555,
			wantEnabled: true,
		},
		{
			name: "agent override re-enables MCP",
			cfg: &SandboxConfig{
				MCP: domainconfig.MCPConfig{
					Enabled: boolPtr(false),
					Group:   "global-group",
					Port:    5555,
				},
				AgentOverrides: map[string]domainconfig.AgentOverride{
					"test": {
						MCP: &domainconfig.MCPAgentOverride{
							Enabled: boolPtr(true),
						},
					},
				},
			},
			agentName:   "test",
			wantGroup:   "global-group",
			wantPort:    5555,
			wantEnabled: true,
		},
		{
			name: "agent not in map uses global",
			cfg: &SandboxConfig{
				MCP: domainconfig.MCPConfig{
					Group: "global-group",
					Port:  6666,
				},
				AgentOverrides: map[string]domainconfig.AgentOverride{
					"other-agent": {
						MCP: &domainconfig.MCPAgentOverride{
							Enabled: boolPtr(false),
						},
					},
				},
			},
			agentName:   "test",
			wantGroup:   "global-group",
			wantPort:    6666,
			wantEnabled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runner := NewSandboxRunner(SandboxDeps{
				Logger: testLogger(),
			})
			got := runner.resolveMCPConfig(tt.cfg, tt.agentName)

			assert.Equal(t, tt.wantGroup, got.Group)
			assert.Equal(t, tt.wantPort, got.Port)
			assert.Equal(t, tt.wantEnabled, got.IsEnabled())
		})
	}
}

func TestSandboxRunner_Prepare_MCPSuccess_AddsHostServices(t *testing.T) {
	t.Parallel()

	testAgent := agent.Agent{
		Name:          "test",
		Image:         "img:latest",
		Command:       []string{"cmd"},
		DefaultCPUs:   2,
		DefaultMemory: bytesize.ByteSize(2048),
	}

	handler := http.NewServeMux()
	mvm := &mockVM{sshPort: 2222, sshKeyPath: "/tmp/key"}
	mcpProvider := &mockMCPProvider{
		services: []hostservice.Service{
			{Name: "mcp", Port: 4483, Handler: handler},
		},
	}

	runner := NewSandboxRunner(SandboxDeps{
		Registry:      &mockRegistry{agents: map[string]agent.Agent{"test": testAgent}},
		VMRunner:      &mockVMRunner{vm: mvm},
		SessionRunner: &mockSessionRunner{},
		Config:        &SandboxConfig{},
		EnvProvider:   &mockEnvProvider{},
		Logger:        testLogger(),
		MCPProvider:   mcpProvider,
	})

	sb, err := runner.Prepare(t.Context(), "test", RunOpts{
		Workspace:     "/tmp/workspace",
		EgressProfile: string(egress.ProfilePermissive),
		SessionID:     "abcd1234",
	})
	require.NoError(t, err)
	defer func() { _ = sb.Cleanup() }()

	assert.True(t, mcpProvider.called)
	require.Len(t, sb.VMConfig.HostServices, 1)
	assert.Equal(t, "mcp", sb.VMConfig.HostServices[0].Name)
	assert.Equal(t, uint16(4483), sb.VMConfig.HostServices[0].Port)
}

func TestComposeMatcher(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		patterns []string
		path     string
		want     bool
	}{
		{"exact match", []string{".git"}, ".git", true},
		{"prefix match with slash", []string{".git"}, ".git/config", true},
		{"prefix match trailing slash pattern", []string{".git/"}, ".git/refs/heads", true},
		{"no false positive on .github", []string{".git"}, ".github/workflows/ci.yml", false},
		{"no false positive on .gitignore", []string{".git"}, ".gitignore", false},
		{"no false positive on .gitmodules", []string{".git"}, ".gitmodules", false},
		{"no match", []string{".git"}, "src/main.go", false},
		{"multiple patterns", []string{".git", "vendor"}, "vendor/lib/foo.go", true},
		{"base matcher delegates", []string{}, ".git", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := composeMatcher(snapshot.NopMatcher, tt.patterns)
			got := m.Match(tt.path)
			assert.Equal(t, tt.want, got)
		})
	}
}
