// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainagent "github.com/stacklok/brood-box/pkg/domain/agent"
)

func TestNewRegistry_ContainsBuiltInAgents(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	agents := reg.List()

	require.Len(t, agents, 5, "registry should contain exactly 5 built-in agents")

	names := make(map[string]bool, len(agents))
	for _, a := range agents {
		names[a.Name] = true
		assert.NotEmpty(t, a.Name, "agent name must not be empty")
		assert.NotEmpty(t, a.Image, "agent %s image must not be empty", a.Name)
		assert.NotEmpty(t, a.Command, "agent %s command must not be empty", a.Name)
	}

	assert.True(t, names["claude-code"], "registry should contain claude-code")
	assert.True(t, names["codex"], "registry should contain codex")
	assert.True(t, names["opencode"], "registry should contain opencode")
	assert.True(t, names["hermes"], "registry should contain hermes")
	assert.True(t, names["gemini"], "registry should contain gemini")
}

func TestRegistry_Get_BuiltInAgent(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()

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

			a, err := reg.Get(tt.name)
			require.NoError(t, err)
			assert.Equal(t, tt.name, a.Name)
			assert.NotEmpty(t, a.Image)
			assert.NotEmpty(t, a.Command)
		})
	}
}

func TestRegistry_Get_NotFound(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	_, err := reg.Get("nonexistent")

	require.Error(t, err)
	var notFound *domainagent.ErrNotFound
	assert.True(t, errors.As(err, &notFound), "error should be ErrNotFound")
	assert.Equal(t, "nonexistent", notFound.Name)
}

func TestRegistry_Add_ValidAgent(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	custom := domainagent.Agent{
		Name:    "my-custom-agent",
		Image:   "ghcr.io/example/custom:latest",
		Command: []string{"custom-cmd"},
	}

	err := reg.Add(custom)
	require.NoError(t, err)

	got, err := reg.Get("my-custom-agent")
	require.NoError(t, err)
	assert.Equal(t, custom.Name, got.Name)
	assert.Equal(t, custom.Image, got.Image)
	assert.Equal(t, custom.Command, got.Command)
}

func TestRegistry_Add_InvalidName(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()

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

	reg := NewRegistry()

	// Verify the original exists.
	original, err := reg.Get("claude-code")
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io/stacklok/brood-box/claude-code:latest", original.Image)

	// Override with a new agent using the same name.
	override := domainagent.Agent{
		Name:    "claude-code",
		Image:   "ghcr.io/custom/claude-code:v2",
		Command: []string{"custom-claude"},
	}
	err = reg.Add(override)
	require.NoError(t, err)

	// Get should return the override.
	got, err := reg.Get("claude-code")
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io/custom/claude-code:v2", got.Image)
	assert.Equal(t, []string{"custom-claude"}, got.Command)
}

func TestRegistry_List_SortedByName(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()

	// Add agents with names that should sort between built-ins.
	err := reg.Add(domainagent.Agent{Name: "aaa-first", Image: "img", Command: []string{"cmd"}})
	require.NoError(t, err)
	err = reg.Add(domainagent.Agent{Name: "zzz-last", Image: "img", Command: []string{"cmd"}})
	require.NoError(t, err)

	agents := reg.List()
	require.Len(t, agents, 7)

	for i := 1; i < len(agents); i++ {
		assert.True(t, agents[i-1].Name < agents[i].Name,
			"List() not sorted: %q should come before %q", agents[i-1].Name, agents[i].Name)
	}
}

func TestRegistry_List_ReturnsCopies(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()

	// Get the first list and modify it.
	list1 := reg.List()
	require.NotEmpty(t, list1)
	list1[0].Name = "MUTATED"

	// Get a second list — should be unaffected.
	list2 := reg.List()
	for _, a := range list2 {
		assert.NotEqual(t, "MUTATED", a.Name,
			"modifying returned slice should not affect the registry")
	}
}
