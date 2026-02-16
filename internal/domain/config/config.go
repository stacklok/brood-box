// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package config defines configuration types for sandbox-agent.
// All types are pure data with no I/O dependencies.
package config

import (
	"github.com/stacklok/sandbox-agent/internal/domain/agent"
	"github.com/stacklok/sandbox-agent/internal/domain/egress"
)

// LocalConfigFile is the per-workspace config file name.
const LocalConfigFile = ".sandbox-agent.yaml"

// Config is the top-level user configuration.
type Config struct {
	// Defaults specifies default resource limits.
	Defaults DefaultsConfig `yaml:"defaults"`

	// Review configures workspace snapshot isolation.
	Review ReviewConfig `yaml:"review"`

	// Network configures egress networking.
	Network NetworkConfig `yaml:"network"`

	// MCP configures the in-process MCP proxy.
	MCP MCPConfig `yaml:"mcp"`

	// Agents maps agent names to configuration overrides.
	Agents map[string]AgentOverride `yaml:"agents"`
}

// MCPConfig configures the in-process MCP proxy.
type MCPConfig struct {
	// Enabled controls whether the MCP proxy is active. Default: false.
	Enabled bool `yaml:"enabled"`

	// Group is the ToolHive group to discover servers from. Default: "default".
	Group string `yaml:"group,omitempty"`

	// Port is the TCP port on the gateway IP. Default: 4483.
	Port uint16 `yaml:"port,omitempty"`

	// ConfigPath is an optional path to a vmcp config YAML for advanced
	// customization (tool filtering, conflict resolution, composite workflows).
	ConfigPath string `yaml:"config,omitempty"`
}

// ReviewConfig configures workspace snapshot isolation and review behavior.
type ReviewConfig struct {
	// Enabled controls whether snapshot isolation is active.
	// When nil, defaults to true (enabled).
	Enabled *bool `yaml:"enabled,omitempty"`

	// ExcludePatterns are additional gitignore-style patterns to exclude
	// from the workspace snapshot.
	ExcludePatterns []string `yaml:"exclude_patterns,omitempty"`
}

// DefaultsConfig specifies default VM resource limits.
type DefaultsConfig struct {
	// CPUs is the default number of vCPUs.
	CPUs uint32 `yaml:"cpus"`

	// Memory is the default RAM in MiB.
	Memory uint32 `yaml:"memory"`

	// EgressProfile is the default egress restriction level.
	EgressProfile string `yaml:"egress_profile,omitempty"`
}

// NetworkConfig configures egress networking.
type NetworkConfig struct {
	// AllowHosts are additional egress hosts to allow beyond the profile defaults.
	AllowHosts []EgressHostConfig `yaml:"allow_hosts,omitempty"`
}

// EgressHostConfig is the YAML representation of an allowed egress host.
type EgressHostConfig struct {
	Name     string   `yaml:"name"`
	Ports    []uint16 `yaml:"ports,omitempty"`
	Protocol uint8    `yaml:"protocol,omitempty"`
}

// AgentOverride allows users to override built-in agent settings.
type AgentOverride struct {
	// Image overrides the OCI image reference.
	Image string `yaml:"image,omitempty"`

	// Command overrides the entrypoint command.
	Command []string `yaml:"command,omitempty"`

	// EnvForward overrides the env forwarding patterns.
	EnvForward []string `yaml:"env_forward,omitempty"`

	// CPUs overrides the vCPU count.
	CPUs uint32 `yaml:"cpus,omitempty"`

	// Memory overrides the RAM in MiB.
	Memory uint32 `yaml:"memory,omitempty"`

	// EgressProfile overrides the agent's default egress profile.
	EgressProfile string `yaml:"egress_profile,omitempty"`

	// AllowHosts are additional egress hosts for this agent.
	AllowHosts []EgressHostConfig `yaml:"allow_hosts,omitempty"`

	// MCP overrides the global MCP proxy settings for this agent.
	MCP *MCPConfig `yaml:"mcp,omitempty"`
}

