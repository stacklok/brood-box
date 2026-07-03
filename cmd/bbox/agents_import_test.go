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

	infraconfig "github.com/stacklok/brood-box/internal/infra/config"
	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
)

// writeManifest writes a manifest YAML file for tests.
func writeManifest(t *testing.T, path, yamlContent string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(yamlContent), 0o600))
}

const sampleManifestYAML = `name: aider
image: ghcr.io/acme/aider-bbox:latest
command: ["aider"]
description: ACME agent
env_forward: [OPENAI_API_KEY]
env_required: [OPENAI_API_KEY]
egress_profile: standard
egress_hosts:
  standard:
    - name: api.openai.com
      ports: [443]
mcp:
  enabled: true
  mode: env
  authz:
    profile: safe-tools
`

func TestAgentsImportEndToEnd(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "present-value")

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	manifestPath := filepath.Join(t.TempDir(), "aider.yaml")
	writeManifest(t, manifestPath, sampleManifestYAML)

	var out bytes.Buffer
	cmd := agentsCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"import", manifestPath, "--config", cfgPath, "--json"})
	require.NoError(t, cmd.Execute())

	var receipt agentReceipt
	require.NoError(t, json.Unmarshal(out.Bytes(), &receipt))
	assert.Equal(t, "agents import", receipt.Command)
	assert.True(t, receipt.OK)
	assert.Equal(t, "custom", receipt.Agent.Type)
	assert.Equal(t, "aider", receipt.Agent.Name)
	assert.Equal(t, "ghcr.io/acme/aider-bbox:latest", receipt.Agent.Image)
	assert.Equal(t, "standard", receipt.Agent.EgressProfile)
	assert.Equal(t, domainconfig.MCPModeEnv, receipt.Agent.MCPMode)
	require.NotNil(t, receipt.Write)
	assert.True(t, receipt.Write.Created)

	// doctor --json on the imported agent passes.
	var dout bytes.Buffer
	dcmd := agentsCmd()
	dcmd.SetOut(&dout)
	dcmd.SetErr(&dout)
	dcmd.SetArgs([]string{"doctor", "aider", "--config", cfgPath, "--json"})
	require.NoError(t, dcmd.Execute())

	var dr agentReceipt
	require.NoError(t, json.Unmarshal(dout.Bytes(), &dr))
	assert.True(t, dr.OK)

	// The written config round-trips through the loader.
	loaded, err := infraconfig.NewLoader(cfgPath).Load()
	require.NoError(t, err)
	custom, ok := loaded.Agents["aider"]
	require.True(t, ok)
	assert.Equal(t, "ghcr.io/acme/aider-bbox:latest", custom.Image)
	assert.Equal(t, []string{"aider"}, custom.Command)
	assert.Equal(t, []string{"OPENAI_API_KEY"}, custom.EnvForward)
	require.NotNil(t, custom.MCP)
	assert.Equal(t, domainconfig.MCPModeEnv, custom.MCP.Mode)
}

func TestAgentsImportRefusesExistingWithoutForce(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	manifestPath := filepath.Join(t.TempDir(), "aider.yaml")
	writeManifest(t, manifestPath, sampleManifestYAML)

	args := []string{"import", manifestPath, "--config", cfgPath}

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

func TestAgentsImportRefusesBuiltin(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	manifestPath := filepath.Join(t.TempDir(), "claude.yaml")
	writeManifest(t, manifestPath, `name: claude-code
image: ghcr.io/acme/x:latest
command: ["x"]
egress_profile: permissive
`)

	cmd := agentsCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"import", manifestPath, "--config", cfgPath})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "built-in agent")
	_, statErr := os.Stat(cfgPath)
	assert.True(t, os.IsNotExist(statErr))
}

func TestAgentsImportRejectsMissingName(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	manifestPath := filepath.Join(t.TempDir(), "noname.yaml")
	writeManifest(t, manifestPath, `image: ghcr.io/acme/x:latest
command: ["x"]
egress_profile: permissive
`)

	cmd := agentsCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"import", manifestPath, "--config", cfgPath})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"name" field is required`)
	_, statErr := os.Stat(cfgPath)
	assert.True(t, os.IsNotExist(statErr))
}

func TestAgentsImportRejectsInvalidManifest(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	manifestPath := filepath.Join(t.TempDir(), "bad.yaml")
	// Missing required command -> ValidateCustomAgent fails.
	writeManifest(t, manifestPath, `name: bad
