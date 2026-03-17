// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package config defines configuration types for Brood Box.
// All types are pure data with no I/O dependencies.
package config

import (
	"fmt"
	"strings"

	"github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/egress"
)

// LocalConfigFile is the per-workspace config file name.
const LocalConfigFile = ".broodbox.yaml"

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

	// Git configures git identity and auth forwarding.
	Git GitConfig `yaml:"git"`

	// Auth configures credential persistence.
	Auth AuthConfig `yaml:"auth"`

	// Runtime configures host runtime dependencies.
	Runtime RuntimeConfig `yaml:"runtime"`

	// Agents maps agent names to configuration overrides.
	Agents map[string]AgentOverride `yaml:"agents"`
}

// GitConfig configures git identity and authentication forwarding
// into the sandbox VM.
type GitConfig struct {
	// ForwardToken controls whether GITHUB_TOKEN/GH_TOKEN are forwarded.
	// nil = default true.
	ForwardToken *bool `yaml:"forward_token,omitempty"`

	// ForwardSSHAgent controls whether SSH agent forwarding is enabled.
	// nil = default true.
	ForwardSSHAgent *bool `yaml:"forward_ssh_agent,omitempty"`
}

// AuthConfig configures credential persistence between sessions.
type AuthConfig struct {
	// SaveCredentials controls whether agent credentials are saved.
	// nil = default true.
	SaveCredentials *bool `yaml:"save_credentials,omitempty"`

	// SeedHostCredentials controls whether host credentials (e.g. macOS
	// Keychain) are seeded into the VM before the agent starts.
	// nil = default false.
	SeedHostCredentials *bool `yaml:"seed_host_credentials,omitempty"`
}

// SaveCredentialsEnabled returns whether credential saving is enabled.
// Defaults to true when SaveCredentials is nil.
func (a AuthConfig) SaveCredentialsEnabled() bool {
	if a.SaveCredentials == nil {
		return true
	}
	return *a.SaveCredentials
}

// SeedHostCredentialsEnabled returns whether host credential seeding is enabled.
// Defaults to false when SeedHostCredentials is nil.
func (a AuthConfig) SeedHostCredentialsEnabled() bool {
	if a.SeedHostCredentials == nil {
		return false
	}
	return *a.SeedHostCredentials
}

// RuntimeConfig configures host runtime dependency handling.
type RuntimeConfig struct {
	// FirmwareDownload controls whether libkrunfw is downloaded at runtime.
	// nil = default true.
	FirmwareDownload *bool `yaml:"firmware_download,omitempty"`
}

// GitTokenEnabled returns whether git token forwarding is enabled.
// Defaults to true when ForwardToken is nil.
func (g GitConfig) GitTokenEnabled() bool {
	if g.ForwardToken == nil {
		return true
	}
	return *g.ForwardToken
}

// SSHAgentEnabled returns whether SSH agent forwarding is enabled.
// Defaults to true when ForwardSSHAgent is nil.
func (g GitConfig) SSHAgentEnabled() bool {
	if g.ForwardSSHAgent == nil {
		return true
	}
	return *g.ForwardSSHAgent
}

// MCPConfig configures the in-process MCP proxy.
type MCPConfig struct {
	// Enabled controls whether the MCP proxy is active.
	// When nil, defaults to true (enabled).
	Enabled *bool `yaml:"enabled,omitempty"`

	// Group is the ToolHive group to discover servers from. Default: "default".
	Group string `yaml:"group,omitempty"`

	// Port is the TCP port on the gateway IP. Default: 4483.
	Port uint16 `yaml:"port,omitempty"`

	// Config is an optional inline MCP config for Cedar authorization
	// policies and tool aggregation settings. Can also be loaded from
	// a file via --mcp-config.
	//
	// NOTE: MCP.Config is intentionally NOT merged from workspace-local
	// config (.broodbox.yaml). Same security constraint as the "custom"
	// profile — untrusted repos must not inject Cedar policies.
	Config *MCPFileConfig `yaml:"config,omitempty"`

	// Authz configures authorization for the MCP proxy.
	Authz *MCPAuthzConfig `yaml:"authz,omitempty"`
}

