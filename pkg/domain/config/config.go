// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package config defines configuration types for Brood Box.
// All types are pure data with no I/O dependencies.
package config

import (
	"fmt"
	"strings"

	"github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/bytesize"
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

	// Image configures OCI image pulling behavior.
	Image ImageConfig `yaml:"image"`

	// MCP configures the in-process MCP proxy.
	MCP MCPConfig `yaml:"mcp"`

	// Git configures git identity and auth forwarding.
	Git GitConfig `yaml:"git"`

	// SettingsImport configures agent settings injection into the VM.
	SettingsImport SettingsImportConfig `yaml:"settings_import"`

	// Auth configures credential persistence.
	Auth AuthConfig `yaml:"auth"`

	// Runtime configures host runtime dependencies.
	Runtime RuntimeConfig `yaml:"runtime"`

	// Agents maps agent names to configuration overrides.
	Agents map[string]AgentOverride `yaml:"agents"`
}

// Validate checks user-supplied fields for footguns that must be
// rejected at load time. Today this scope is narrow:
//
//   - Per-agent env_forward patterns cannot be a bare "*" / empty /
//     leading-star — see agent.ValidateEnvForwardPatterns for the full
//     rationale. Applies to both the global and the workspace-local
//     config (each gets validated at its own load site).
//
// Other validation lives with the types it guards (e.g. MCPFileConfig
// has its own Validate). Add more here as new classes of footgun
// emerge.
func (c *Config) Validate() error {
	for name, override := range c.Agents {
		if err := agent.ValidateEnvForwardPatterns(override.EnvForward); err != nil {
			return fmt.Errorf("agents.%s.%w", name, err)
		}
	}
	return nil
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

// SettingsImportConfig configures agent settings injection into the VM.
type SettingsImportConfig struct {
	// Enabled controls whether settings injection is active. nil = default true.
	Enabled *bool `yaml:"enabled,omitempty"`

	// Categories controls which categories to import. nil = all enabled.
	Categories *SettingsCategoryConfig `yaml:"categories,omitempty"`
}

// IsEnabled returns whether settings import is enabled.
// Defaults to true when Enabled is nil.
func (s SettingsImportConfig) IsEnabled() bool {
	if s.Enabled == nil {
		return true
	}
	return *s.Enabled
}

// SettingsCategoryConfig controls which settings categories are imported.
type SettingsCategoryConfig struct {
	Settings     *bool `yaml:"settings,omitempty"`
	Instructions *bool `yaml:"instructions,omitempty"`
	Rules        *bool `yaml:"rules,omitempty"`
	Agents       *bool `yaml:"agents,omitempty"`
	Skills       *bool `yaml:"skills,omitempty"`
	Commands     *bool `yaml:"commands,omitempty"`
	Tools        *bool `yaml:"tools,omitempty"`
	Plugins      *bool `yaml:"plugins,omitempty"`
	Themes       *bool `yaml:"themes,omitempty"`
}

// IsCategoryEnabled returns whether the named category is enabled.
// nil receiver = all enabled. nil field = enabled.
func (c *SettingsCategoryConfig) IsCategoryEnabled(category string) bool {
	if c == nil {
		return true
	}
	var field *bool
	switch category {
	case "settings":
		field = c.Settings
	case "instructions":
		field = c.Instructions
	case "rules":
		field = c.Rules
	case "agents":
		field = c.Agents
	case "skills":
		field = c.Skills
	case "commands":
		field = c.Commands
	case "tools":
		field = c.Tools
	case "plugins":
		field = c.Plugins
	case "themes":
		field = c.Themes
	default:
		return true
	}
	if field == nil {
		return true
	}
	return *field
}

// RuntimeConfig configures host runtime dependency handling.
type RuntimeConfig struct {
	// FirmwareDownload controls whether libkrunfw is downloaded at runtime.
	// nil = default true.
	FirmwareDownload *bool `yaml:"firmware_download,omitempty"`
}

// ImageConfig configures OCI image pulling behavior.
type ImageConfig struct {
	// Pull controls the image pull policy.
	// Valid values: "always", "if-not-present", "never".
	// Default: "if-not-present".
	Pull string `yaml:"pull,omitempty"`
}

const (
	// PullAlways always checks the registry for a new digest before
	// starting the VM. Still uses the digest-based cache — if the
	// registry returns the same digest, the cached extraction is reused.
	PullAlways = "always"

	// PullBackground uses the cached image for the current run (instant
	// start) and checks the registry in the background. If a newer image
	// is found, it is cached for the next run. This is the default.
	PullBackground = "background"

	// PullIfNotPresent uses the cache if available, otherwise pulls from
	// the registry.
	PullIfNotPresent = "if-not-present"

	// PullNever uses the cache only. Returns an error if the image is not
	// cached. Useful for airgapped/offline environments and CI.
	PullNever = "never"
)

// IsValidPullPolicy reports whether the given pull policy name is recognized.
func IsValidPullPolicy(policy string) bool {
	switch policy {
	case PullAlways, PullBackground, PullIfNotPresent, PullNever:
		return true
	default:
		return false
	}
}

// ValidPullPolicies returns the list of valid pull policy names.
func ValidPullPolicies() []string {
	return []string{PullAlways, PullBackground, PullIfNotPresent, PullNever}
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

// MCPAgentOverride holds the subset of MCP settings that can be
// overridden per-agent. Only Enabled (gate) and Authz (tighten-only)
// have runtime effect at the per-agent level.
type MCPAgentOverride struct {
	// Enabled controls whether the MCP proxy is active for this agent.
	// nil means "no override" (inherit global setting), unlike
	// MCPConfig.Enabled where nil defaults to true.
	Enabled *bool `yaml:"enabled,omitempty"`

	// Authz overrides the MCP authorization profile for this agent.
	// Tighten-only: can only make the profile stricter, not more permissive.
	Authz *MCPAuthzConfig `yaml:"authz,omitempty"`
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
	MaxMemory bytesize.ByteSize = 131072

	// MaxTmpSize is the upper bound for /tmp tmpfs size (64 GiB).
	MaxTmpSize bytesize.ByteSize = 65536
)

// DefaultsConfig specifies default VM resource limits.
type DefaultsConfig struct {
	// CPUs is the default number of vCPUs.
	CPUs uint32 `yaml:"cpus"`

	// Memory is the default RAM for the VM. Accepts human-readable values
	// like "4g" or "512m". Bare integers are treated as MiB for backward
	// compatibility. Zero uses the agent default.
	Memory bytesize.ByteSize `yaml:"memory"`

	// TmpSize is the default /tmp tmpfs size. Accepts human-readable values
	// like "512m" or "2g". Bare integers are treated as MiB for backward
	// compatibility. Zero uses the go-microvm default (256 MiB).
	TmpSize bytesize.ByteSize `yaml:"tmp_size,omitempty"`

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
	Memory bytesize.ByteSize `yaml:"memory,omitempty"`

	// TmpSize overrides the /tmp tmpfs size for this agent. Accepts
	// human-readable values like "512m" or "2g".
	TmpSize bytesize.ByteSize `yaml:"tmp_size,omitempty"`

	// EgressProfile overrides the agent's default egress profile.
	EgressProfile string `yaml:"egress_profile,omitempty"`

	// AllowHosts are additional egress hosts for this agent.
	AllowHosts []EgressHostConfig `yaml:"allow_hosts,omitempty"`

	// MCP overrides the global MCP proxy settings for this agent.
	// Only Enabled (gate) and Authz (tighten-only) are supported.
	MCP *MCPAgentOverride `yaml:"mcp,omitempty"`

	// SettingsImport overrides the global settings import for this agent.
	SettingsImport *SettingsImportConfig `yaml:"settings_import,omitempty"`
}

// clampResources caps CPU and memory values to their maximums.
// Zero values are passed through (they mean "use default").
func clampResources(cpus uint32, memory bytesize.ByteSize) (uint32, bytesize.ByteSize) {
	if cpus > MaxCPUs {
		cpus = MaxCPUs
	}
	if memory > MaxMemory {
		memory = MaxMemory
	}
	return cpus, memory
}

// clampTmpSize caps the /tmp tmpfs size to MaxTmpSize.
func clampTmpSize(s bytesize.ByteSize) bytesize.ByteSize {
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

	// SettingsImport: local can only disable (tighten), not enable.
	result.SettingsImport = mergeSettingsImportConfig(global.SettingsImport, local.SettingsImport)

	// Runtime: local overrides global when explicitly set.
	result.Runtime = mergeRuntimeConfig(global.Runtime, local.Runtime)

	// Image: local overrides global when non-empty.
	if local.Image.Pull != "" {
		result.Image.Pull = local.Image.Pull
	}

	// MCP.Authz: local can only tighten (not widen). Same security pattern
	// as egress profiles — a workspace config cannot escalate permissions.
	result.MCP.Authz = MergeMCPAuthzConfig(global.MCP.Authz, local.MCP.Authz)

	// Agents: local extends/overrides global per key.
	// Security fields (EgressProfile, MCP.Authz) use tighten-only merge.
	// Resource/identity fields use local-overrides-when-non-zero.
	// AllowHosts are additive.
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
			if ga, ok := result.Agents[k]; ok {
				v = mergeAgentOverride(ga, v)
			} else {
				// New agent from local — still sanitize security fields.
				v = sanitizeNewAgentOverride(v)
			}
			result.Agents[k] = v
		}
	}

	return &result
}

