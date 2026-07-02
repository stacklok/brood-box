// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
)

func sampleOverride() domainconfig.AgentOverride {
	return domainconfig.AgentOverride{
		Image:         "ghcr.io/acme/aider:latest",
		Command:       []string{"aider"},
		Description:   "ACME agent",
		EnvForward:    []string{"OPENAI_API_KEY"},
		EgressProfile: "permissive",
	}
}

func TestUpsertAgentCreatesFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sub", "config.yaml")
	res, err := UpsertAgent(path, "aider", sampleOverride(), false)
	require.NoError(t, err)
	assert.True(t, res.Created)
	assert.False(t, res.Replaced)
	assert.Empty(t, res.BeforeSHA256)
	assert.NotEmpty(t, res.AfterSHA256)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	// The written file round-trips back through the loader.
	loaded, err := NewLoader(path).Load()
	require.NoError(t, err)
	custom, ok := loaded.Agents["aider"]
	require.True(t, ok)
	assert.Equal(t, "ghcr.io/acme/aider:latest", custom.Image)
	assert.Equal(t, []string{"aider"}, custom.Command)
	assert.Equal(t, []string{"OPENAI_API_KEY"}, custom.EnvForward)
}

func TestUpsertAgentPreservesComments(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	seed := `# top of file comment
mcp:
  enabled: true  # inline comment
`
	require.NoError(t, os.WriteFile(path, []byte(seed), 0o600))

	_, err := UpsertAgent(path, "aider", sampleOverride(), false)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	content := string(data)
	assert.Contains(t, content, "# top of file comment")
	assert.Contains(t, content, "# inline comment")
	assert.Contains(t, content, "agents:")
	assert.Contains(t, content, "aider:")

	// Existing config still parses and keeps its values alongside the new agent.
	loaded, err := NewLoader(path).Load()
	require.NoError(t, err)
	require.NotNil(t, loaded.MCP.Enabled)
	assert.True(t, *loaded.MCP.Enabled)
	_, ok := loaded.Agents["aider"]
	assert.True(t, ok)
}

func TestUpsertAgentPreservesCommentOnlyTemplate(t *testing.T) {
	t.Parallel()

	// The `config init` template is entirely comments; it parses to an empty
	// YAML tree, so a naive node round-trip would drop all documentation. The
	// mutation must keep the commented template and append the new agent.
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, WriteDefault(path, false))

	before, err := os.ReadFile(path)
	require.NoError(t, err)
	commentLinesBefore := countCommentLines(string(before))
	require.Positive(t, commentLinesBefore)

	res, err := UpsertAgent(path, "aider", sampleOverride(), false)
	require.NoError(t, err)
	assert.False(t, res.Created) // the file already existed (all comments)

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	// Every original comment line survives, and the agent is appended.
	assert.GreaterOrEqual(t, countCommentLines(string(after)), commentLinesBefore)
	assert.Contains(t, string(after), "# Brood Box configuration")

	loaded, err := NewLoader(path).Load()
	require.NoError(t, err)
	_, ok := loaded.Agents["aider"]
	assert.True(t, ok)
}

func countCommentLines(s string) int {
	n := 0
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			n++
		}
	}
	return n
}

func TestUpsertAgentAppendsToExistingAgents(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	seed := `agents:
  first:
    image: ghcr.io/acme/first:latest
    command: ["first"]
    egress_profile: permissive
`
	require.NoError(t, os.WriteFile(path, []byte(seed), 0o600))

	res, err := UpsertAgent(path, "aider", sampleOverride(), false)
	require.NoError(t, err)
	assert.False(t, res.Created)
	assert.NotEmpty(t, res.BeforeSHA256)
	assert.NotEqual(t, res.BeforeSHA256, res.AfterSHA256)

	loaded, err := NewLoader(path).Load()
	require.NoError(t, err)
	assert.Len(t, loaded.Agents, 2)
	assert.Contains(t, loaded.Agents, "first")
	assert.Contains(t, loaded.Agents, "aider")
}

func TestUpsertAgentRefusesExistingWithoutForce(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	_, err := UpsertAgent(path, "aider", sampleOverride(), false)
	require.NoError(t, err)

	before, err := os.ReadFile(path)
	require.NoError(t, err)

	_, err = UpsertAgent(path, "aider", sampleOverride(), false)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAgentExists)

	// File is left untouched on the refused write.
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestUpsertAgentReplacesWithForce(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	_, err := UpsertAgent(path, "aider", sampleOverride(), false)
	require.NoError(t, err)

	updated := sampleOverride()
	updated.Image = "ghcr.io/acme/aider:v2"
	res, err := UpsertAgent(path, "aider", updated, true)
	require.NoError(t, err)
	assert.True(t, res.Replaced)

	loaded, err := NewLoader(path).Load()
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io/acme/aider:v2", loaded.Agents["aider"].Image)
	assert.Len(t, loaded.Agents, 1)
}

func TestUpsertAgentRejectsNonMappingRoot(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("- just\n- a\n- list\n"), 0o600))

	_, err := UpsertAgent(path, "aider", sampleOverride(), false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a YAML mapping")
}

func TestUpsertAgentHandlesBareAgentsKey(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("agents:\n"), 0o600))

	_, err := UpsertAgent(path, "aider", sampleOverride(), false)
	require.NoError(t, err)

	loaded, err := NewLoader(path).Load()
	require.NoError(t, err)
	_, ok := loaded.Agents["aider"]
	assert.True(t, ok)
}

func TestUpsertAgentIsErrAgentExists(t *testing.T) {
	t.Parallel()
	// Guard the sentinel wrapping so callers can rely on errors.Is.
	err := errors.Join(ErrAgentExists)
	assert.ErrorIs(t, err, ErrAgentExists)
}
