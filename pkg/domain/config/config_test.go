// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/bytesize"
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

func TestMerge_UsesGlobalTmpSizeWhenAgentDefaultUnset(t *testing.T) {
	t.Parallel()

	merged := Merge(
		agent.Agent{Name: "test-agent"},
		AgentOverride{},
		DefaultsConfig{TmpSize: bytesize.ByteSize(2048)},
	)

	assert.Equal(t, bytesize.ByteSize(2048), merged.DefaultTmpSize)
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
			global: &Config{Defaults: DefaultsConfig{CPUs: 2, Memory: bytesize.ByteSize(1024)}},
			local:  nil,
			want:   &Config{Defaults: DefaultsConfig{CPUs: 2, Memory: bytesize.ByteSize(1024)}},
		},
		{
			name:   "local scalars override global when non-zero",
			global: &Config{Defaults: DefaultsConfig{CPUs: 2, Memory: bytesize.ByteSize(1024)}},
			local:  &Config{Defaults: DefaultsConfig{CPUs: 4}},
			want:   &Config{Defaults: DefaultsConfig{CPUs: 4, Memory: bytesize.ByteSize(1024)}},
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
		DefaultMemory:        bytesize.ByteSize(2048),
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
				DefaultMemory:        bytesize.ByteSize(2048),
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
				DefaultMemory:        bytesize.ByteSize(2048),
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
				DefaultMemory:        bytesize.ByteSize(2048),
				DefaultEgressProfile: egress.ProfileStandard,
			},
		},
		{
			name:  "override cpus and memory",
			agent: baseAgent,
			override: AgentOverride{
				CPUs:   4,
				Memory: bytesize.ByteSize(4096),
			},
			defaults: DefaultsConfig{},
			want: agent.Agent{
				Name:                 "test-agent",
				Image:                "ghcr.io/example/test:latest",
				Command:              []string{"test-cmd"},
				EnvForward:           []string{"API_KEY"},
				DefaultCPUs:          4,
				DefaultMemory:        bytesize.ByteSize(4096),
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
				Memory: bytesize.ByteSize(1024),
			},
			want: agent.Agent{
				Name:                 "minimal",
				Image:                "img:latest",
				Command:              []string{"cmd"},
				DefaultCPUs:          2,
				DefaultMemory:        bytesize.ByteSize(1024),
				DefaultEgressProfile: egress.ProfilePermissive,
			},
		},
		{
			name:  "override takes precedence over global defaults",
			agent: agent.Agent{Name: "a", Image: "i:l", Command: []string{"c"}},
			override: AgentOverride{
				CPUs:   8,
				Memory: bytesize.ByteSize(8192),
			},
			defaults: DefaultsConfig{
				CPUs:   2,
				Memory: bytesize.ByteSize(1024),
			},
			want: agent.Agent{
				Name:                 "a",
				Image:                "i:l",
				Command:              []string{"c"},
				DefaultCPUs:          8,
				DefaultMemory:        bytesize.ByteSize(8192),
				DefaultEgressProfile: egress.ProfilePermissive,
			},
		},
		{
			name:     "agent values take precedence over global defaults",
			agent:    baseAgent,
			override: AgentOverride{},
			defaults: DefaultsConfig{
				CPUs:   1,
				Memory: bytesize.ByteSize(512),
			},
			want: baseAgent,
		},
		{
			name: "global defaults do not override built-in resource defaults",
			agent: agent.Agent{
				Name:                 "a",
				Image:                "i:l",
				Command:              []string{"c"},
				DefaultCPUs:          2,
				DefaultMemory:        bytesize.ByteSize(4096),
				DefaultTmpSize:       bytesize.ByteSize(512),
				DefaultEgressProfile: egress.ProfileStandard,
			},
			override: AgentOverride{},
			defaults: DefaultsConfig{
				CPUs:    6,
				Memory:  bytesize.ByteSize(8192),
				TmpSize: bytesize.ByteSize(2048),
			},
			want: agent.Agent{
				Name:                 "a",
				Image:                "i:l",
				Command:              []string{"c"},
				DefaultCPUs:          2,
				DefaultMemory:        bytesize.ByteSize(4096),
				DefaultTmpSize:       bytesize.ByteSize(512),
				DefaultEgressProfile: egress.ProfileStandard,
			},
		},
		{
			name: "override still beats global defaults and built-ins",
			agent: agent.Agent{
				Name:                 "a",
				Image:                "i:l",
				Command:              []string{"c"},
				DefaultCPUs:          2,
				DefaultMemory:        bytesize.ByteSize(4096),
				DefaultTmpSize:       bytesize.ByteSize(512),
				DefaultEgressProfile: egress.ProfileStandard,
			},
			override: AgentOverride{
				CPUs:    8,
				Memory:  bytesize.ByteSize(16384),
				TmpSize: bytesize.ByteSize(4096),
			},
			defaults: DefaultsConfig{
				CPUs:    6,
				Memory:  bytesize.ByteSize(8192),
				TmpSize: bytesize.ByteSize(2048),
			},
			want: agent.Agent{
				Name:                 "a",
				Image:                "i:l",
				Command:              []string{"c"},
				DefaultCPUs:          8,
				DefaultMemory:        bytesize.ByteSize(16384),
				DefaultTmpSize:       bytesize.ByteSize(4096),
				DefaultEgressProfile: egress.ProfileStandard,
			},
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
		wantMemory bytesize.ByteSize
	}{
		{
			name:       "normal values pass through",
			global:     &Config{Defaults: DefaultsConfig{CPUs: 4, Memory: bytesize.ByteSize(4096)}},
			local:      &Config{Defaults: DefaultsConfig{CPUs: 8, Memory: bytesize.ByteSize(8192)}},
			wantCPUs:   8,
			wantMemory: bytesize.ByteSize(8192),
		},
		{
			name:       "local CPUs exceed max — clamped",
			global:     &Config{Defaults: DefaultsConfig{CPUs: 4, Memory: bytesize.ByteSize(2048)}},
			local:      &Config{Defaults: DefaultsConfig{CPUs: 256}},
			wantCPUs:   MaxCPUs,
			wantMemory: bytesize.ByteSize(2048),
		},
		{
			name:       "local memory exceeds max — clamped",
			global:     &Config{Defaults: DefaultsConfig{CPUs: 4, Memory: bytesize.ByteSize(2048)}},
			local:      &Config{Defaults: DefaultsConfig{Memory: bytesize.ByteSize(999999)}},
			wantCPUs:   4,
			wantMemory: MaxMemory,
		},
		{
			name:       "both exceed max — both clamped",
			global:     &Config{},
			local:      &Config{Defaults: DefaultsConfig{CPUs: 500, Memory: bytesize.ByteSize(500000)}},
			wantCPUs:   MaxCPUs,
			wantMemory: MaxMemory,
		},
		{
			name:       "zero local does not override global",
			global:     &Config{Defaults: DefaultsConfig{CPUs: 4, Memory: bytesize.ByteSize(2048)}},
			local:      &Config{Defaults: DefaultsConfig{}},
			wantCPUs:   4,
			wantMemory: bytesize.ByteSize(2048),
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
			global:     &Config{Defaults: DefaultsConfig{CPUs: 200, Memory: bytesize.ByteSize(200000)}},
			local:      &Config{},
			wantCPUs:   MaxCPUs,
			wantMemory: MaxMemory,
		},
		{
			name:       "one over one under — only over is clamped",
			global:     &Config{Defaults: DefaultsConfig{CPUs: 4}},
			local:      &Config{Defaults: DefaultsConfig{Memory: bytesize.ByteSize(999999)}},
			wantCPUs:   4,
			wantMemory: MaxMemory,
		},
		{
			name:       "MaxUint32 values are clamped",
			global:     &Config{},
			local:      &Config{Defaults: DefaultsConfig{CPUs: math.MaxUint32, Memory: bytesize.ByteSize(math.MaxUint32)}},
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
		wantTmp bytesize.ByteSize
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

func TestMergeConfigs_AgentOverridesMCPAuthzTightenOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		global      *Config
		local       *Config
		agent       string
		wantAuthz   *MCPAuthzConfig
		wantEnabled *bool // if non-nil, assert MCP.Enabled matches
	}{
		{
			name: "local can tighten per-agent authz — observe beats safe-tools",
			global: &Config{
				Agents: map[string]AgentOverride{
					"claude-code": {
						MCP: &MCPAgentOverride{
							Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileSafeTools},
						},
					},
				},
			},
			local: &Config{
				Agents: map[string]AgentOverride{
					"claude-code": {
						MCP: &MCPAgentOverride{
							Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileObserve},
						},
					},
				},
			},
			agent:     "claude-code",
			wantAuthz: &MCPAuthzConfig{Profile: MCPAuthzProfileObserve},
		},
		{
			name: "local cannot widen per-agent authz — full-access blocked by observe",
			global: &Config{
				Agents: map[string]AgentOverride{
					"claude-code": {
						MCP: &MCPAgentOverride{
							Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileObserve},
						},
					},
				},
			},
			local: &Config{
				Agents: map[string]AgentOverride{
					"claude-code": {
						MCP: &MCPAgentOverride{
							Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileFullAccess},
						},
					},
				},
			},
			agent:     "claude-code",
			wantAuthz: &MCPAuthzConfig{Profile: MCPAuthzProfileObserve},
		},
		{
			name:   "local sets per-agent authz when global has none",
			global: &Config{},
			local: &Config{
				Agents: map[string]AgentOverride{
					"codex": {
						MCP: &MCPAgentOverride{
							Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileSafeTools},
						},
					},
				},
			},
			agent:     "codex",
			wantAuthz: &MCPAuthzConfig{Profile: MCPAuthzProfileSafeTools},
		},
		{
			name:   "custom from local per-agent config is ignored",
			global: &Config{},
			local: &Config{
				Agents: map[string]AgentOverride{
					"codex": {
						MCP: &MCPAgentOverride{
							Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileCustom},
						},
					},
				},
			},
			agent:     "codex",
			wantAuthz: nil,
		},
		{
			name: "enabled field survives alongside authz merge",
			global: &Config{
				Agents: map[string]AgentOverride{
					"claude-code": {
						MCP: &MCPAgentOverride{
							Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileSafeTools},
						},
					},
				},
			},
			local: &Config{
				Agents: map[string]AgentOverride{
					"claude-code": {
						MCP: &MCPAgentOverride{
							Enabled: boolPtr(false),
							Authz:   &MCPAuthzConfig{Profile: MCPAuthzProfileObserve},
						},
					},
				},
			},
			agent:       "claude-code",
			wantAuthz:   &MCPAuthzConfig{Profile: MCPAuthzProfileObserve},
			wantEnabled: boolPtr(false),
		},
		{
			name: "local MCP with only Enabled set preserves global authz",
			global: &Config{
				Agents: map[string]AgentOverride{
					"claude-code": {
						MCP: &MCPAgentOverride{
							Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileSafeTools},
						},
					},
				},
			},
			local: &Config{
				Agents: map[string]AgentOverride{
					"claude-code": {
						MCP: &MCPAgentOverride{
							Enabled: boolPtr(false),
						},
					},
				},
			},
			agent:       "claude-code",
			wantAuthz:   &MCPAuthzConfig{Profile: MCPAuthzProfileSafeTools},
			wantEnabled: boolPtr(false),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := MergeConfigs(tt.global, tt.local)
			agentOverride, ok := result.Agents[tt.agent]
			if tt.wantAuthz == nil {
				if ok && agentOverride.MCP != nil && agentOverride.MCP.Authz != nil {
					t.Errorf("expected nil authz, got %+v", agentOverride.MCP.Authz)
				}
			} else {
				require.True(t, ok, "agent should exist in result")
				require.NotNil(t, agentOverride.MCP, "MCP should be present")
				assert.Equal(t, tt.wantAuthz, agentOverride.MCP.Authz)
			}
			if tt.wantEnabled != nil && ok && agentOverride.MCP != nil {
				require.NotNil(t, agentOverride.MCP.Enabled, "MCP.Enabled should be set")
				assert.Equal(t, *tt.wantEnabled, *agentOverride.MCP.Enabled, "MCP.Enabled")
			}
		})
	}
}

