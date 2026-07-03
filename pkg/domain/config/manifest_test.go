// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
)

func TestAgentManifestInlineFieldsTrackAgentOverride(t *testing.T) {
	t.Parallel()

	// A manifest with every AgentOverride field set round-trips through YAML
	// and the fields land at the top level (inline), not nested under a
	// sub-key. This is the structural guarantee the import/export commands
	// rely on: the manifest IS an agents:<name> block plus a top-level name.
	enabled := true
	in := domainconfig.AgentManifest{
		Name: "aider",
		AgentOverride: domainconfig.AgentOverride{
			Image:         "ghcr.io/acme/aider:latest",
			Description:   "ACME agent",
			Command:       []string{"aider", "--yes"},
			EnvForward:    []string{"OPENAI_API_KEY"},
			EnvRequired:   []string{"OPENAI_API_KEY"},
			EgressProfile: "standard",
			EgressHosts: map[string][]domainconfig.EgressHostConfig{
				"standard": {{Name: "api.openai.com", Ports: []uint16{443}}},
			},
			MCP: &domainconfig.MCPAgentOverride{
				Enabled: &enabled,
				Mode:    domainconfig.MCPModeEnv,
				Authz:   &domainconfig.MCPAuthzConfig{Profile: "safe-tools"},
			},
		},
	}

	data, err := yaml.Marshal(in)
	require.NoError(t, err)
	str := string(data)
	assert.Contains(t, str, "name: aider")
	assert.Contains(t, str, "image: ghcr.io/acme/aider:latest")
	assert.Contains(t, str, "command:")
	assert.Contains(t, str, "egress_profile: standard")
	assert.Contains(t, str, "mode: env")
	// No nested "agentoverride:" key should appear (inline embedding).
	assert.NotContains(t, str, "agentoverride:")

	var out domainconfig.AgentManifest
	require.NoError(t, yaml.Unmarshal(data, &out))
	assert.Equal(t, in.Name, out.Name)
	assert.Equal(t, in.Image, out.Image)
	assert.Equal(t, in.Command, out.Command)
	assert.Equal(t, in.EgressProfile, out.EgressProfile)
	require.NotNil(t, out.MCP)
	assert.Equal(t, in.MCP.Mode, out.MCP.Mode)
}

func TestAgentManifestStrictUnknownFieldRejected(t *testing.T) {
	t.Parallel()

	// A typo'd field must fail loudly under strict decoding (the import path
	// uses configfile.DecodeStrict, which sets KnownFields(true)).
	bad := []byte("name: x\nbogus_field: true\n")
	var m domainconfig.AgentManifest
	dec := yaml.NewDecoder(bytes.NewReader(bad))
	dec.KnownFields(true)
	err := dec.Decode(&m)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bogus_field")
}
