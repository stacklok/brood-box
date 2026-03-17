// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/egress"
)

func TestReviewConfig_Defaults(t *testing.T) {
	t.Parallel()

	// Zero-value ReviewConfig means review is implicitly disabled (nil pointer).
	var cfg ReviewConfig
	assert.Nil(t, cfg.Enabled)
	assert.Empty(t, cfg.ExcludePatterns)
}

func TestReviewConfig_Explicit(t *testing.T) {
	t.Parallel()

	enabled := true
	cfg := ReviewConfig{
		Enabled:         &enabled,
		ExcludePatterns: []string{"*.log", "tmp/"},
	}
	assert.True(t, *cfg.Enabled)
	assert.Equal(t, []string{"*.log", "tmp/"}, cfg.ExcludePatterns)
}

func boolPtr(b bool) *bool { return &b }

func TestMergeConfigs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		global *Config
		local  *Config
		want   *Config
	}{
		{
			name:   "nil local returns global unchanged",
			global: &Config{Defaults: DefaultsConfig{CPUs: 2, Memory: ByteSize(1024)}},
			local:  nil,
			want:   &Config{Defaults: DefaultsConfig{CPUs: 2, Memory: ByteSize(1024)}},
		},
		{
			name:   "local scalars override global when non-zero",
			global: &Config{Defaults: DefaultsConfig{CPUs: 2, Memory: ByteSize(1024)}},
			local:  &Config{Defaults: DefaultsConfig{CPUs: 4}},
			want:   &Config{Defaults: DefaultsConfig{CPUs: 4, Memory: ByteSize(1024)}},
		},
		{
			name: "review.enabled from local is ignored",
			global: &Config{
				Review: ReviewConfig{Enabled: boolPtr(true)},
			},
			local: &Config{
				Review: ReviewConfig{Enabled: boolPtr(false)},
			},
			want: &Config{
				Review: ReviewConfig{Enabled: boolPtr(true)},
			},
		},
		{
			name: "auth.seed_host_credentials from local is ignored",
			global: &Config{
				Auth: AuthConfig{SeedHostCredentials: nil},
			},
			local: &Config{
				Auth: AuthConfig{SeedHostCredentials: boolPtr(true)},
			},
			want: &Config{
				Auth: AuthConfig{SeedHostCredentials: nil},
			},
		},
		{
			name: "exclude patterns are additive",
			global: &Config{
				Review: ReviewConfig{ExcludePatterns: []string{"*.log"}},
			},
			local: &Config{
				Review: ReviewConfig{ExcludePatterns: []string{"tmp/"}},
			},
			want: &Config{
				Review: ReviewConfig{ExcludePatterns: []string{"*.log", "tmp/"}},
			},
		},
		{
			name: "agents map merge — local extends global",
			global: &Config{
				Agents: map[string]AgentOverride{
					"a": {Image: "img-a"},
				},
			},
			local: &Config{
				Agents: map[string]AgentOverride{
					"b": {Image: "img-b"},
				},
			},
			want: &Config{
				Agents: map[string]AgentOverride{
					"a": {Image: "img-a"},
					"b": {Image: "img-b"},
				},
			},
		},
		{
			name: "agents map merge — local overrides global per key",
			global: &Config{
				Agents: map[string]AgentOverride{
					"a": {Image: "old"},
				},
			},
			local: &Config{
				Agents: map[string]AgentOverride{
					"a": {Image: "new"},
				},
			},
			want: &Config{
				Agents: map[string]AgentOverride{
					"a": {Image: "new"},
				},
			},
		},
		{
			name:   "local agents into nil global agents",
			global: &Config{},
			local: &Config{
				Agents: map[string]AgentOverride{
					"x": {Image: "img-x"},
				},
			},
			want: &Config{
				Agents: map[string]AgentOverride{
					"x": {Image: "img-x"},
				},
			},
		},
		{
			name: "egress profile tighten-only — local locked tightens global standard",
			global: &Config{
				Defaults: DefaultsConfig{EgressProfile: "standard"},
			},
			local: &Config{
				Defaults: DefaultsConfig{EgressProfile: "locked"},
			},
			want: &Config{
				Defaults: DefaultsConfig{EgressProfile: "locked"},
			},
		},
		{
			name: "egress profile tighten-only — local permissive cannot widen global standard",
			global: &Config{
				Defaults: DefaultsConfig{EgressProfile: "standard"},
			},
			local: &Config{
				Defaults: DefaultsConfig{EgressProfile: "permissive"},
			},
			want: &Config{
				Defaults: DefaultsConfig{EgressProfile: "standard"},
			},
		},
		{
			name:   "egress profile — local sets when global is empty",
			global: &Config{},
			local: &Config{
				Defaults: DefaultsConfig{EgressProfile: "locked"},
			},
			want: &Config{
				Defaults: DefaultsConfig{EgressProfile: "locked"},
			},
		},
		{
			name:   "egress profile — empty global + local permissive stays permissive",
			global: &Config{},
			local: &Config{
				Defaults: DefaultsConfig{EgressProfile: "permissive"},
			},
			want: &Config{
				Defaults: DefaultsConfig{EgressProfile: "permissive"},
			},
		},
		{
			name:   "egress profile — empty global + local standard tightens to standard",
			global: &Config{},
			local: &Config{
				Defaults: DefaultsConfig{EgressProfile: "standard"},
			},
			want: &Config{
				Defaults: DefaultsConfig{EgressProfile: "standard"},
			},
		},
		{
			name:   "egress profile — unrecognized local profile treated as permissive",
			global: &Config{},
			local: &Config{
				Defaults: DefaultsConfig{EgressProfile: "not-a-profile"},
			},
			want: &Config{
				Defaults: DefaultsConfig{EgressProfile: "permissive"},
			},
		},
		{
			name: "network allow_hosts are additive",
			global: &Config{
				Network: NetworkConfig{
					AllowHosts: []EgressHostConfig{{Name: "a.com"}},
				},
			},
			local: &Config{
				Network: NetworkConfig{
					AllowHosts: []EgressHostConfig{{Name: "b.com"}},
				},
			},
			want: &Config{
				Network: NetworkConfig{
					AllowHosts: []EgressHostConfig{{Name: "a.com"}, {Name: "b.com"}},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := MergeConfigs(tt.global, tt.local)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMergeConfigs_DoesNotMutateGlobal(t *testing.T) {
	t.Parallel()

	global := &Config{
		Agents: map[string]AgentOverride{
			"a": {Image: "original"},
		},
		Review: ReviewConfig{ExcludePatterns: []string{"*.log"}},
	}
	local := &Config{
		Agents: map[string]AgentOverride{
			"b": {Image: "new"},
		},
		Review: ReviewConfig{ExcludePatterns: []string{"tmp/"}},
	}

	_ = MergeConfigs(global, local)

	// Global should be unchanged.
	assert.Len(t, global.Agents, 1)
	assert.Equal(t, "original", global.Agents["a"].Image)
	assert.Equal(t, []string{"*.log"}, global.Review.ExcludePatterns)
}

func TestMerge(t *testing.T) {
	t.Parallel()

	baseAgent := agent.Agent{
		Name:                 "test-agent",
		Image:                "ghcr.io/example/test:latest",
		Command:              []string{"test-cmd"},
		EnvForward:           []string{"API_KEY"},
		DefaultCPUs:          2,
		DefaultMemory:        2048,
		DefaultEgressProfile: egress.ProfileStandard,
	}

	tests := []struct {
		name     string
		agent    agent.Agent
		override AgentOverride
		defaults DefaultsConfig
		want     agent.Agent
	}{
		{
			name:     "no overrides uses agent defaults",
			agent:    baseAgent,
			override: AgentOverride{},
			defaults: DefaultsConfig{},
			want:     baseAgent,
		},
		{
			name:  "override image",
			agent: baseAgent,
			override: AgentOverride{
				Image: "custom-image:v1",
			},
			defaults: DefaultsConfig{},
			want: agent.Agent{
				Name:                 "test-agent",
				Image:                "custom-image:v1",
				Command:              []string{"test-cmd"},
				EnvForward:           []string{"API_KEY"},
				DefaultCPUs:          2,
				DefaultMemory:        2048,
				DefaultEgressProfile: egress.ProfileStandard,
			},
		},
		{
			name:  "override command",
			agent: baseAgent,
			override: AgentOverride{
				Command: []string{"new-cmd", "--flag"},
			},
			defaults: DefaultsConfig{},
			want: agent.Agent{
				Name:                 "test-agent",
				Image:                "ghcr.io/example/test:latest",
				Command:              []string{"new-cmd", "--flag"},
				EnvForward:           []string{"API_KEY"},
				DefaultCPUs:          2,
				DefaultMemory:        2048,
				DefaultEgressProfile: egress.ProfileStandard,
			},
		},
		{
			name:  "override env forward",
			agent: baseAgent,
			override: AgentOverride{
				EnvForward: []string{"NEW_KEY", "OTHER_*"},
			},
			defaults: DefaultsConfig{},
			want: agent.Agent{
				Name:                 "test-agent",
				Image:                "ghcr.io/example/test:latest",
				Command:              []string{"test-cmd"},
				EnvForward:           []string{"NEW_KEY", "OTHER_*"},
				DefaultCPUs:          2,
				DefaultMemory:        2048,
				DefaultEgressProfile: egress.ProfileStandard,
			},
		},
		{
			name:  "override cpus and memory",
			agent: baseAgent,
			override: AgentOverride{
				CPUs:   4,
				Memory: ByteSize(4096),
			},
			defaults: DefaultsConfig{},
			want: agent.Agent{
				Name:                 "test-agent",
				Image:                "ghcr.io/example/test:latest",
				Command:              []string{"test-cmd"},
				EnvForward:           []string{"API_KEY"},
				DefaultCPUs:          4,
				DefaultMemory:        4096,
				DefaultEgressProfile: egress.ProfileStandard,
			},
		},
		{
			name: "global defaults fill zero agent values",
			agent: agent.Agent{
				Name:    "minimal",
				Image:   "img:latest",
				Command: []string{"cmd"},
			},
			override: AgentOverride{},
			defaults: DefaultsConfig{
				CPUs:   2,
				Memory: ByteSize(1024),
			},
			want: agent.Agent{
				Name:                 "minimal",
				Image:                "img:latest",
				Command:              []string{"cmd"},
				DefaultCPUs:          2,
				DefaultMemory:        1024,
				DefaultEgressProfile: egress.ProfilePermissive,
			},
		},
		{
			name:  "override takes precedence over global defaults",
			agent: agent.Agent{Name: "a", Image: "i:l", Command: []string{"c"}},
			override: AgentOverride{
				CPUs:   8,
				Memory: ByteSize(8192),
			},
			defaults: DefaultsConfig{
				CPUs:   2,
				Memory: ByteSize(1024),
			},
			want: agent.Agent{
				Name:                 "a",
				Image:                "i:l",
				Command:              []string{"c"},
				DefaultCPUs:          8,
				DefaultMemory:        8192,
				DefaultEgressProfile: egress.ProfilePermissive,
			},
		},
		{
			name:     "agent values take precedence over global defaults",
			agent:    baseAgent,
			override: AgentOverride{},
			defaults: DefaultsConfig{
				CPUs:   1,
				Memory: ByteSize(512),
			},
			want: baseAgent,
		},
		{
			name: "egress profile — override takes precedence",
			agent: agent.Agent{
				Name:                 "a",
				Image:                "i:l",
				Command:              []string{"c"},
				DefaultEgressProfile: egress.ProfileStandard,
			},
			override: AgentOverride{EgressProfile: "locked"},
			defaults: DefaultsConfig{},
			want: agent.Agent{
				Name:                 "a",
				Image:                "i:l",
				Command:              []string{"c"},
				DefaultEgressProfile: egress.ProfileLocked,
			},
		},
		{
			name: "egress profile — global default fills empty",
			agent: agent.Agent{
				Name:    "a",
				Image:   "i:l",
				Command: []string{"c"},
			},
			override: AgentOverride{},
			defaults: DefaultsConfig{EgressProfile: "locked"},
			want: agent.Agent{
				Name:                 "a",
				Image:                "i:l",
				Command:              []string{"c"},
				DefaultEgressProfile: egress.ProfileLocked,
			},
		},
		{
			name: "egress profile — permissive override blocked by standard agent",
			agent: agent.Agent{
				Name:                 "a",
				Image:                "i:l",
				Command:              []string{"c"},
				DefaultEgressProfile: egress.ProfileStandard,
			},
			override: AgentOverride{EgressProfile: "permissive"},
			defaults: DefaultsConfig{},
			want: agent.Agent{
				Name:                 "a",
				Image:                "i:l",
				Command:              []string{"c"},
				DefaultEgressProfile: egress.ProfileStandard,
			},
		},
		{
			name: "egress profile — permissive override allowed when agent empty",
			agent: agent.Agent{
				Name:    "a",
				Image:   "i:l",
				Command: []string{"c"},
			},
			override: AgentOverride{EgressProfile: "permissive"},
			defaults: DefaultsConfig{},
			want: agent.Agent{
				Name:                 "a",
				Image:                "i:l",
				Command:              []string{"c"},
				DefaultEgressProfile: egress.ProfilePermissive,
			},
		},
		{
			name: "egress profile — locked override allowed when agent empty",
			agent: agent.Agent{
				Name:    "a",
				Image:   "i:l",
				Command: []string{"c"},
			},
			override: AgentOverride{EgressProfile: "locked"},
			defaults: DefaultsConfig{},
			want: agent.Agent{
				Name:                 "a",
				Image:                "i:l",
				Command:              []string{"c"},
				DefaultEgressProfile: egress.ProfileLocked,
			},
		},
		{
			name: "egress profile — falls back to permissive when all empty",
			agent: agent.Agent{
				Name:    "a",
				Image:   "i:l",
				Command: []string{"c"},
			},
			override: AgentOverride{},
			defaults: DefaultsConfig{},
			want: agent.Agent{
				Name:                 "a",
				Image:                "i:l",
				Command:              []string{"c"},
				DefaultEgressProfile: egress.ProfilePermissive,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Merge(tt.agent, tt.override, tt.defaults)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestToEgressHosts(t *testing.T) {
	t.Parallel()

	t.Run("nil input returns nil", func(t *testing.T) {
		t.Parallel()
		got, err := ToEgressHosts(nil)
		require.NoError(t, err)
		assert.Nil(t, got)
	})

	t.Run("converts config to domain hosts", func(t *testing.T) {
		t.Parallel()
		configs := []EgressHostConfig{
			{Name: "api.example.com", Ports: []uint16{443}, Protocol: 6},
			{Name: "*.docker.io"},
		}
		got, err := ToEgressHosts(configs)
		require.NoError(t, err)
		assert.Len(t, got, 2)
		assert.Equal(t, "api.example.com", got[0].Name)
		assert.Equal(t, []uint16{443}, got[0].Ports)
		assert.Equal(t, uint8(6), got[0].Protocol)
		assert.Equal(t, "*.docker.io", got[1].Name)
		assert.Nil(t, got[1].Ports)
	})

	t.Run("lowercases hostnames", func(t *testing.T) {
		t.Parallel()
		configs := []EgressHostConfig{
			{Name: "API.GitHub.COM"},
		}
		got, err := ToEgressHosts(configs)
		require.NoError(t, err)
		assert.Equal(t, "api.github.com", got[0].Name)
	})

	t.Run("rejects IP address in config", func(t *testing.T) {
		t.Parallel()
		configs := []EgressHostConfig{
			{Name: "api.example.com"},
			{Name: "1.2.3.4"},
		}
		_, err := ToEgressHosts(configs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "allow_hosts[1]")
		assert.Contains(t, err.Error(), "IP address")
	})

	t.Run("rejects bare wildcard in config", func(t *testing.T) {
		t.Parallel()
		configs := []EgressHostConfig{
			{Name: "*"},
		}
		_, err := ToEgressHosts(configs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "allow_hosts[0]")
		assert.Contains(t, err.Error(), "bare wildcard")
	})
}

func TestAuthConfig_SeedHostCredentialsEnabled(t *testing.T) {
	t.Parallel()
	// Defaults to false when nil.
	var a AuthConfig
	if a.SeedHostCredentialsEnabled() {
		t.Fatal("expected default false")
	}
	// Explicit true.
	tr := true
	a.SeedHostCredentials = &tr
	if !a.SeedHostCredentialsEnabled() {
		t.Fatal("expected true")
	}
	// Explicit false.
	f := false
	a.SeedHostCredentials = &f
	if a.SeedHostCredentialsEnabled() {
		t.Fatal("expected false")
	}
}

func TestMergeGitConfig(t *testing.T) {
	t.Parallel()

	boolPtr := func(b bool) *bool { return &b }

	tests := []struct {
		name   string
		global GitConfig
		local  GitConfig
		want   GitConfig
	}{
		{
			name:   "both nil defaults to enabled",
			global: GitConfig{},
			local:  GitConfig{},
			want:   GitConfig{},
		},
		{
			name:   "local can disable token forwarding",
			global: GitConfig{},
			local:  GitConfig{ForwardToken: boolPtr(false)},
			want:   GitConfig{ForwardToken: boolPtr(false)},
		},
		{
			name:   "local cannot re-enable token forwarding",
			global: GitConfig{ForwardToken: boolPtr(false)},
			local:  GitConfig{ForwardToken: boolPtr(true)},
			want:   GitConfig{ForwardToken: boolPtr(false)},
		},
		{
			name:   "local can disable SSH agent forwarding",
			global: GitConfig{},
			local:  GitConfig{ForwardSSHAgent: boolPtr(false)},
			want:   GitConfig{ForwardSSHAgent: boolPtr(false)},
		},
		{
			name:   "local cannot re-enable SSH agent forwarding",
			global: GitConfig{ForwardSSHAgent: boolPtr(false)},
			local:  GitConfig{ForwardSSHAgent: boolPtr(true)},
			want:   GitConfig{ForwardSSHAgent: boolPtr(false)},
		},
		{
			name:   "global enabled plus local nil stays enabled",
			global: GitConfig{ForwardToken: boolPtr(true)},
			local:  GitConfig{},
			want:   GitConfig{ForwardToken: boolPtr(true)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := mergeGitConfig(tt.global, tt.local)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGitConfig_Defaults(t *testing.T) {
	t.Parallel()

	var cfg GitConfig
	assert.True(t, cfg.GitTokenEnabled(), "nil ForwardToken should default to true")
	assert.True(t, cfg.SSHAgentEnabled(), "nil ForwardSSHAgent should default to true")

	boolPtr := func(b bool) *bool { return &b }

	cfg.ForwardToken = boolPtr(false)
	assert.False(t, cfg.GitTokenEnabled())

	cfg.ForwardSSHAgent = boolPtr(false)
	assert.False(t, cfg.SSHAgentEnabled())
}

func TestMergeConfigs_ResourceBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		global     *Config
		local      *Config
		wantCPUs   uint32
		wantMemory ByteSize
	}{
		{
			name:       "normal values pass through",
			global:     &Config{Defaults: DefaultsConfig{CPUs: 4, Memory: ByteSize(4096)}},
			local:      &Config{Defaults: DefaultsConfig{CPUs: 8, Memory: ByteSize(8192)}},
			wantCPUs:   8,
			wantMemory: ByteSize(8192),
		},
		{
			name:       "local CPUs exceed max — clamped",
			global:     &Config{Defaults: DefaultsConfig{CPUs: 4, Memory: ByteSize(2048)}},
			local:      &Config{Defaults: DefaultsConfig{CPUs: 256}},
			wantCPUs:   MaxCPUs,
			wantMemory: ByteSize(2048),
		},
		{
			name:       "local memory exceeds max — clamped",
			global:     &Config{Defaults: DefaultsConfig{CPUs: 4, Memory: ByteSize(2048)}},
			local:      &Config{Defaults: DefaultsConfig{Memory: ByteSize(999999)}},
			wantCPUs:   4,
			wantMemory: MaxMemory,
		},
		{
			name:       "both exceed max — both clamped",
			global:     &Config{},
			local:      &Config{Defaults: DefaultsConfig{CPUs: 500, Memory: ByteSize(500000)}},
			wantCPUs:   MaxCPUs,
			wantMemory: MaxMemory,
		},
		{
			name:       "zero local does not override global",
			global:     &Config{Defaults: DefaultsConfig{CPUs: 4, Memory: ByteSize(2048)}},
			local:      &Config{Defaults: DefaultsConfig{}},
			wantCPUs:   4,
			wantMemory: ByteSize(2048),
		},
		{
			name:       "at boundary — exactly max passes through",
			global:     &Config{},
			local:      &Config{Defaults: DefaultsConfig{CPUs: MaxCPUs, Memory: MaxMemory}},
			wantCPUs:   MaxCPUs,
			wantMemory: MaxMemory,
		},
		{
			name:       "global exceeds max — clamped even without local override",
			global:     &Config{Defaults: DefaultsConfig{CPUs: 200, Memory: ByteSize(200000)}},
			local:      &Config{},
			wantCPUs:   MaxCPUs,
			wantMemory: MaxMemory,
		},
		{
			name:       "one over one under — only over is clamped",
			global:     &Config{Defaults: DefaultsConfig{CPUs: 4}},
			local:      &Config{Defaults: DefaultsConfig{Memory: ByteSize(999999)}},
			wantCPUs:   4,
			wantMemory: MaxMemory,
		},
		{
			name:       "MaxUint32 values are clamped",
			global:     &Config{},
			local:      &Config{Defaults: DefaultsConfig{CPUs: math.MaxUint32, Memory: ByteSize(math.MaxUint32)}},
			wantCPUs:   MaxCPUs,
			wantMemory: MaxMemory,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := MergeConfigs(tt.global, tt.local)
			assert.Equal(t, tt.wantCPUs, got.Defaults.CPUs, "CPUs")
			assert.Equal(t, tt.wantMemory, got.Defaults.Memory, "Memory")
		})
	}
}

func TestMergeConfigs_TmpSizeTightenOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		global  *Config
		local   *Config
		wantTmp ByteSize
	}{
		{
			name:    "local smaller than global — accepted",
			global:  &Config{Defaults: DefaultsConfig{TmpSize: 512}},
			local:   &Config{Defaults: DefaultsConfig{TmpSize: 256}},
			wantTmp: 256,
		},
		{
			name:    "local larger than global — rejected",
			global:  &Config{Defaults: DefaultsConfig{TmpSize: 512}},
			local:   &Config{Defaults: DefaultsConfig{TmpSize: 2048}},
			wantTmp: 512,
		},
		{
			name:    "local zero does not override global",
			global:  &Config{Defaults: DefaultsConfig{TmpSize: 512}},
			local:   &Config{Defaults: DefaultsConfig{}},
			wantTmp: 512,
		},
		{
			name:    "global zero local sets — accepted",
			global:  &Config{Defaults: DefaultsConfig{}},
			local:   &Config{Defaults: DefaultsConfig{TmpSize: 256}},
			wantTmp: 256,
		},
		{
			name:    "local exceeds MaxTmpSize — clamped",
			global:  &Config{Defaults: DefaultsConfig{}},
			local:   &Config{Defaults: DefaultsConfig{TmpSize: MaxTmpSize + 1}},
			wantTmp: MaxTmpSize,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := MergeConfigs(tt.global, tt.local)
			assert.Equal(t, tt.wantTmp, got.Defaults.TmpSize, "TmpSize")
		})
	}
}

func TestStricterMCPAuthzProfile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a    string
		b    string
		want string
	}{
		{name: "both empty defaults to full-access", a: "", b: "", want: MCPAuthzProfileFullAccess},
		{name: "observe is stricter than safe-tools", a: MCPAuthzProfileSafeTools, b: MCPAuthzProfileObserve, want: MCPAuthzProfileObserve},
		{name: "observe is stricter than full-access", a: MCPAuthzProfileFullAccess, b: MCPAuthzProfileObserve, want: MCPAuthzProfileObserve},
		{name: "safe-tools is stricter than full-access", a: MCPAuthzProfileFullAccess, b: MCPAuthzProfileSafeTools, want: MCPAuthzProfileSafeTools},
		{name: "same profile returns itself", a: MCPAuthzProfileObserve, b: MCPAuthzProfileObserve, want: MCPAuthzProfileObserve},
		{name: "empty a treated as full-access", a: "", b: MCPAuthzProfileObserve, want: MCPAuthzProfileObserve},
		{name: "empty b treated as full-access", a: MCPAuthzProfileObserve, b: "", want: MCPAuthzProfileObserve},
		{name: "custom a preserved", a: MCPAuthzProfileCustom, b: MCPAuthzProfileObserve, want: MCPAuthzProfileCustom},
		{name: "custom b preserved", a: MCPAuthzProfileObserve, b: MCPAuthzProfileCustom, want: MCPAuthzProfileCustom},
		{name: "both custom returns a", a: MCPAuthzProfileCustom, b: MCPAuthzProfileCustom, want: MCPAuthzProfileCustom},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := StricterMCPAuthzProfile(tt.a, tt.b)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIsValidMCPAuthzProfile(t *testing.T) {
	t.Parallel()

	assert.True(t, IsValidMCPAuthzProfile(MCPAuthzProfileFullAccess))
	assert.True(t, IsValidMCPAuthzProfile(MCPAuthzProfileObserve))
	assert.True(t, IsValidMCPAuthzProfile(MCPAuthzProfileSafeTools))
	assert.True(t, IsValidMCPAuthzProfile(MCPAuthzProfileCustom))
	assert.False(t, IsValidMCPAuthzProfile(""))
	assert.False(t, IsValidMCPAuthzProfile("unknown"))
}

func TestMergeConfigs_MCPAuthz(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		global *Config
		local  *Config
		want   *MCPAuthzConfig
	}{
		{
			name:   "both nil — no authz",
			global: &Config{},
			local:  &Config{},
			want:   nil,
		},
		{
			name:   "global set, local nil — global preserved",
			global: &Config{MCP: MCPConfig{Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileObserve}}},
			local:  &Config{},
			want:   &MCPAuthzConfig{Profile: MCPAuthzProfileObserve},
		},
		{
			name:   "global nil, local set — local applied",
			global: &Config{},
			local:  &Config{MCP: MCPConfig{Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileSafeTools}}},
			want:   &MCPAuthzConfig{Profile: MCPAuthzProfileSafeTools},
		},
		{
			name:   "local can tighten — observe beats safe-tools",
			global: &Config{MCP: MCPConfig{Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileSafeTools}}},
			local:  &Config{MCP: MCPConfig{Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileObserve}}},
			want:   &MCPAuthzConfig{Profile: MCPAuthzProfileObserve},
		},
		{
			name:   "local cannot widen — full-access blocked by observe",
			global: &Config{MCP: MCPConfig{Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileObserve}}},
			local:  &Config{MCP: MCPConfig{Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileFullAccess}}},
			want:   &MCPAuthzConfig{Profile: MCPAuthzProfileObserve},
		},
		{
			name:   "local cannot widen — safe-tools blocked by observe",
			global: &Config{MCP: MCPConfig{Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileObserve}}},
			local:  &Config{MCP: MCPConfig{Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileSafeTools}}},
			want:   &MCPAuthzConfig{Profile: MCPAuthzProfileObserve},
		},
		{
			name:   "custom from local config is ignored — global preserved",
			global: &Config{MCP: MCPConfig{Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileObserve}}},
			local:  &Config{MCP: MCPConfig{Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileCustom}}},
			want:   &MCPAuthzConfig{Profile: MCPAuthzProfileObserve},
		},
		{
			name:   "custom from local config is ignored — nil global stays nil",
			global: &Config{},
			local:  &Config{MCP: MCPConfig{Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileCustom}}},
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := MergeConfigs(tt.global, tt.local)
			assert.Equal(t, tt.want, got.MCP.Authz)
		})
	}
}

