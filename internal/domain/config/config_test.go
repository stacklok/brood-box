// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/stacklok/sandbox-agent/internal/domain/agent"
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

func TestMerge(t *testing.T) {
	t.Parallel()

	baseAgent := agent.Agent{
		Name:          "test-agent",
		Image:         "ghcr.io/example/test:latest",
		Command:       []string{"test-cmd"},
		EnvForward:    []string{"API_KEY"},
		DefaultCPUs:   2,
		DefaultMemory: 2048,
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
				Name:          "test-agent",
				Image:         "custom-image:v1",
				Command:       []string{"test-cmd"},
				EnvForward:    []string{"API_KEY"},
				DefaultCPUs:   2,
				DefaultMemory: 2048,
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
				Name:          "test-agent",
				Image:         "ghcr.io/example/test:latest",
				Command:       []string{"new-cmd", "--flag"},
				EnvForward:    []string{"API_KEY"},
				DefaultCPUs:   2,
				DefaultMemory: 2048,
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
				Name:          "test-agent",
				Image:         "ghcr.io/example/test:latest",
				Command:       []string{"test-cmd"},
				EnvForward:    []string{"NEW_KEY", "OTHER_*"},
				DefaultCPUs:   2,
				DefaultMemory: 2048,
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
				Name:          "test-agent",
				Image:         "ghcr.io/example/test:latest",
				Command:       []string{"test-cmd"},
				EnvForward:    []string{"API_KEY"},
				DefaultCPUs:   4,
				DefaultMemory: 4096,
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
				Name:          "minimal",
				Image:         "img:latest",
				Command:       []string{"cmd"},
				DefaultCPUs:   2,
				DefaultMemory: 1024,
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
				Name:          "a",
				Image:         "i:l",
				Command:       []string{"c"},
				DefaultCPUs:   8,
				DefaultMemory: 8192,
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Merge(tt.agent, tt.override, tt.defaults)
			assert.Equal(t, tt.want, got)
		})
	}
}