func TestMergeConfigs_AgentOverridesMCPDoesNotMutateGlobal(t *testing.T) {
	t.Parallel()

	global := &Config{
		Agents: map[string]AgentOverride{
			"claude-code": {
				MCP: &MCPAgentOverride{
					Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileObserve},
				},
			},
		},
	}
	local := &Config{
		Agents: map[string]AgentOverride{
			"claude-code": {
				MCP: &MCPAgentOverride{
					Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileSafeTools},
				},
			},
		},
	}

	_ = MergeConfigs(global, local)

	// Global agent override must not be mutated.
	assert.NotNil(t, global.Agents["claude-code"].MCP.Authz, "global input must not be mutated")
	assert.Equal(t, MCPAuthzProfileObserve, global.Agents["claude-code"].MCP.Authz.Profile)
}

func TestMergeAgentOverride_FieldByField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		global AgentOverride
		local  AgentOverride
		want   AgentOverride
	}{
		{
			name: "local resource override preserves global security fields",
			global: AgentOverride{
				Image: "original:v1",
				MCP: &MCPAgentOverride{
					Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileObserve},
				},
				EgressProfile: "locked",
			},
			local: AgentOverride{
				CPUs: 4,
			},
			want: AgentOverride{
				Image: "original:v1",
				CPUs:  4,
				MCP: &MCPAgentOverride{
					Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileObserve},
				},
				EgressProfile: "locked",
			},
		},
		{
			name: "local egress profile tighten-only — cannot widen",
			global: AgentOverride{
				EgressProfile: "locked",
			},
			local: AgentOverride{
				EgressProfile: "permissive",
			},
			want: AgentOverride{
				EgressProfile: "locked",
			},
		},
		{
			name: "local egress profile can tighten",
			global: AgentOverride{
				EgressProfile: "standard",
			},
			local: AgentOverride{
				EgressProfile: "locked",
			},
			want: AgentOverride{
				EgressProfile: "locked",
			},
		},
		{
			name:   "local egress profile tightens implicit permissive",
			global: AgentOverride{},
			local: AgentOverride{
				EgressProfile: "standard",
			},
			want: AgentOverride{
				EgressProfile: "standard",
			},
		},
		{
			name: "allow hosts are additive",
			global: AgentOverride{
				AllowHosts: []EgressHostConfig{{Name: "a.example.com"}},
			},
			local: AgentOverride{
				AllowHosts: []EgressHostConfig{{Name: "b.example.com"}},
			},
			want: AgentOverride{
				AllowHosts: []EgressHostConfig{
					{Name: "a.example.com"},
					{Name: "b.example.com"},
				},
			},
		},
		{
			name: "MCP authz tighten-only via field-by-field merge",
			global: AgentOverride{
				MCP: &MCPAgentOverride{
					Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileSafeTools},
				},
			},
			local: AgentOverride{
				MCP: &MCPAgentOverride{
					Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileObserve},
				},
			},
			want: AgentOverride{
				MCP: &MCPAgentOverride{
					Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileObserve},
				},
			},
		},
		{
			name: "MCP enabled override preserves global authz",
			global: AgentOverride{
				MCP: &MCPAgentOverride{
					Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileObserve},
				},
			},
			local: AgentOverride{
				MCP: &MCPAgentOverride{
					Enabled: boolPtr(false),
				},
			},
			want: AgentOverride{
				MCP: &MCPAgentOverride{
					Enabled: boolPtr(false),
					Authz:   &MCPAuthzConfig{Profile: MCPAuthzProfileObserve},
				},
			},
		},
		{
			name: "all resource fields override when non-zero",
			global: AgentOverride{
				Image:      "old:v1",
				Command:    []string{"old-cmd"},
				EnvForward: []string{"OLD_*"},
				CPUs:       2,
				Memory:     bytesize.ByteSize(2048),
				TmpSize:    bytesize.ByteSize(512),
			},
			local: AgentOverride{
				Image:      "new:v2",
				Command:    []string{"new-cmd", "--flag"},
				EnvForward: []string{"NEW_*"},
				CPUs:       8,
				Memory:     bytesize.ByteSize(8192),
				TmpSize:    bytesize.ByteSize(1024),
			},
			want: AgentOverride{
				Image:      "new:v2",
				Command:    []string{"new-cmd", "--flag"},
				EnvForward: []string{"NEW_*"},
				CPUs:       8,
				Memory:     bytesize.ByteSize(8192),
				TmpSize:    bytesize.ByteSize(1024),
			},
		},
		{
			name:   "zero local is a no-op",
			global: AgentOverride{Image: "keep", CPUs: 4},
			local:  AgentOverride{},
			want:   AgentOverride{Image: "keep", CPUs: 4},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := mergeAgentOverride(tt.global, tt.local)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestMergeConfigs_AgentOverridePreservesSecurityFields(t *testing.T) {
	t.Parallel()

	// Exact scenario from review: local sets cpus, global has MCP authz + egress.
	global := &Config{
		Agents: map[string]AgentOverride{
			"claude-code": {
				MCP: &MCPAgentOverride{
					Authz: &MCPAuthzConfig{Profile: MCPAuthzProfileObserve},
				},
				EgressProfile: "locked",
			},
		},
	}
	local := &Config{
		Agents: map[string]AgentOverride{
			"claude-code": {
				CPUs: 4,
			},
		},
	}

	result := MergeConfigs(global, local)
	ao := result.Agents["claude-code"]

	assert.Equal(t, uint32(4), ao.CPUs, "CPUs should be overridden")
	require.NotNil(t, ao.MCP, "MCP should be preserved from global")
	require.NotNil(t, ao.MCP.Authz, "MCP.Authz should be preserved from global")
	assert.Equal(t, MCPAuthzProfileObserve, ao.MCP.Authz.Profile, "authz profile should survive")
	assert.Equal(t, "locked", ao.EgressProfile, "egress profile should survive")
}

func TestMergeConfigs_SettingsImport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		global *Config
		local  *Config
		want   SettingsImportConfig
	}{
		{
			name:   "both nil — defaults enabled",
			global: &Config{},
			local:  &Config{},
			want:   SettingsImportConfig{},
		},
		{
			name:   "global enabled, local disables — local wins",
			global: &Config{SettingsImport: SettingsImportConfig{Enabled: boolPtr(true)}},
			local:  &Config{SettingsImport: SettingsImportConfig{Enabled: boolPtr(false)}},
			want:   SettingsImportConfig{Enabled: boolPtr(false)},
		},
		{
			name:   "global nil, local enables — enable ignored (tighten only)",
			global: &Config{},
			local:  &Config{SettingsImport: SettingsImportConfig{Enabled: boolPtr(true)}},
			want:   SettingsImportConfig{},
		},
		{
			name:   "global disabled, local enables — global preserved",
			global: &Config{SettingsImport: SettingsImportConfig{Enabled: boolPtr(false)}},
			local:  &Config{SettingsImport: SettingsImportConfig{Enabled: boolPtr(true)}},
			want:   SettingsImportConfig{Enabled: boolPtr(false)},
		},
		{
			name:   "local disables skills category",
			global: &Config{},
			local:  &Config{SettingsImport: SettingsImportConfig{Categories: &SettingsCategoryConfig{Skills: boolPtr(false)}}},
			want:   SettingsImportConfig{Categories: &SettingsCategoryConfig{Skills: boolPtr(false)}},
		},
		{
			name: "local cannot re-enable disabled category",
			global: &Config{SettingsImport: SettingsImportConfig{
				Categories: &SettingsCategoryConfig{Skills: boolPtr(false)},
			}},
			local: &Config{SettingsImport: SettingsImportConfig{
				Categories: &SettingsCategoryConfig{Skills: boolPtr(true)},
			}},
			want: SettingsImportConfig{Categories: &SettingsCategoryConfig{Skills: boolPtr(false)}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := MergeConfigs(tt.global, tt.local)
			assert.Equal(t, tt.want, got.SettingsImport)
		})
	}
}