func TestMergeConfigs_AgentOverridesMCPSecurityFieldsStripped(t *testing.T) {
	t.Parallel()

	policies := []string{`permit(principal, action, resource);`}
	global := &Config{
		Agents: map[string]AgentOverride{
			"claude-code": {
				MCP: &MCPConfig{
					Group: "global-group",
					Config: &MCPFileConfig{
						Authz: &MCPFileAuthzConfig{Policies: policies},
					},
					Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileObserve},
				},
			},
		},
	}
	local := &Config{
		Agents: map[string]AgentOverride{
			"claude-code": {
				MCP: &MCPConfig{
					Group: "local-group",
					Config: &MCPFileConfig{
						Authz: &MCPFileAuthzConfig{Policies: policies},
					},
					Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileFullAccess},
				},
			},
			"codex": {
				MCP: &MCPConfig{
					Config: &MCPFileConfig{
						Authz: &MCPFileAuthzConfig{Policies: policies},
					},
					Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileSafeTools},
				},
			},
		},
	}

	result := MergeConfigs(global, local)

	// Local agent override MCP.Config and MCP.Authz must be stripped.
	claude := result.Agents["claude-code"]
	assert.NotNil(t, claude.MCP, "MCP should be preserved")
	assert.Equal(t, "local-group", claude.MCP.Group, "non-security MCP fields should survive")
	assert.Nil(t, claude.MCP.Config, "MCP.Config from local override must be stripped")
	assert.Nil(t, claude.MCP.Authz, "MCP.Authz from local override must be stripped")

	codex := result.Agents["codex"]
	assert.NotNil(t, codex.MCP)
	assert.Nil(t, codex.MCP.Config, "MCP.Config from local override must be stripped")
	assert.Nil(t, codex.MCP.Authz, "MCP.Authz from local override must be stripped")

	// Global agent override security fields are preserved (trusted config).
	assert.NotNil(t, global.Agents["claude-code"].MCP.Config, "global input must not be mutated")
	assert.NotNil(t, global.Agents["claude-code"].MCP.Authz, "global input must not be mutated")
}