// MCPFileConfig is the user-facing MCP configuration format.
// It abstracts away vmcp internals (groupRef, auth type, etc.) and exposes
// only the two sections brood-box uses: authorization policies and
// tool aggregation settings.
type MCPFileConfig struct {
	// Authz configures Cedar authorization policies.
	Authz *MCPFileAuthzConfig `yaml:"authz,omitempty"`

	// Aggregation configures tool conflict resolution and filtering.
	Aggregation *MCPAggregationConfig `yaml:"aggregation,omitempty"`
}

// Validate checks the MCPFileConfig for structural correctness.
func (c *MCPFileConfig) Validate() error {
	if c == nil {
		return nil
	}
	if c.Authz != nil && len(c.Authz.Policies) == 0 {
		return fmt.Errorf("authz.policies must be non-empty when authz is specified")
	}
	if c.Aggregation != nil && c.Aggregation.ConflictResolution != "" {
		switch strings.ToLower(c.Aggregation.ConflictResolution) {
		case "prefix", "priority", "manual":
			// valid
		default:
			return fmt.Errorf("aggregation.conflict_resolution must be one of: prefix, priority, manual; got %q",
				c.Aggregation.ConflictResolution)
		}
	}
	return nil
}

// MCPFileAuthzConfig configures Cedar authorization policies.
type MCPFileAuthzConfig struct {
	// Policies is a list of Cedar policy strings.
	Policies []string `yaml:"policies"`
}

// MCPAggregationConfig configures tool conflict resolution and filtering.
type MCPAggregationConfig struct {
	// ConflictResolution is the strategy: "prefix", "priority", or "manual".
	ConflictResolution string `yaml:"conflict_resolution,omitempty"`

	// PrefixFormat defines the prefix format for the "prefix" strategy.
	PrefixFormat string `yaml:"prefix_format,omitempty"`

	// PriorityOrder defines workload priority for the "priority" strategy.
	PriorityOrder []string `yaml:"priority_order,omitempty"`

	// ExcludeAllTools hides all backend tools from MCP clients.
	ExcludeAllTools bool `yaml:"exclude_all_tools,omitempty"`

	// Tools defines per-workload tool filtering and overrides.
	Tools []MCPWorkloadToolConfig `yaml:"tools,omitempty"`
}

// MCPWorkloadToolConfig defines tool filtering and overrides for a workload.
type MCPWorkloadToolConfig struct {
	// Workload is the backend workload name.
	Workload string `yaml:"workload"`

	// Filter is an allow-list of tool names to advertise.
	Filter []string `yaml:"filter,omitempty"`

	// Overrides maps original tool names to override settings.
	Overrides map[string]*MCPToolOverride `yaml:"overrides,omitempty"`

	// ExcludeAll hides all tools from this workload.
	ExcludeAll bool `yaml:"exclude_all,omitempty"`
}

// MCPToolOverride defines name and description overrides for a tool.
type MCPToolOverride struct {
	// Name is the new tool name.
	Name string `yaml:"name,omitempty"`

	// Description is the new tool description.
	Description string `yaml:"description,omitempty"`
}

// MCPAuthzConfig configures authorization for the MCP proxy.
type MCPAuthzConfig struct {
	// Profile is the authorization profile: "full-access" (default), "observe",
	// "safe-tools", or "custom". The "custom" profile delegates to Cedar policies
	// defined in the MCP config YAML (--mcp-config) and cannot be set from
	// workspace-local config.
	Profile string `yaml:"profile,omitempty"`
}

const (
	// MCPAuthzProfileFullAccess allows all MCP operations (default, no authz middleware).
	MCPAuthzProfileFullAccess = "full-access"

	// MCPAuthzProfileObserve allows listing and reading tools/prompts/resources only.
	MCPAuthzProfileObserve = "observe"

	// MCPAuthzProfileSafeTools allows observe operations plus calling non-destructive
	// closed-world tools based on MCP tool annotations.
	MCPAuthzProfileSafeTools = "safe-tools"

	// MCPAuthzProfileCustom delegates authorization to Cedar policies defined in
	// the MCP config YAML (--mcp-config authz.policies). Inferred automatically
	// when --mcp-config has Cedar policies. Cannot be set from workspace-local
	// config for security.
	MCPAuthzProfileCustom = "custom"
)

