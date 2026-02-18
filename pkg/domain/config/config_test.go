// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/stacklok/apiary/pkg/domain/agent"
)

func TestReviewConfig_Defaults(t *testing.T) {
	t.Parallel()

	// Zero-value ReviewConfig means review is implicitly enabled (nil pointer).
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
			global: &Config{Defaults: DefaultsConfig{CPUs: 2, Memory: 1024}},
			local:  nil,
			want:   &Config{Defaults: DefaultsConfig{CPUs: 2, Memory: 1024}},
		},
		{
			name:   "local scalars override global when non-zero",
			global: &Config{Defaults: DefaultsConfig{CPUs: 2, Memory: 1024}},
			local:  &Config{Defaults: DefaultsConfig{CPUs: 4}},
			want:   &Config{Defaults: DefaultsConfig{CPUs: 4, Memory: 1024}},
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
		DefaultEgressProfile: "standard",
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
				DefaultEgressProfile: "standard",
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
				DefaultEgressProfile: "standard",
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
				DefaultEgressProfile: "standard",
			},
		},
		{
			name:  "override cpus and memory",
			agent: baseAgent,
			override: AgentOverride{
				CPUs:   4,
				Memory: 4096,
			},
			defaults: DefaultsConfig{},
			want: agent.Agent{
				Name:                 "test-agent",
				Image:                "ghcr.io/example/test:latest",
				Command:              []string{"test-cmd"},
				EnvForward:           []string{"API_KEY"},
				DefaultCPUs:          4,
				DefaultMemory:        4096,
				DefaultEgressProfile: "standard",
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
				Memory: 1024,
			},
			want: agent.Agent{
				Name:                 "minimal",
				Image:                "img:latest",
				Command:              []string{"cmd"},
				DefaultCPUs:          2,
				DefaultMemory:        1024,
				DefaultEgressProfile: "standard",
			},
		},
		{
			name:  "override takes precedence over global defaults",
			agent: agent.Agent{Name: "a", Image: "i:l", Command: []string{"c"}},
			override: AgentOverride{
				CPUs:   8,
				Memory: 8192,
			},
			defaults: DefaultsConfig{
				CPUs:   2,
				Memory: 1024,
			},
			want: agent.Agent{
				Name:                 "a",
				Image:                "i:l",
				Command:              []string{"c"},
				DefaultCPUs:          8,
				DefaultMemory:        8192,
				DefaultEgressProfile: "standard",
			},
		},
		{
			name:     "agent values take precedence over global defaults",
			agent:    baseAgent,
			override: AgentOverride{},
			defaults: DefaultsConfig{
				CPUs:   1,
				Memory: 512,
			},
			want: baseAgent,
		},
		{
			name: "egress profile — override takes precedence",
			agent: agent.Agent{
				Name:                 "a",
				Image:                "i:l",
				Command:              []string{"c"},
				DefaultEgressProfile: "standard",
			},
			override: AgentOverride{EgressProfile: "locked"},
			defaults: DefaultsConfig{},
			want: agent.Agent{
				Name:                 "a",
				Image:                "i:l",
				Command:              []string{"c"},
				DefaultEgressProfile: "locked",
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
				DefaultEgressProfile: "locked",
			},
		},
		{
			name: "egress profile — falls back to standard when all empty",
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
				DefaultEgressProfile: "standard",
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
		assert.Nil(t, ToEgressHosts(nil))
	})

	t.Run("converts config to domain hosts", func(t *testing.T) {
		t.Parallel()
		configs := []EgressHostConfig{
			{Name: "api.example.com", Ports: []uint16{443}, Protocol: 6},
			{Name: "*.docker.io"},
		}
		got := ToEgressHosts(configs)
		assert.Len(t, got, 2)
		assert.Equal(t, "api.example.com", got[0].Name)
		assert.Equal(t, []uint16{443}, got[0].Ports)
		assert.Equal(t, uint8(6), got[0].Protocol)
		assert.Equal(t, "*.docker.io", got[1].Name)
		assert.Nil(t, got[1].Ports)
	})
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