func TestMerge_ResourceBounds(t *testing.T) {
	t.Parallel()

	baseAgent := agent.Agent{
		Name:                 "test",
		Image:                "img:latest",
		Command:              []string{"cmd"},
		DefaultCPUs:          2,
		DefaultMemory:        2048,
		DefaultEgressProfile: egress.ProfileStandard,
	}

	tests := []struct {
		name       string
		agent      agent.Agent
		override   AgentOverride
		defaults   DefaultsConfig
		wantCPUs   uint32
		wantMemory uint32
	}{
		{
			name:       "normal values pass through",
			agent:      baseAgent,
			override:   AgentOverride{CPUs: 4, Memory: ByteSize(4096)},
			defaults:   DefaultsConfig{},
			wantCPUs:   4,
			wantMemory: 4096,
		},
		{
			name:       "override CPUs exceed max — clamped",
			agent:      baseAgent,
			override:   AgentOverride{CPUs: 256},
			defaults:   DefaultsConfig{},
			wantCPUs:   MaxCPUs,
			wantMemory: 2048,
		},
		{
			name:       "override memory exceeds max — clamped",
			agent:      baseAgent,
			override:   AgentOverride{Memory: ByteSize(999999)},
			defaults:   DefaultsConfig{},
			wantCPUs:   2,
			wantMemory: MaxMemory.MiB(),
		},
		{
			name:       "global defaults exceed max — clamped",
			agent:      agent.Agent{Name: "a", Image: "i:l", Command: []string{"c"}, DefaultEgressProfile: "standard"},
			override:   AgentOverride{},
			defaults:   DefaultsConfig{CPUs: 500, Memory: ByteSize(500000)},
			wantCPUs:   MaxCPUs,
			wantMemory: MaxMemory.MiB(),
		},
		{
			name:       "agent defaults exceed max — clamped",
			agent:      agent.Agent{Name: "a", Image: "i:l", Command: []string{"c"}, DefaultCPUs: 200, DefaultMemory: 200000, DefaultEgressProfile: "standard"},
			override:   AgentOverride{},
			defaults:   DefaultsConfig{},
			wantCPUs:   MaxCPUs,
			wantMemory: MaxMemory.MiB(),
		},
		{
			name:       "zero override uses agent defaults",
			agent:      baseAgent,
			override:   AgentOverride{},
			defaults:   DefaultsConfig{},
			wantCPUs:   2,
			wantMemory: 2048,
		},
		{
			name:       "at boundary — exactly max passes through",
			agent:      baseAgent,
			override:   AgentOverride{CPUs: MaxCPUs, Memory: MaxMemory},
			defaults:   DefaultsConfig{},
			wantCPUs:   MaxCPUs,
			wantMemory: MaxMemory.MiB(),
		},
		{
			name:       "MaxUint32 values are clamped",
			agent:      baseAgent,
			override:   AgentOverride{CPUs: math.MaxUint32, Memory: ByteSize(math.MaxUint32)},
			defaults:   DefaultsConfig{},
			wantCPUs:   MaxCPUs,
			wantMemory: MaxMemory.MiB(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Merge(tt.agent, tt.override, tt.defaults)
			assert.Equal(t, tt.wantCPUs, got.DefaultCPUs, "CPUs")
			assert.Equal(t, tt.wantMemory, got.DefaultMemory, "Memory")
		})
	}
}