// mcpAuthzProfileStrictness defines the ordering from least to most permissive.
// Lower index = stricter. "custom" is excluded — its permissiveness is unknown.
var mcpAuthzProfileStrictness = []string{
	MCPAuthzProfileObserve,
	MCPAuthzProfileSafeTools,
	MCPAuthzProfileFullAccess,
}

// ValidMCPAuthzProfiles returns the list of valid MCP authz profile names.
func ValidMCPAuthzProfiles() []string {
	return []string{
		MCPAuthzProfileFullAccess, MCPAuthzProfileObserve,
		MCPAuthzProfileSafeTools, MCPAuthzProfileCustom,
	}
}

// IsValidMCPAuthzProfile reports whether the given profile name is recognized.
func IsValidMCPAuthzProfile(profile string) bool {
	switch profile {
	case MCPAuthzProfileFullAccess, MCPAuthzProfileObserve,
		MCPAuthzProfileSafeTools, MCPAuthzProfileCustom:
		return true
	default:
		return false
	}
}

// StricterMCPAuthzProfile returns the stricter of the two profiles.
// An empty string is treated as full-access (the implicit default).
// "custom" cannot be compared — if either side is "custom", it is returned
// unchanged (the caller is responsible for validating custom policy sources).
func StricterMCPAuthzProfile(a, b string) string {
	if a == MCPAuthzProfileCustom || b == MCPAuthzProfileCustom {
		// Custom has unknown permissiveness; preserve whichever side is custom.
		if a == MCPAuthzProfileCustom {
			return a
		}
		return b
	}
	if a == "" {
		a = MCPAuthzProfileFullAccess
	}
	if b == "" {
		b = MCPAuthzProfileFullAccess
	}
	for _, p := range mcpAuthzProfileStrictness {
		if p == a || p == b {
			return p
		}
	}
	// Both unknown — return a unchanged.
	return a
}

// IsEnabled returns whether the MCP proxy is enabled.
// Defaults to true when Enabled is nil.
func (m MCPConfig) IsEnabled() bool {
	if m.Enabled == nil {
		return true
	}
	return *m.Enabled
}

// ReviewConfig configures workspace snapshot isolation and review behavior.
type ReviewConfig struct {
	// Enabled controls whether snapshot isolation is active.
	// When nil, defaults to false (disabled).
	Enabled *bool `yaml:"enabled,omitempty"`

	// ExcludePatterns are additional gitignore-style patterns to exclude
	// from the workspace snapshot.
	ExcludePatterns []string `yaml:"exclude_patterns,omitempty"`
}

const (
	// MaxCPUs is the upper bound for vCPU allocation.
	MaxCPUs uint32 = 128

	// MaxMemory is the upper bound for RAM allocation (128 GiB).
	MaxMemory ByteSize = 131072

	// MaxTmpSize is the upper bound for /tmp tmpfs size (64 GiB).
	MaxTmpSize ByteSize = 65536
)

// DefaultsConfig specifies default VM resource limits.
type DefaultsConfig struct {
	// CPUs is the default number of vCPUs.
	CPUs uint32 `yaml:"cpus"`

	// Memory is the default RAM for the VM. Accepts human-readable values
	// like "4g" or "512m". Bare integers are treated as MiB for backward
	// compatibility. Zero uses the agent default.
	Memory ByteSize `yaml:"memory"`

	// TmpSize is the default /tmp tmpfs size. Accepts human-readable values
	// like "512m" or "2g". Bare integers are treated as MiB for backward
	// compatibility. Zero uses the go-microvm default (256 MiB).
	TmpSize ByteSize `yaml:"tmp_size,omitempty"`

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

	// Memory overrides the RAM for this agent. Accepts human-readable
	// values like "4g" or "512m".
	Memory ByteSize `yaml:"memory,omitempty"`

	// TmpSize overrides the /tmp tmpfs size for this agent. Accepts
	// human-readable values like "512m" or "2g".
	TmpSize ByteSize `yaml:"tmp_size,omitempty"`

	// EgressProfile overrides the agent's default egress profile.
	EgressProfile string `yaml:"egress_profile,omitempty"`

	// AllowHosts are additional egress hosts for this agent.
	AllowHosts []EgressHostConfig `yaml:"allow_hosts,omitempty"`

	// MCP overrides the global MCP proxy settings for this agent.
	MCP *MCPConfig `yaml:"mcp,omitempty"`
}

