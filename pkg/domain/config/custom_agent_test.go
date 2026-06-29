// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/bytesize"
	"github.com/stacklok/brood-box/pkg/domain/egress"
	"github.com/stacklok/brood-box/pkg/domain/settings"
)

func TestAgentFromOverride(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		agent    string
		override AgentOverride
		defaults DefaultsConfig
		assert   func(t *testing.T, a agent.Agent)
	}{
		{
			name:  "full mapping",
			agent: "my-agent",
			override: AgentOverride{
				Image:         "ghcr.io/acme/my-agent:latest",
				Command:       []string{"my-agent", "--interactive"},
				Description:   "ACME agent",
				EnvForward:    []string{"ACME_API_KEY", "ACME_*"},
				DefaultEnv:    map[string]string{"ACME_MODE": "interactive"},
				CPUs:          4,
				Memory:        4096,
				TmpSize:       2048,
				EgressProfile: string(egress.ProfileStandard),
				EgressHosts: map[string][]EgressHostConfig{
					"standard": {{Name: "api.acme.dev", Ports: []uint16{443}, Protocol: 6}},
				},
				Credentials: &AgentCredentialsConfig{Persist: []string{".acme/credentials.json", ".config/acme/"}},
				Settings: []AgentSettingsEntryConfig{
					{
						Category:  "settings",
						HostPath:  ".config/acme/settings.json",
						GuestPath: ".config/acme/settings.json",
						Kind:      "merge-file",
						Format:    "json",
						Optional:  true,
						AllowKeys: []string{"theme", "editor"},
					},
				},
			},
			assert: func(t *testing.T, a agent.Agent) {
				assert.Equal(t, "my-agent", a.Name)
				assert.Equal(t, "ghcr.io/acme/my-agent:latest", a.Image)
				assert.Equal(t, []string{"my-agent", "--interactive"}, a.Command)
				assert.Equal(t, []string{"ACME_API_KEY", "ACME_*"}, a.EnvForward)
				assert.Equal(t, map[string]string{"ACME_MODE": "interactive"}, a.DefaultEnv)
				assert.Equal(t, uint32(4), a.DefaultCPUs)
				assert.Equal(t, bytesize.ByteSize(4096), a.DefaultMemory)
				assert.Equal(t, bytesize.ByteSize(2048), a.DefaultTmpSize)
				assert.Equal(t, egress.ProfileStandard, a.DefaultEgressProfile)
				require.Len(t, a.EgressHosts[egress.ProfileStandard], 1)
				assert.Equal(t, "api.acme.dev", a.EgressHosts[egress.ProfileStandard][0].Name)
				assert.Equal(t, []string{".acme/credentials.json", ".config/acme/"}, a.CredentialPaths)
				require.NotNil(t, a.SettingsManifest)
				require.Len(t, a.SettingsManifest.Entries, 1)
				e := a.SettingsManifest.Entries[0]
				assert.Equal(t, settings.KindMergeFile, e.Kind)
				require.NotNil(t, e.Filter)
				assert.Equal(t, []string{"theme", "editor"}, e.Filter.AllowKeys)
			},
		},
		{
			name:  "settings guest_path defaults to host_path and kinds map correctly",
			agent: "settingsmap",
			override: AgentOverride{
				Image:         "ghcr.io/acme/x:latest",
				Command:       []string{"run"},
				EgressProfile: string(egress.ProfilePermissive),
				Settings: []AgentSettingsEntryConfig{
					{
						Category: "settings",
						HostPath: ".config/acme/",
						Kind:     "directory",
						// GuestPath omitted on purpose: must default to HostPath.
					},
					{
						Category: "settings",
						HostPath: ".acme-rc",
						// Kind omitted on purpose: must default to KindFile.
					},
				},
			},
			assert: func(t *testing.T, a agent.Agent) {
				require.NotNil(t, a.SettingsManifest)
				require.Len(t, a.SettingsManifest.Entries, 2)

				dir := a.SettingsManifest.Entries[0]
				assert.Equal(t, settings.KindDirectory, dir.Kind)
				assert.Equal(t, ".config/acme/", dir.GuestPath,
					"empty guest_path must default to host_path")

				file := a.SettingsManifest.Entries[1]
				assert.Equal(t, settings.KindFile, file.Kind,
					"unset kind must default to file")
				assert.Equal(t, ".acme-rc", file.GuestPath,
					"empty guest_path must default to host_path")
			},
		},
		{
			name:  "empty env_forward stays empty",
			agent: "minimal",
			override: AgentOverride{
				Image:         "ghcr.io/acme/minimal:latest",
				Command:       []string{"run"},
				EgressProfile: string(egress.ProfilePermissive),
			},
			assert: func(t *testing.T, a agent.Agent) {
				assert.Empty(t, a.EnvForward)
				assert.Nil(t, a.DefaultEnv)
				assert.Nil(t, a.CredentialPaths)
				assert.Nil(t, a.SettingsManifest)
			},
		},
		{
			name:  "egress profile defaults to standard when unset",
			agent: "defprofile",
			override: AgentOverride{
				Image:   "ghcr.io/acme/x:latest",
				Command: []string{"run"},
			},
			assert: func(t *testing.T, a agent.Agent) {
				assert.Equal(t, egress.ProfileStandard, a.DefaultEgressProfile)
			},
		},
		{
			name:  "global defaults apply when override resources unset",
			agent: "useglobal",
			override: AgentOverride{
				Image:   "ghcr.io/acme/x:latest",
				Command: []string{"run"},
			},
			defaults: DefaultsConfig{CPUs: 2, Memory: 1024, TmpSize: 512},
			assert: func(t *testing.T, a agent.Agent) {
				assert.Equal(t, uint32(2), a.DefaultCPUs)
				assert.Equal(t, bytesize.ByteSize(1024), a.DefaultMemory)
				assert.Equal(t, bytesize.ByteSize(512), a.DefaultTmpSize)
			},
		},
		{
			name:  "override resources win over global defaults",
			agent: "overrideres",
			override: AgentOverride{
				Image:         "ghcr.io/acme/x:latest",
				Command:       []string{"run"},
				EgressProfile: string(egress.ProfilePermissive),
				CPUs:          8,
				Memory:        8192,
				TmpSize:       4096,
			},
			defaults: DefaultsConfig{CPUs: 2, Memory: 1024, TmpSize: 512},
			assert: func(t *testing.T, a agent.Agent) {
				assert.Equal(t, uint32(8), a.DefaultCPUs)
				assert.Equal(t, bytesize.ByteSize(8192), a.DefaultMemory)
				assert.Equal(t, bytesize.ByteSize(4096), a.DefaultTmpSize)
			},
		},
		{
			name:  "safe minimum cpus/memory applied when override and defaults are zero",
			agent: "barebones",
			override: AgentOverride{
				Image:         "ghcr.io/acme/x:latest",
				Command:       []string{"run"},
				EgressProfile: string(egress.ProfilePermissive),
			},
			// No global defaults set either.
			assert: func(t *testing.T, a agent.Agent) {
				assert.Equal(t, uint32(DefaultCustomAgentCPUs), a.DefaultCPUs,
					"a custom agent with no resources must fall back to a bootable CPU floor")
				assert.Equal(t, DefaultCustomAgentMemory, a.DefaultMemory,
					"a custom agent with no resources must fall back to a bootable memory floor")
				assert.NotZero(t, a.DefaultCPUs)
				assert.NotZero(t, a.DefaultMemory)
			},
		},
		{
			name:  "resources clamped to configured maximums",
			agent: "greedy",
			override: AgentOverride{
				Image:         "ghcr.io/acme/x:latest",
				Command:       []string{"run"},
				EgressProfile: string(egress.ProfilePermissive),
				CPUs:          MaxCPUs + 64,
				Memory:        MaxMemory + 1024,
				TmpSize:       MaxTmpSize + 1024,
			},
			assert: func(t *testing.T, a agent.Agent) {
				assert.Equal(t, MaxCPUs, a.DefaultCPUs)
				assert.Equal(t, MaxMemory, a.DefaultMemory)
				assert.Equal(t, MaxTmpSize, a.DefaultTmpSize)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ag, err := AgentFromOverride(tt.agent, tt.override, tt.defaults)
			require.NoError(t, err)
			tt.assert(t, ag)
		})
	}
}