func TestMCPFileConfig_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     *MCPFileConfig
		wantErr string
	}{
		{
			name: "nil config is valid",
			cfg:  nil,
		},
		{
			name: "empty config is valid",
			cfg:  &MCPFileConfig{},
		},
		{
			name: "authz with policies is valid",
			cfg: &MCPFileConfig{
				Authz: &MCPFileAuthzConfig{
					Policies: []string{`permit(principal, action, resource);`},
				},
			},
		},
		{
			name: "authz with empty policies is invalid",
			cfg: &MCPFileConfig{
				Authz: &MCPFileAuthzConfig{
					Policies: []string{},
				},
			},
			wantErr: "authz.policies must be non-empty",
		},
		{
			name: "authz with nil policies is invalid",
			cfg: &MCPFileConfig{
				Authz: &MCPFileAuthzConfig{},
			},
			wantErr: "authz.policies must be non-empty",
		},
		{
			name: "valid conflict_resolution prefix",
			cfg: &MCPFileConfig{
				Aggregation: &MCPAggregationConfig{
					ConflictResolution: "prefix",
				},
			},
		},
		{
			name: "valid conflict_resolution priority",
			cfg: &MCPFileConfig{
				Aggregation: &MCPAggregationConfig{
					ConflictResolution: "priority",
				},
			},
		},
		{
			name: "valid conflict_resolution manual",
			cfg: &MCPFileConfig{
				Aggregation: &MCPAggregationConfig{
					ConflictResolution: "manual",
				},
			},
		},
		{
			name: "invalid conflict_resolution",
			cfg: &MCPFileConfig{
				Aggregation: &MCPAggregationConfig{
					ConflictResolution: "invalid",
				},
			},
			wantErr: "conflict_resolution must be one of",
		},
		{
			name: "empty conflict_resolution is valid (optional)",
			cfg: &MCPFileConfig{
				Aggregation: &MCPAggregationConfig{},
			},
		},
		{
			name: "full config is valid",
			cfg: &MCPFileConfig{
				Authz: &MCPFileAuthzConfig{
					Policies: []string{`permit(principal, action, resource);`},
				},
				Aggregation: &MCPAggregationConfig{
					ConflictResolution: "prefix",
					PrefixFormat:       "{workload}_",
					Tools: []MCPWorkloadToolConfig{
						{Workload: "github", Filter: []string{"search_code"}},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.cfg.Validate()
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