// Merge combines an agent definition with user overrides and defaults.
// Override fields take precedence when non-zero. Global defaults override
// agent built-in defaults for resource fields (CPUs, Memory, TmpSize).
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

	// CPUs: override > global default > agent default
	if override.CPUs > 0 {
		result.DefaultCPUs = override.CPUs
	} else if defaults.CPUs > 0 {
		result.DefaultCPUs = defaults.CPUs
	}

	// Memory: override > global default > agent default
	if override.Memory > 0 {
		result.DefaultMemory = override.Memory
	} else if defaults.Memory > 0 {
		result.DefaultMemory = defaults.Memory
	}

	// TmpSize: override > global default > agent default
	if override.TmpSize > 0 {
		result.DefaultTmpSize = override.TmpSize
	} else if defaults.TmpSize > 0 {
		result.DefaultTmpSize = defaults.TmpSize
	}

	// Clamp resource values to configured maximums.
	clampedCPUs, clampedMem := clampResources(result.DefaultCPUs, result.DefaultMemory)
	result.DefaultCPUs = clampedCPUs
	result.DefaultMemory = clampedMem
	result.DefaultTmpSize = clampTmpSize(result.DefaultTmpSize)

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

// MergeMCPAuthzConfig merges local into global using tighten-only semantics.
// Local can only make the profile stricter, never more permissive.
// "custom" from local config is silently ignored (security: local config must
// not be able to switch to operator-controlled Cedar policies).
// Returns nil when neither side sets an authz config.
func MergeMCPAuthzConfig(global, local *MCPAuthzConfig) *MCPAuthzConfig {
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

// mergeSettingsImportConfig merges local into global with tighten-only semantics.
// Local can only disable settings import or individual categories, never enable them.
func mergeSettingsImportConfig(global, local SettingsImportConfig) SettingsImportConfig {
	result := global

	// Local can only disable, not enable.
	if local.Enabled != nil && !*local.Enabled {
		result.Enabled = local.Enabled
	}

	// Merge categories: local can only disable individual categories.
	if local.Categories != nil {
		result.Categories = TightenSettingsCategories(result.Categories, local.Categories)
	}

	return result
}

// TightenSettingsCategories merges local categories into global with tighten-only
// semantics. Local can only set categories to false (disable), never to true (enable).
// Exported so the application layer can apply per-agent overrides with the same
// security policy without reimplementing the tighten logic.
func TightenSettingsCategories(global, local *SettingsCategoryConfig) *SettingsCategoryConfig {
	if local == nil {
		return global
	}

	// Start from global (or empty if nil).
	var result SettingsCategoryConfig
	if global != nil {
		result = *global
	}

	tightenBool := func(dst **bool, src *bool) {
		if src != nil && !*src {
			*dst = src
		}
	}

	tightenBool(&result.Settings, local.Settings)
	tightenBool(&result.Instructions, local.Instructions)
	tightenBool(&result.Rules, local.Rules)
	tightenBool(&result.Agents, local.Agents)
	tightenBool(&result.Skills, local.Skills)
	tightenBool(&result.Commands, local.Commands)
	tightenBool(&result.Tools, local.Tools)
	tightenBool(&result.Plugins, local.Plugins)
	tightenBool(&result.Themes, local.Themes)

	return &result
}

// mergeAgentOverride merges a local (workspace) agent override into a global one.
// Rules mirror the top-level MergeConfigs patterns:
//   - Resource/identity fields (Image, Command, EnvForward, CPUs, Memory, TmpSize):
//     local overrides global when non-zero.
//   - EgressProfile: tighten-only via egress.Stricter.
//   - AllowHosts: additive (global + local).
//   - MCP.Enabled: local overrides global.
//   - MCP.Authz: tighten-only via MergeMCPAuthzConfig.
//   - SettingsImport: tighten-only via mergeSettingsImportConfig.
func mergeAgentOverride(global, local AgentOverride) AgentOverride {
	result := global

	// Resource/identity: local overrides when non-zero.
	if local.Image != "" {
		result.Image = local.Image
	}
	if len(local.Command) > 0 {
		result.Command = local.Command
	}
	if len(local.EnvForward) > 0 {
		result.EnvForward = local.EnvForward
	}
	if local.CPUs > 0 {
		result.CPUs = local.CPUs
	}
	if local.Memory > 0 {
		result.Memory = local.Memory
	}
	if local.TmpSize > 0 {
		result.TmpSize = local.TmpSize
	}

	// EgressProfile: tighten-only, matching top-level Defaults.EgressProfile merge.
	if local.EgressProfile != "" {
		effectiveGlobal := egress.ProfileName(result.EgressProfile)
		if effectiveGlobal == "" {
			effectiveGlobal = egress.ProfilePermissive
		}
		result.EgressProfile = string(egress.Stricter(
			effectiveGlobal,
			egress.ProfileName(local.EgressProfile),
		))
	}

	// AllowHosts: additive, matching Network.AllowHosts merge.
	if len(local.AllowHosts) > 0 {
		result.AllowHosts = append(
			append([]EgressHostConfig{}, result.AllowHosts...),
			local.AllowHosts...,
		)
	}

	// MCP: merge Enabled and Authz separately.
	if local.MCP != nil {
		if result.MCP == nil {
			result.MCP = &MCPAgentOverride{}
		} else {
			// Copy to avoid mutating the global.
			cp := *result.MCP
			result.MCP = &cp
		}
		if local.MCP.Enabled != nil {
			result.MCP.Enabled = local.MCP.Enabled
		}
		result.MCP.Authz = MergeMCPAuthzConfig(result.MCP.Authz, local.MCP.Authz)
	}

	// SettingsImport: tighten-only, matching top-level merge.
	if local.SettingsImport != nil {
		if result.SettingsImport == nil {
			// Local can only disable — when global has no settings import
			// config, only honour an explicit disable from local.
			if local.SettingsImport.Enabled != nil && !*local.SettingsImport.Enabled {
				result.SettingsImport = local.SettingsImport
			}
		} else {
			merged := mergeSettingsImportConfig(*result.SettingsImport, *local.SettingsImport)
			result.SettingsImport = &merged
		}
	}

	return result
}

// sanitizeNewAgentOverride strips security-sensitive fields that a workspace
// config must not set on a brand-new agent (one with no global counterpart).
// "custom" MCP authz profile is blocked (same as MergeMCPAuthzConfig).
func sanitizeNewAgentOverride(ao AgentOverride) AgentOverride {
	if ao.MCP != nil && ao.MCP.Authz != nil && ao.MCP.Authz.Profile == MCPAuthzProfileCustom {
		sanitized := *ao.MCP
		sanitized.Authz = nil
		ao.MCP = &sanitized
	}
	return ao
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