// MergeConfigs merges a local (per-workspace) config into a global config.
// Rules:
//   - Scalars (CPUs, Memory): local overrides global when non-zero.
//   - Review.Enabled: local value is IGNORED (security constraint).
//   - Review.ExcludePatterns: additive (global + local).
//   - Defaults.EgressProfile: local can only tighten (not widen).
//   - Network.AllowHosts: additive (global + local).
//   - Agents map: local extends/overrides global per key.
//
// Returns global unchanged when local is nil.
func MergeConfigs(global, local *Config) *Config {
	if local == nil {
		return global
	}

	result := *global

	// Scalars: local overrides global when non-zero.
	if local.Defaults.CPUs > 0 {
		result.Defaults.CPUs = local.Defaults.CPUs
	}
	if local.Defaults.Memory > 0 {
		result.Defaults.Memory = local.Defaults.Memory
	}

	// EgressProfile: local can only tighten (use Stricter). If local tries
	// to widen, keep global value.
	if local.Defaults.EgressProfile != "" {
		if result.Defaults.EgressProfile == "" {
			result.Defaults.EgressProfile = local.Defaults.EgressProfile
		} else {
			result.Defaults.EgressProfile = string(egress.Stricter(
				egress.ProfileName(result.Defaults.EgressProfile),
				egress.ProfileName(local.Defaults.EgressProfile),
			))
		}
	}

	// Review.Enabled: local value is IGNORED (global preserved).
	// Review.ExcludePatterns: additive.
	if len(global.Review.ExcludePatterns) > 0 || len(local.Review.ExcludePatterns) > 0 {
		result.Review.ExcludePatterns = append(
			append([]string{}, global.Review.ExcludePatterns...),
			local.Review.ExcludePatterns...,
		)
	}

	// Network.AllowHosts: additive.
	if len(global.Network.AllowHosts) > 0 || len(local.Network.AllowHosts) > 0 {
		result.Network.AllowHosts = append(
			append([]EgressHostConfig{}, global.Network.AllowHosts...),
			local.Network.AllowHosts...,
		)
	}

	// Agents: local extends/overrides global per key.
	if len(local.Agents) > 0 {
		if result.Agents == nil {
			result.Agents = make(map[string]AgentOverride)
		} else {
			// Copy the global map to avoid mutating the original.
			merged := make(map[string]AgentOverride, len(result.Agents)+len(local.Agents))
			for k, v := range result.Agents {
				merged[k] = v
			}
			result.Agents = merged
		}
		for k, v := range local.Agents {
			result.Agents[k] = v
		}
	}

	return &result
}

// Merge combines an agent definition with user overrides and defaults.
// Override fields take precedence when non-zero. Defaults are used as fallback
// when neither the agent nor the override specifies a value.
func Merge(a agent.Agent, override AgentOverride, defaults DefaultsConfig) agent.Agent {
	result := a

	if override.Image != "" {
		result.Image = override.Image
	}
	if len(override.Command) > 0 {
		result.Command = override.Command
	}
	if len(override.EnvForward) > 0 {
		result.EnvForward = override.EnvForward
	}

	// CPUs: override > agent default > global default
	if override.CPUs > 0 {
		result.DefaultCPUs = override.CPUs
	}
	if result.DefaultCPUs == 0 && defaults.CPUs > 0 {
		result.DefaultCPUs = defaults.CPUs
	}

	// Memory: override > agent default > global default
	if override.Memory > 0 {
		result.DefaultMemory = override.Memory
	}
	if result.DefaultMemory == 0 && defaults.Memory > 0 {
		result.DefaultMemory = defaults.Memory
	}

	// EgressProfile: override > agent default > global default > "standard"
	if override.EgressProfile != "" {
		result.DefaultEgressProfile = egress.ProfileName(override.EgressProfile)
	}
	if result.DefaultEgressProfile == "" && defaults.EgressProfile != "" {
		result.DefaultEgressProfile = egress.ProfileName(defaults.EgressProfile)
	}
	if result.DefaultEgressProfile == "" {
		result.DefaultEgressProfile = egress.ProfileStandard
	}

	return result
}

// ToEgressHosts converts config host entries to domain egress hosts.
func ToEgressHosts(configs []EgressHostConfig) []egress.Host {
	if len(configs) == 0 {
		return nil
	}
	hosts := make([]egress.Host, len(configs))
	for i, c := range configs {
		hosts[i] = egress.Host{
			Name:     c.Name,
			Ports:    c.Ports,
			Protocol: c.Protocol,
		}
	}
	return hosts
}