// clampResources caps CPU and memory values to their maximums.
// Zero values are passed through (they mean "use default").
func clampResources(cpus uint32, memory ByteSize) (uint32, ByteSize) {
	if cpus > MaxCPUs {
		cpus = MaxCPUs
	}
	if memory > MaxMemory {
		memory = MaxMemory
	}
	return cpus, memory
}

// clampTmpSize caps the /tmp tmpfs size to MaxTmpSize.
func clampTmpSize(s ByteSize) ByteSize {
	if s > MaxTmpSize {
		return MaxTmpSize
	}
	return s
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
	if local.Defaults.TmpSize > 0 {
		// TmpSize: local can only reduce (not inflate) the global default.
		if result.Defaults.TmpSize == 0 || local.Defaults.TmpSize < result.Defaults.TmpSize {
			result.Defaults.TmpSize = local.Defaults.TmpSize
		}
	}

	// Clamp resource values to configured maximums.
	result.Defaults.CPUs, result.Defaults.Memory = clampResources(
		result.Defaults.CPUs, result.Defaults.Memory,
	)
	result.Defaults.TmpSize = clampTmpSize(result.Defaults.TmpSize)

	// EgressProfile: local can only tighten (use Stricter).
	// Treat empty global as "permissive" — the implicit default for all built-in agents.
	if local.Defaults.EgressProfile != "" {
		effectiveGlobal := egress.ProfileName(result.Defaults.EgressProfile)
		if effectiveGlobal == "" {
			effectiveGlobal = egress.ProfilePermissive
		}
		result.Defaults.EgressProfile = string(egress.Stricter(
			effectiveGlobal,
			egress.ProfileName(local.Defaults.EgressProfile),
		))
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

	// Git: local can only tighten (disable), not enable if globally disabled.
	result.Git = mergeGitConfig(global.Git, local.Git)

	// Auth: local values are IGNORED (security constraint).
	// Explicitly preserve only global values to prevent workspace config
	// from enabling credential persistence or host credential seeding.
	result.Auth = AuthConfig{
		SaveCredentials:     global.Auth.SaveCredentials,
		SeedHostCredentials: global.Auth.SeedHostCredentials,
	}

	// Runtime: local overrides global when explicitly set.
	result.Runtime = mergeRuntimeConfig(global.Runtime, local.Runtime)

	// MCP.Authz: local can only tighten (not widen). Same security pattern
	// as egress profiles — a workspace config cannot escalate permissions.
	result.MCP.Authz = mergeMCPAuthzConfig(global.MCP.Authz, local.MCP.Authz)

	// Agents: local extends/overrides global per key.
	// MCP.Config and MCP.Authz are stripped from local overrides — workspace
	// config must not inject Cedar policies, aggregation settings, or authz
	// profiles through per-agent overrides (same security pattern as top-level
	// MCP.Authz tighten-only and Auth ignore).
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
			if v.MCP != nil && (v.MCP.Config != nil || v.MCP.Authz != nil) {
				sanitized := *v.MCP
				sanitized.Config = nil
				sanitized.Authz = nil
				v.MCP = &sanitized
			}
			result.Agents[k] = v
		}
	}

	return &result
}

