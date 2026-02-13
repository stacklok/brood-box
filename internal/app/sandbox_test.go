// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/sandbox-agent/internal/domain/agent"
	infraconfig "github.com/stacklok/sandbox-agent/internal/infra/config"
	infrassh "github.com/stacklok/sandbox-agent/internal/infra/ssh"
	"github.com/stacklok/sandbox-agent/internal/infra/vm"
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
		Logger:      slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
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
		Logger:      slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
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
		Logger:      slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
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