image: ghcr.io/acme/x:latest
egress_profile: permissive
`)

	cmd := agentsCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"import", manifestPath, "--config", cfgPath})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "command is required")
	_, statErr := os.Stat(cfgPath)
	assert.True(t, os.IsNotExist(statErr))
}

func TestAgentsImportRejectsUnknownField(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	manifestPath := filepath.Join(t.TempDir(), "unknown.yaml")
	writeManifest(t, manifestPath, `name: x
image: ghcr.io/acme/x:latest
command: ["x"]
egress_profile: permissive
bogus_field: true
`)

	cmd := agentsCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"import", manifestPath, "--config", cfgPath})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bogus_field")
}

func TestAgentsExportEndToEnd(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "present-value")

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	manifestPath := filepath.Join(t.TempDir(), "aider.yaml")
	writeManifest(t, manifestPath, sampleManifestYAML)

	// Import first so the agent exists in the config.
	importCmd := agentsCmd()
	importCmd.SetOut(&bytes.Buffer{})
	importCmd.SetErr(&bytes.Buffer{})
	importCmd.SetArgs([]string{"import", manifestPath, "--config", cfgPath})
	require.NoError(t, importCmd.Execute())

	// Export it back out.
	var out bytes.Buffer
	expCmd := agentsCmd()
	expCmd.SetOut(&out)
	expCmd.SetErr(&out)
	expCmd.SetArgs([]string{"export", "aider", "--config", cfgPath})
	require.NoError(t, expCmd.Execute())

	exported := out.String()
	assert.Contains(t, exported, "name: aider")
	assert.Contains(t, exported, "image: ghcr.io/acme/aider-bbox:latest")
	assert.Contains(t, exported, "api.openai.com")
	assert.Contains(t, exported, "mode: env")

	// Re-import the exported manifest into a fresh config — round-trip no-op.
	cfgPath2 := filepath.Join(t.TempDir(), "config2.yaml")
	reExported := filepath.Join(t.TempDir(), "reexported.yaml")
	writeManifest(t, reExported, exported)

	var out2 bytes.Buffer
	reimportCmd := agentsCmd()
	reimportCmd.SetOut(&out2)
	reimportCmd.SetErr(&out2)
	reimportCmd.SetArgs([]string{"import", reExported, "--config", cfgPath2, "--json"})
	require.NoError(t, reimportCmd.Execute())

	var receipt agentReceipt
	require.NoError(t, json.Unmarshal(out2.Bytes(), &receipt))
	assert.True(t, receipt.OK)
	assert.Equal(t, "aider", receipt.Agent.Name)
	assert.Equal(t, "ghcr.io/acme/aider-bbox:latest", receipt.Agent.Image)

	// Both configs hold equivalent agent definitions.
	l1, err := infraconfig.NewLoader(cfgPath).Load()
	require.NoError(t, err)
	l2, err := infraconfig.NewLoader(cfgPath2).Load()
	require.NoError(t, err)
	assert.Equal(t, l1.Agents["aider"], l2.Agents["aider"])
}

func TestAgentsExportStripsDefaultEnvValues(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	// Seed a config with a custom agent that has default_env values (secrets).
	seed := `agents:
  sec:
    image: ghcr.io/acme/sec:latest
    command: ["sec"]
    egress_profile: permissive
    default_env:
      API_KEY: "super-secret-value"
      OTHER: "another-secret"
`
	require.NoError(t, os.WriteFile(cfgPath, []byte(seed), 0o600))

	var out bytes.Buffer
	cmd := agentsCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"export", "sec", "--config", cfgPath})
	require.NoError(t, cmd.Execute())

	exported := out.String()
	assert.NotContains(t, exported, "super-secret-value")
	assert.NotContains(t, exported, "another-secret")
	assert.NotContains(t, exported, "default_env")
}

func TestAgentsExportRefusesBuiltin(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	cmd := agentsCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"export", "claude-code", "--config", cfgPath})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "built-in agent")
}

func TestAgentsExportRefusesUnknown(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	cmd := agentsCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"export", "nope", "--config", cfgPath})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not declared")
}

func TestLoadManifestOperatorPathAcceptsSymlink(t *testing.T) {
	t.Parallel()

	// Import paths are operator-supplied (like --config), not workspace-local,
	// so a symlink is accepted — matching the global config loader's behavior.
	dir := t.TempDir()
	target := filepath.Join(dir, "real.yaml")
	writeManifest(t, target, sampleManifestYAML)
	link := filepath.Join(dir, "link.yaml")
	require.NoError(t, os.Symlink(target, link))

	m, err := infraconfig.LoadManifest(link)
	require.NoError(t, err)
	assert.Equal(t, "aider", m.Name)
}