// Merge combines an agent definition with user overrides and defaults.
// Override fields take precedence when non-zero. Defaults are used as fallback
// when neither the agent nor the override specifies a value.
// EgressProfile overrides can only tighten (not widen) the agent's profile.
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
		result.DefaultMemory = override.Memory.MiB()
	}
	if result.DefaultMemory == 0 && defaults.Memory > 0 {
		result.DefaultMemory = defaults.Memory.MiB()
	}

	// TmpSize: override > agent default > global default
	if override.TmpSize > 0 {
		result.DefaultTmpSize = override.TmpSize.MiB()
	}
	if result.DefaultTmpSize == 0 && defaults.TmpSize > 0 {
		result.DefaultTmpSize = defaults.TmpSize.MiB()
	}

	// Clamp resource values to configured maximums.
	clampedCPUs, clampedMem := clampResources(
		result.DefaultCPUs, ByteSize(result.DefaultMemory),
	)
	result.DefaultCPUs = clampedCPUs
	result.DefaultMemory = clampedMem.MiB()
	if result.DefaultTmpSize > MaxTmpSize.MiB() {
		result.DefaultTmpSize = MaxTmpSize.MiB()
	}

	// EgressProfile: override can only tighten the agent's built-in profile.
	// Treat empty as "permissive" — the default for all agents.
	if override.EgressProfile != "" {
		overrideProfile := egress.ProfileName(override.EgressProfile)
		current := result.DefaultEgressProfile
		if current == "" {
			current = egress.ProfilePermissive
		}
		result.DefaultEgressProfile = egress.Stricter(current, overrideProfile)
	}
	if result.DefaultEgressProfile == "" && defaults.EgressProfile != "" {
		result.DefaultEgressProfile = egress.ProfileName(defaults.EgressProfile)
	}
	if result.DefaultEgressProfile == "" {
		result.DefaultEgressProfile = egress.ProfilePermissive
	}

	return result
}

// mergeGitConfig merges local into global. Local can only tighten
// (set to false) — it cannot re-enable something globally disabled.
func mergeGitConfig(global, local GitConfig) GitConfig {
	result := global

	// Local can disable token forwarding, but cannot re-enable it.
	if local.ForwardToken != nil && !*local.ForwardToken {
		result.ForwardToken = local.ForwardToken
	}

	// Local can disable SSH agent forwarding, but cannot re-enable it.
	if local.ForwardSSHAgent != nil && !*local.ForwardSSHAgent {
		result.ForwardSSHAgent = local.ForwardSSHAgent
	}

	return result
}

// mergeMCPAuthzConfig merges local into global using tighten-only semantics.
// Local can only make the profile stricter, never more permissive.
// "custom" from local config is silently ignored (security: local config must
// not be able to switch to operator-controlled Cedar policies).
// Returns nil when neither side sets an authz config.
func mergeMCPAuthzConfig(global, local *MCPAuthzConfig) *MCPAuthzConfig {
	if local == nil || local.Profile == "" {
		return global
	}
	// "custom" is not allowed from workspace-local config — it would let a
	// repository supply its own Cedar policies via an MCP config it controls.
	if local.Profile == MCPAuthzProfileCustom {
		return global
	}
	if global == nil || global.Profile == "" {
		return local
	}
	return &MCPAuthzConfig{
		Profile: StricterMCPAuthzProfile(global.Profile, local.Profile),
	}
}

func mergeRuntimeConfig(global, local RuntimeConfig) RuntimeConfig {
	if global.FirmwareDownload != nil && !*global.FirmwareDownload {
		return RuntimeConfig{FirmwareDownload: global.FirmwareDownload}
	}
	if local.FirmwareDownload != nil {
		return RuntimeConfig{FirmwareDownload: local.FirmwareDownload}
	}
	return global
}

// ToEgressHosts converts config host entries to domain egress hosts.
// Each hostname is validated and canonicalized (lowercased). Returns an
// error on the first invalid hostname, identifying it by position.
func ToEgressHosts(configs []EgressHostConfig) ([]egress.Host, error) {
	if len(configs) == 0 {
		return nil, nil
	}
	hosts := make([]egress.Host, len(configs))
	for i, c := range configs {
		hosts[i] = egress.Host{
			Name:     c.Name,
			Ports:    c.Ports,
			Protocol: c.Protocol,
		}
		if err := egress.ValidateHost(&hosts[i]); err != nil {
			return nil, fmt.Errorf("allow_hosts[%d] %q: %w", i, c.Name, err)
		}
	}
	return hosts, nil
}