func TestAgentFromOverride_UnknownSettingsKind(t *testing.T) {
	t.Parallel()
	_, err := AgentFromOverride("x", AgentOverride{
		Image:   "img",
		Command: []string{"run"},
		Settings: []AgentSettingsEntryConfig{
			{HostPath: "a", Kind: "bogus"},
		},
	}, DefaultsConfig{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown settings kind")
}

func TestValidateCustomAgent(t *testing.T) {
	t.Parallel()

	valid := AgentOverride{
		Image:         "ghcr.io/acme/my-agent:latest",
		Command:       []string{"my-agent"},
		EnvForward:    []string{"ACME_*"},
		EgressProfile: string(egress.ProfileStandard),
		EgressHosts: map[string][]EgressHostConfig{
			"standard": {{Name: "api.acme.dev", Ports: []uint16{443}}},
		},
	}

	tests := []struct {
		name      string
		agentName string
		override  AgentOverride
		wantErr   string
	}{
		{name: "valid", agentName: "my-agent", override: valid},
		{name: "invalid name", agentName: "../evil", override: valid, wantErr: "invalid agent name"},
		{name: "missing image", agentName: "x", override: AgentOverride{Command: []string{"run"}, EgressProfile: "permissive"}, wantErr: "image is required"},
		{name: "missing command", agentName: "x", override: AgentOverride{Image: "img", EgressProfile: "permissive"}, wantErr: "command is required"},
		{
			name: "bare star env_forward", agentName: "x",
			override: AgentOverride{Image: "img", Command: []string{"run"}, EnvForward: []string{"*"}, EgressProfile: "permissive"},
			wantErr:  "bare",
		},
		{
			name: "leading star env_forward", agentName: "x",
			override: AgentOverride{Image: "img", Command: []string{"run"}, EnvForward: []string{"*_KEY"}, EgressProfile: "permissive"},
			wantErr:  "leading-star",
		},
		{
			name: "env_required with equals", agentName: "x",
			override: AgentOverride{Image: "img", Command: []string{"run"}, EnvRequired: []string{"FOO=bar"}, EgressProfile: "permissive"},
			wantErr:  "must not contain",
		},
		{
			name: "env_required empty name", agentName: "x",
			override: AgentOverride{Image: "img", Command: []string{"run"}, EnvRequired: []string{""}, EgressProfile: "permissive"},
			wantErr:  "empty name",
		},
		{
			name: "env_required whitespace name", agentName: "x",
			override: AgentOverride{Image: "img", Command: []string{"run"}, EnvRequired: []string{"  "}, EgressProfile: "permissive"},
			wantErr:  "empty name",
		},
		{
			name: "absolute credential path", agentName: "x",
			override: AgentOverride{Image: "img", Command: []string{"run"}, EgressProfile: "permissive",
				Credentials: &AgentCredentialsConfig{Persist: []string{"/etc/passwd"}}},
			wantErr: "absolute path",
		},
		{
			name: "dotdot credential path", agentName: "x",
			override: AgentOverride{Image: "img", Command: []string{"run"}, EgressProfile: "permissive",
				Credentials: &AgentCredentialsConfig{Persist: []string{"../escape"}}},
			wantErr: "escapes",
		},
		{
			name: "empty credential path", agentName: "x",
			override: AgentOverride{Image: "img", Command: []string{"run"}, EgressProfile: "permissive",
				Credentials: &AgentCredentialsConfig{Persist: []string{""}}},
			wantErr: "empty path",
		},
		{
			name: "settings absolute host_path", agentName: "x",
			override: AgentOverride{Image: "img", Command: []string{"run"}, EgressProfile: "permissive",
				Settings: []AgentSettingsEntryConfig{{HostPath: "/abs"}}},
			wantErr: "absolute path",
		},
		{
			name: "settings dotdot guest_path", agentName: "x",
			override: AgentOverride{Image: "img", Command: []string{"run"}, EgressProfile: "permissive",
				Settings: []AgentSettingsEntryConfig{{HostPath: "ok", GuestPath: "a/../../b"}}},
			wantErr: "escapes",
		},
		{
			name: "bad egress hostname", agentName: "x",
			override: AgentOverride{Image: "img", Command: []string{"run"}, EgressProfile: "standard",
				EgressHosts: map[string][]EgressHostConfig{"standard": {{Name: "1.2.3.4"}}}},
			wantErr: "IP address",
		},
		{
			name: "non-permissive profile without hosts", agentName: "x",
			override: AgentOverride{Image: "img", Command: []string{"run"}, EgressProfile: "standard"},
			wantErr:  "requires egress_hosts",
		},
		{
			name: "default profile (standard) without hosts", agentName: "x",
			override: AgentOverride{Image: "img", Command: []string{"run"}},
			wantErr:  "requires egress_hosts",
		},
		{
			name: "mcp mode config rejected", agentName: "x",
			override: AgentOverride{Image: "img", Command: []string{"run"}, EgressProfile: "permissive",
				MCP: &MCPAgentOverride{Mode: MCPModeConfig}},
			wantErr: "not supported in this version",
		},
		{
			name: "mcp mode env accepted", agentName: "x",
			override: AgentOverride{Image: "img", Command: []string{"run"}, EgressProfile: "permissive",
				MCP: &MCPAgentOverride{Mode: MCPModeEnv}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateCustomAgent(tt.agentName, tt.override, nil)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestValidateCustomAgent_ImageRefValidator(t *testing.T) {
	t.Parallel()

	o := AgentOverride{Image: "img", Command: []string{"run"}, EgressProfile: "permissive"}

	// nil validator: skipped.
	require.NoError(t, ValidateCustomAgent("x", o, nil))

	// failing validator surfaces.
	fakeErr := errors.New("bad ref")
	err := ValidateCustomAgent("x", o, func(string) error { return fakeErr })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid image reference")
}

// TestValidateCustomAgent_CanonicalAiderExample is the issue #191
// acceptance-criteria example: image, command, env_forward, mcp.mode:env — NO
// egress_profile, NO egress_hosts. ValidateCustomAgent must accept this: with
// mcp.mode==env the MCP proxy is the agent's network discovery path, and
// egress.Resolve remains the authoritative runtime safety net (rejects a
// hostless non-permissive profile at VM start), so the load-time gate is
// loosened for the env mode only.
func TestValidateCustomAgent_CanonicalAiderExample(t *testing.T) {
	t.Parallel()

	o := AgentOverride{
		Image:      "ghcr.io/example/aider:latest",
		Command:    []string{"aider"},
		EnvForward: []string{"AIDER_API_KEY"},
		MCP:        &MCPAgentOverride{Mode: MCPModeEnv},
		// No EgressProfile, no EgressHosts — defaults to standard.
	}
	require.NoError(t, ValidateCustomAgent("aider", o, nil))
}
