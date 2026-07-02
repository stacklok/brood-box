// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
	"github.com/stacklok/brood-box/pkg/domain/egress"
)

func TestBuildAddAgentOverride(t *testing.T) {
	t.Parallel()

	t.Run("maps core fields and forces mcp env mode", func(t *testing.T) {
		t.Parallel()
		o, err := buildAddAgentOverride(agentAddFlags{
			image:           "ghcr.io/acme/aider:latest",
			command:         []string{"aider", "--yes"},
			description:     "ACME agent",
			envForward:      []string{"OPENAI_API_KEY"},
			envRequired:     []string{"OPENAI_API_KEY"},
			mcp:             true,
			mcpAuthzProfile: "observe",
		})
		require.NoError(t, err)
		assert.Equal(t, "ghcr.io/acme/aider:latest", o.Image)
		assert.Equal(t, []string{"aider", "--yes"}, o.Command)
		assert.Equal(t, []string{"OPENAI_API_KEY"}, o.EnvForward)
		require.NotNil(t, o.MCP)
		assert.Equal(t, domainconfig.MCPModeEnv, o.MCP.Mode)
		require.NotNil(t, o.MCP.Enabled)
		assert.True(t, *o.MCP.Enabled)
		require.NotNil(t, o.MCP.Authz)
		assert.Equal(t, "observe", o.MCP.Authz.Profile)
	})

	t.Run("files allow-host under the effective (default standard) profile", func(t *testing.T) {
		t.Parallel()
		o, err := buildAddAgentOverride(agentAddFlags{
			image:      "ghcr.io/acme/tool:latest",
			command:    []string{"tool"},
			allowHosts: []string{"api.acme.dev:443"},
		})
		require.NoError(t, err)
		// EgressProfile stays empty so AgentFromOverride applies "standard".
		assert.Empty(t, o.EgressProfile)
		hosts := o.EgressHosts[string(domainconfig.DefaultCustomAgentEgressProfile)]
		require.Len(t, hosts, 1)
		assert.Equal(t, "api.acme.dev", hosts[0].Name)

		// The result passes custom-agent validation (standard profile has hosts).
		require.NoError(t, domainconfig.ValidateCustomAgent("tool", o, imageRefValidator))
	})

	t.Run("parses memory and explicit egress profile", func(t *testing.T) {
		t.Parallel()
		o, err := buildAddAgentOverride(agentAddFlags{
			image:         "ghcr.io/acme/tool:latest",
			command:       []string{"tool"},
			memory:        "4g",
			cpus:          4,
			egressProfile: string(egress.ProfilePermissive),
		})
		require.NoError(t, err)
		assert.Equal(t, uint32(4096), o.Memory.MiB())
		assert.Equal(t, uint32(4), o.CPUs)
		assert.Equal(t, string(egress.ProfilePermissive), o.EgressProfile)
	})

	t.Run("rejects bad memory", func(t *testing.T) {
		t.Parallel()
		_, err := buildAddAgentOverride(agentAddFlags{
			image:   "ghcr.io/acme/tool:latest",
			command: []string{"tool"},
			memory:  "banana",
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--memory")
	})
}

func TestAgentsAddEndToEnd(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "present-value")
	path := filepath.Join(t.TempDir(), "config.yaml")

	var out bytes.Buffer
	cmd := agentsCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{
		"add", "aider",
		"--image", "ghcr.io/acme/aider:latest",
		"--command", "aider",
		"--env", "OPENAI_API_KEY",
		"--env-required", "OPENAI_API_KEY",
		"--mcp",
		"--config", path,
		"--json",
	})
	require.NoError(t, cmd.Execute())

	var receipt agentReceipt
	require.NoError(t, json.Unmarshal(out.Bytes(), &receipt))
	assert.Equal(t, "agents add", receipt.Command)
	assert.True(t, receipt.OK)
	assert.Equal(t, "custom", receipt.Agent.Type)
	assert.Equal(t, domainconfig.CustomAgentValidatorVersion, receipt.ValidatorVersion)
	require.NotNil(t, receipt.Write)
	assert.True(t, receipt.Write.Created)
	assert.Empty(t, receipt.Write.Fingerprint.Before)
	assert.NotEmpty(t, receipt.Write.Fingerprint.After)
	// MCP env mode applied; authz defaults to safe-tools for a custom agent.
	assert.Equal(t, domainconfig.MCPModeEnv, receipt.Agent.MCPMode)
	assert.Equal(t, domainconfig.DefaultCustomAgentMCPAuthzProfile, receipt.Agent.MCPAuthzProfile)
	// Required env is reported by name + presence only (no value).
	require.Len(t, receipt.Agent.EnvRequired, 1)
	assert.Equal(t, "OPENAI_API_KEY", receipt.Agent.EnvRequired[0].Name)
	assert.True(t, receipt.Agent.EnvRequired[0].Present)
	assert.NotContains(t, out.String(), "present-value")

	// doctor --json on the freshly added agent passes.
	var dout bytes.Buffer
	dcmd := agentsCmd()
	dcmd.SetOut(&dout)
	dcmd.SetErr(&dout)
	dcmd.SetArgs([]string{"doctor", "aider", "--config", path, "--json"})
	require.NoError(t, dcmd.Execute())

	var dr agentReceipt
	require.NoError(t, json.Unmarshal(dout.Bytes(), &dr))
	assert.Equal(t, "agents doctor", dr.Command)
	assert.True(t, dr.OK)
	assert.NotEmpty(t, dr.Checks)
}

func TestAgentsAddRefusesExistingWithoutForce(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	args := []string{
		"add", "aider",
		"--image", "ghcr.io/acme/aider:latest",
		"--command", "aider",
		"--egress-profile", "permissive",
		"--config", path,
	}

	first := agentsCmd()
	first.SetOut(&bytes.Buffer{})
	first.SetErr(&bytes.Buffer{})
	first.SetArgs(args)
	require.NoError(t, first.Execute())

	second := agentsCmd()
	second.SetOut(&bytes.Buffer{})
	second.SetErr(&bytes.Buffer{})
	second.SetArgs(args)
	err := second.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")

	// --force allows the overwrite.
	third := agentsCmd()
	third.SetOut(&bytes.Buffer{})
	third.SetErr(&bytes.Buffer{})
	third.SetArgs(append(args, "--force"))
	require.NoError(t, third.Execute())
}

func TestAgentsAddRefusesBuiltin(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	cmd := agentsCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"add", "claude-code",
		"--image", "ghcr.io/acme/x:latest",
		"--command", "x",
		"--egress-profile", "permissive",
		"--config", path,
	})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "built-in agent")
	// No file should have been written.
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr))
}

func TestAgentsAddRefusesMCPAuthzProfileWithoutMCP(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "config.yaml")
	cmd := agentsCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"add", "aider",
		"--image", "ghcr.io/acme/aider:latest",
		"--command", "aider",
		"--mcp-authz-profile", "observe",
		"--config", path,
	})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--mcp")
	// No file should have been written.
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr))
}

func TestAgentsInitPrintsStanza(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	cmd := agentsCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"init", "aider"})
	require.NoError(t, cmd.Execute())

	got := out.String()
	assert.Contains(t, got, "agents:")
	assert.Contains(t, got, "# aider:")
	assert.Contains(t, got, "GLOBAL-ONLY")
}