func TestSettingsImportConfig_IsEnabled(t *testing.T) {
	t.Parallel()

	// nil defaults to true
	var cfg SettingsImportConfig
	assert.True(t, cfg.IsEnabled())

	// explicit true
	cfg.Enabled = boolPtr(true)
	assert.True(t, cfg.IsEnabled())

	// explicit false
	cfg.Enabled = boolPtr(false)
	assert.False(t, cfg.IsEnabled())
}

func TestSettingsCategoryConfig_IsCategoryEnabled(t *testing.T) {
	t.Parallel()

	// nil receiver = all enabled
	var c *SettingsCategoryConfig
	assert.True(t, c.IsCategoryEnabled("settings"))
	assert.True(t, c.IsCategoryEnabled("skills"))
	assert.True(t, c.IsCategoryEnabled("unknown"))

	// nil field = enabled
	cfg := &SettingsCategoryConfig{}
	assert.True(t, cfg.IsCategoryEnabled("settings"))

	// explicit false
	cfg.Skills = boolPtr(false)
	assert.False(t, cfg.IsCategoryEnabled("skills"))
	assert.True(t, cfg.IsCategoryEnabled("settings"))
}

func TestMerge_ResourceBounds(t *testing.T) {
	t.Parallel()

	baseAgent := agent.Agent{
		Name:                 "test",
		Image:                "img:latest",
		Command:              []string{"cmd"},
		DefaultCPUs:          2,
		DefaultMemory:        bytesize.ByteSize(2048),
		DefaultEgressProfile: egress.ProfileStandard,
	}

	tests := []struct {
		name       string
		agent      agent.Agent
		override   AgentOverride
		defaults   DefaultsConfig
		wantCPUs   uint32
		wantMemory bytesize.ByteSize
	}{
		{
			name:       "normal values pass through",
			agent:      baseAgent,
			override:   AgentOverride{CPUs: 4, Memory: bytesize.ByteSize(4096)},
			defaults:   DefaultsConfig{},
			wantCPUs:   4,
			wantMemory: bytesize.ByteSize(4096),
		},
		{
			name:       "override CPUs exceed max — clamped",
			agent:      baseAgent,
			override:   AgentOverride{CPUs: 256},
			defaults:   DefaultsConfig{},
			wantCPUs:   MaxCPUs,
			wantMemory: bytesize.ByteSize(2048),
		},
		{
			name:       "override memory exceeds max — clamped",
			agent:      baseAgent,
			override:   AgentOverride{Memory: bytesize.ByteSize(999999)},
			defaults:   DefaultsConfig{},
			wantCPUs:   2,
			wantMemory: MaxMemory,
		},
		{
			name:       "global defaults exceed max — clamped",
			agent:      agent.Agent{Name: "a", Image: "i:l", Command: []string{"c"}, DefaultEgressProfile: "standard"},
			override:   AgentOverride{},
			defaults:   DefaultsConfig{CPUs: 500, Memory: bytesize.ByteSize(500000)},
			wantCPUs:   MaxCPUs,
			wantMemory: MaxMemory,
		},
		{
			name:       "agent defaults exceed max — clamped",
			agent:      agent.Agent{Name: "a", Image: "i:l", Command: []string{"c"}, DefaultCPUs: 200, DefaultMemory: bytesize.ByteSize(200000), DefaultEgressProfile: "standard"},
			override:   AgentOverride{},
			defaults:   DefaultsConfig{},
			wantCPUs:   MaxCPUs,
			wantMemory: MaxMemory,
		},
		{
			name:       "zero override uses agent defaults",
			agent:      baseAgent,
			override:   AgentOverride{},
			defaults:   DefaultsConfig{},
			wantCPUs:   2,
			wantMemory: bytesize.ByteSize(2048),
		},
		{
			name:       "at boundary — exactly max passes through",
			agent:      baseAgent,
			override:   AgentOverride{CPUs: MaxCPUs, Memory: MaxMemory},
			defaults:   DefaultsConfig{},
			wantCPUs:   MaxCPUs,
			wantMemory: MaxMemory,
		},
		{
			name:       "MaxUint32 values are clamped",
			agent:      baseAgent,
			override:   AgentOverride{CPUs: math.MaxUint32, Memory: bytesize.ByteSize(math.MaxUint32)},
			defaults:   DefaultsConfig{},
			wantCPUs:   MaxCPUs,
			wantMemory: MaxMemory,
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
