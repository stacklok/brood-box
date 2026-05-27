// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/brood-box/pkg/clients"
	domainagent "github.com/stacklok/brood-box/pkg/domain/agent"
)

func newBuiltinRegistry() *Registry {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewRegistry(clients.Builtins(logger)...)
}

func TestNewRegistry_Empty(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	assert.Empty(t, reg.List(), "no-arg NewRegistry should be empty")
}

func TestNewRegistry_WithBuiltins(t *testing.T) {
	t.Parallel()

	reg := newBuiltinRegistry()
	entries := reg.List()

	require.Len(t, entries, 5, "registry should contain exactly 5 built-in clients")

	names := make(map[string]bool, len(entries))
	for _, e := range entries {
		names[e.Agent.Name] = true
		assert.NotEmpty(t, e.Agent.Name, "agent name must not be empty")
		assert.NotEmpty(t, e.Agent.Image, "agent %s image must not be empty", e.Agent.Name)
		assert.NotEmpty(t, e.Agent.Command, "agent %s command must not be empty", e.Agent.Name)
		assert.NotNil(t, e.Plugin, "built-in client %s must have a Plugin", e.Agent.Name)
	}

	assert.True(t, names["claude-code"], "registry should contain claude-code")
	assert.True(t, names["codex"], "registry should contain codex")
	assert.True(t, names["opencode"], "registry should contain opencode")
	assert.True(t, names["hermes"], "registry should contain hermes")
	assert.True(t, names["gemini"], "registry should contain gemini")
}

func TestRegistry_Get_BuiltInClient(t *testing.T) {
	t.Parallel()

	reg := newBuiltinRegistry()

	tests := []struct {
		name string
	}{
		{name: "claude-code"},
		{name: "codex"},
		{name: "opencode"},
		{name: "hermes"},
		{name: "gemini"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e, err := reg.Get(tt.name)
			require.NoError(t, err)
			assert.Equal(t, tt.name, e.Agent.Name)
			assert.NotEmpty(t, e.Agent.Image)
			assert.NotEmpty(t, e.Agent.Command)
			require.NotNil(t, e.Plugin)
			assert.NotNil(t, e.Plugin.MCPConfig(), "every built-in must have an MCPInjector")
		})
	}
}

func TestRegistry_Get_NotFound(t *testing.T) {
	t.Parallel()

	reg := newBuiltinRegistry()
	_, err := reg.Get("nonexistent")

	require.Error(t, err)
	var notFound *domainagent.ErrNotFound
	assert.True(t, errors.As(err, &notFound), "error should be ErrNotFound")
	assert.Equal(t, "nonexistent", notFound.Name)
}

func TestRegistry_Add_DataOnlyAgent(t *testing.T) {
	t.Parallel()

	reg := newBuiltinRegistry()
	custom := domainagent.Agent{
		Name:    "my-custom-agent",
		Image:   "ghcr.io/example/custom:latest",
		Command: []string{"custom-cmd"},
	}

	err := reg.Add(custom)
	require.NoError(t, err)

	got, err := reg.Get("my-custom-agent")
	require.NoError(t, err)
	assert.Equal(t, custom.Name, got.Agent.Name)
	assert.Equal(t, custom.Image, got.Agent.Image)
	assert.Equal(t, custom.Command, got.Agent.Command)
	assert.Nil(t, got.Plugin, "data-only Add entries must have nil Plugin")
}

func TestRegistry_Add_InvalidName(t *testing.T) {
	t.Parallel()

	reg := newBuiltinRegistry()

	tests := []struct {
		name      string
		agentName string
	}{
		{name: "empty name", agentName: ""},
		{name: "path traversal", agentName: "../etc"},
		{name: "leading hyphen", agentName: "-bad"},
		{name: "too long", agentName: strings.Repeat("a", domainagent.MaxNameLength+1)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := reg.Add(domainagent.Agent{
				Name:    tt.agentName,
				Image:   "img:latest",
				Command: []string{"cmd"},
			})
			require.Error(t, err, "Add(%q) should fail", tt.agentName)
			assert.Contains(t, err.Error(), "cannot register agent")
		})
	}
}

func TestRegistry_Add_OverridesExisting(t *testing.T) {
	t.Parallel()

	reg := newBuiltinRegistry()

	original, err := reg.Get("claude-code")
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io/stacklok/brood-box/claude-code:latest", original.Agent.Image)

	override := domainagent.Agent{
		Name:    "claude-code",
		Image:   "ghcr.io/custom/claude-code:v2",
		Command: []string{"custom-claude"},
	}
	err = reg.Add(override)
	require.NoError(t, err)

	got, err := reg.Get("claude-code")
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io/custom/claude-code:v2", got.Agent.Image)
	assert.Equal(t, []string{"custom-claude"}, got.Agent.Command)
	assert.Nil(t, got.Plugin, "Add overrides drop the Plugin (data-only)")
}

func TestRegistry_List_SortedByName(t *testing.T) {
	t.Parallel()

	reg := newBuiltinRegistry()

	err := reg.Add(domainagent.Agent{Name: "aaa-first", Image: "img", Command: []string{"cmd"}})
	require.NoError(t, err)
	err = reg.Add(domainagent.Agent{Name: "zzz-last", Image: "img", Command: []string{"cmd"}})
	require.NoError(t, err)

	entries := reg.List()
	require.Len(t, entries, 7)

	for i := 1; i < len(entries); i++ {
		assert.True(t, entries[i-1].Agent.Name < entries[i].Agent.Name,
			"List() not sorted: %q should come before %q",
			entries[i-1].Agent.Name, entries[i].Agent.Name)
	}
}

func TestRegistry_List_ReturnsCopies(t *testing.T) {
	t.Parallel()

	reg := newBuiltinRegistry()

	list1 := reg.List()
	require.NotEmpty(t, list1)
	list1[0].Agent.Name = "MUTATED"

	list2 := reg.List()
	for _, e := range list2 {
		assert.NotEqual(t, "MUTATED", e.Agent.Name,
			"modifying returned slice should not affect the registry")
	}
}
