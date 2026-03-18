// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package agent provides the built-in agent registry.
package agent

import (
	"fmt"
	"sort"

	domainagent "github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/egress"
	"github.com/stacklok/brood-box/pkg/domain/settings"
)

// Common dev infrastructure hosts shared across agents at the standard profile.
//
// Remaining wildcards and why they are necessary:
//   - *.githubusercontent.com — GitHub CDN subdomains (raw., objects., avatars., etc.)
//   - *.pypi.org — warehouse, upload, and test subdomains used by pip
//   - *.docker.io — registry-1., auth., index. subdomains required for image pulls
var devInfraHosts = []egress.Host{
	{Name: "github.com", Ports: []uint16{443, 22}},
	{Name: "api.github.com", Ports: []uint16{443}},
	{Name: "*.githubusercontent.com", Ports: []uint16{443}},
	{Name: "registry.npmjs.org", Ports: []uint16{443}},
	{Name: "pypi.org", Ports: []uint16{443}},
	{Name: "*.pypi.org", Ports: []uint16{443}},
	{Name: "proxy.golang.org", Ports: []uint16{443}},
	{Name: "sum.golang.org", Ports: []uint16{443}},
	{Name: "*.docker.io", Ports: []uint16{443}},
	{Name: "ghcr.io", Ports: []uint16{443}},
	{Name: "sentry.io", Ports: []uint16{443}},
}

// builtinAgents returns the default set of built-in coding agents.
func builtinAgents() map[string]domainagent.Agent {
	claudeLockedHosts := []egress.Host{
		{Name: "api.anthropic.com", Ports: []uint16{443}},
		{Name: "*.anthropic.com", Ports: []uint16{443}},
		{Name: "claude.com", Ports: []uint16{443}},
		{Name: "*.claude.com", Ports: []uint16{443}},
	}

	codexLockedHosts := []egress.Host{
		{Name: "api.openai.com", Ports: []uint16{443}},
		{Name: "*.openai.com", Ports: []uint16{443}},
	}

	opencodeLockedHosts := []egress.Host{
		{Name: "api.anthropic.com", Ports: []uint16{443}},
		{Name: "*.anthropic.com", Ports: []uint16{443}},
		{Name: "claude.com", Ports: []uint16{443}},
		{Name: "*.claude.com", Ports: []uint16{443}},
		{Name: "api.openai.com", Ports: []uint16{443}},
		{Name: "*.openai.com", Ports: []uint16{443}},
		{Name: "openrouter.ai", Ports: []uint16{443}},
		{Name: "*.openrouter.ai", Ports: []uint16{443}},
	}

	return map[string]domainagent.Agent{
		"claude-code": {
			Name:                 "claude-code",
			Image:                "ghcr.io/stacklok/brood-box/claude-code:latest",
			Command:              []string{"claude"},
			EnvForward:           []string{"ANTHROPIC_API_KEY", "CLAUDE_*", "NODE_OPTIONS"},
			NodeHeapPercent:      75,
			DefaultCPUs:          2,
			DefaultMemory:        4096,
			DefaultTmpSize:       512,
			DefaultEgressProfile: egress.ProfilePermissive,
			MCPConfigFormat:      domainagent.MCPConfigFormatClaudeCode,
			CredentialPaths:      []string{".claude/"},
			EgressHosts: map[egress.ProfileName][]egress.Host{
				egress.ProfileLocked:   claudeLockedHosts,
				egress.ProfileStandard: append(claudeLockedHosts, devInfraHosts...),
			},
			SettingsManifest: &settings.Manifest{Entries: []settings.Entry{
				{Category: "settings", HostPath: ".claude/settings.json", GuestPath: ".claude/settings.json", Kind: settings.KindMergeFile, Optional: true,
					Format: "json", Filter: &settings.FieldFilter{AllowKeys: []string{
						"permissions", "model", "preferredNotifChannel", "hasCompletedOnboarding",
						"autoUpdaterStatus",
					}}},
				// NOTE: .claude.json mcpServers are NOT injected. The VM cannot reach
				// host MCP servers — only the toolhive-proxied sandbox-tools endpoint
				// (injected by the MCP config hook) should be present.
				{Category: "instructions", HostPath: ".claude/CLAUDE.md", GuestPath: ".claude/CLAUDE.md", Kind: settings.KindFile, Optional: true},
				{Category: "rules", HostPath: ".claude/rules", GuestPath: ".claude/rules", Kind: settings.KindDirectory, Optional: true},
				{Category: "agents", HostPath: ".claude/agents", GuestPath: ".claude/agents", Kind: settings.KindDirectory, Optional: true},
				{Category: "skills", HostPath: ".claude/skills", GuestPath: ".claude/skills", Kind: settings.KindDirectory, Optional: true},
				{Category: "commands", HostPath: ".claude/commands", GuestPath: ".claude/commands", Kind: settings.KindDirectory, Optional: true},
			}},
		},
		"codex": {
			Name:                 "codex",
			Image:                "ghcr.io/stacklok/brood-box/codex:latest",
			Command:              []string{"codex"},
			EnvForward:           []string{"OPENAI_API_KEY", "CODEX_*"},
			DefaultCPUs:          2,
			DefaultMemory:        4096,
			DefaultTmpSize:       512,
			DefaultEgressProfile: egress.ProfilePermissive,
			MCPConfigFormat:      domainagent.MCPConfigFormatCodex,
			CredentialPaths:      []string{".codex/"},
			EgressHosts: map[egress.ProfileName][]egress.Host{
				egress.ProfileLocked:   codexLockedHosts,
				egress.ProfileStandard: append(codexLockedHosts, devInfraHosts...),
			},
			// NOTE: mcp_servers is excluded from AllowKeys — the VM cannot reach host
			// MCP servers. Only the toolhive-proxied sandbox-tools (injected by the
			// MCP config hook) should be present in the guest config.
			SettingsManifest: &settings.Manifest{Entries: []settings.Entry{
				{Category: "settings", HostPath: ".codex/config.toml", GuestPath: ".codex/config.toml", Kind: settings.KindMergeFile, Optional: true,
					Format: "toml", Filter: &settings.FieldFilter{AllowKeys: []string{
						"model", "provider", "approval_mode", "features", "profiles", "tui",
						"disable_response_storage", "full_auto_error_mode",
					}}},
				{Category: "instructions", HostPath: ".codex/AGENTS.md", GuestPath: ".codex/AGENTS.md", Kind: settings.KindFile, Optional: true},
				{Category: "instructions", HostPath: ".codex/AGENTS.override.md", GuestPath: ".codex/AGENTS.override.md", Kind: settings.KindFile, Optional: true},
				{Category: "skills", HostPath: ".agents/skills", GuestPath: ".agents/skills", Kind: settings.KindDirectory, Optional: true},
				{Category: "commands", HostPath: ".codex/prompts", GuestPath: ".codex/prompts", Kind: settings.KindDirectory, Optional: true},
			}},
		},
		"opencode": {
			Name:                 "opencode",
			Image:                "ghcr.io/stacklok/brood-box/opencode:latest",
			Command:              []string{"opencode"},
			EnvForward:           []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY", "OPENCODE_*"},
			DefaultCPUs:          2,
			DefaultMemory:        4096,
			DefaultTmpSize:       512,
			DefaultEgressProfile: egress.ProfilePermissive,
			MCPConfigFormat:      domainagent.MCPConfigFormatOpenCode,
			CredentialPaths:      []string{".config/opencode/"},
			EgressHosts: map[egress.ProfileName][]egress.Host{
				egress.ProfileLocked:   opencodeLockedHosts,
				egress.ProfileStandard: append(opencodeLockedHosts, devInfraHosts...),
			},
			SettingsManifest: &settings.Manifest{Entries: []settings.Entry{
				{Category: "settings", HostPath: ".config/opencode/opencode.json", GuestPath: ".config/opencode/opencode.json", Kind: settings.KindMergeFile, Optional: true,
					Format: "jsonc", Filter: &settings.FieldFilter{
						// NOTE: "mcp" is excluded — the VM cannot reach host MCP servers.
						// Only the toolhive-proxied sandbox-tools should be present.
						AllowKeys: []string{"providers", "models", "agent", "tools", "plugin", "theme", "command", "instructions", "formatter", "shell", "permission"},
						DenySubKeys: map[string][]string{"providers": {
							"*.api_key", "*.apiKey", "*.secret", "*.token",
							"*.password", "*.credentials", "*.accessToken",
							"*.access_token", "*.client_secret",
						}},
					}},
				{Category: "settings", HostPath: ".config/opencode/tui.json", GuestPath: ".config/opencode/tui.json", Kind: settings.KindFile, Optional: true},
				{Category: "instructions", HostPath: ".config/opencode/AGENTS.md", GuestPath: ".config/opencode/AGENTS.md", Kind: settings.KindFile, Optional: true},
				{Category: "instructions", HostPath: ".claude/CLAUDE.md", GuestPath: ".claude/CLAUDE.md", Kind: settings.KindFile, Optional: true},
				{Category: "agents", HostPath: ".config/opencode/agents", GuestPath: ".config/opencode/agents", Kind: settings.KindDirectory, Optional: true},
				{Category: "skills", HostPath: ".config/opencode/skills", GuestPath: ".config/opencode/skills", Kind: settings.KindDirectory, Optional: true},
				{Category: "skills", HostPath: ".claude/skills", GuestPath: ".claude/skills", Kind: settings.KindDirectory, Optional: true},
				{Category: "skills", HostPath: ".agents/skills", GuestPath: ".agents/skills", Kind: settings.KindDirectory, Optional: true},
				{Category: "commands", HostPath: ".config/opencode/commands", GuestPath: ".config/opencode/commands", Kind: settings.KindDirectory, Optional: true},
				{Category: "tools", HostPath: ".config/opencode/tools", GuestPath: ".config/opencode/tools", Kind: settings.KindDirectory, Optional: true},
				{Category: "plugins", HostPath: ".config/opencode/plugins", GuestPath: ".config/opencode/plugins", Kind: settings.KindDirectory, Optional: true},
				{Category: "themes", HostPath: ".config/opencode/themes", GuestPath: ".config/opencode/themes", Kind: settings.KindDirectory, Optional: true},
			}},
		},
	}
}

// Registry implements agent.Registry with an in-memory map of agents.
type Registry struct {
	agents map[string]domainagent.Agent
}

// NewRegistry creates a new registry pre-loaded with built-in agents.
func NewRegistry() *Registry {
	return &Registry{
		agents: builtinAgents(),
	}
}

// Add registers or overrides an agent in the registry.
// It validates the agent name before adding.
func (r *Registry) Add(a domainagent.Agent) error {
	if err := domainagent.ValidateName(a.Name); err != nil {
		return fmt.Errorf("cannot register agent: %w", err)
	}
	r.agents[a.Name] = a
	return nil
}

// Get returns the agent with the given name, or ErrNotFound.
func (r *Registry) Get(name string) (domainagent.Agent, error) {
	a, ok := r.agents[name]
	if !ok {
		return domainagent.Agent{}, &domainagent.ErrNotFound{Name: name}
	}
	return a, nil
}

// List returns all registered agents sorted by name.
func (r *Registry) List() []domainagent.Agent {
	result := make([]domainagent.Agent, 0, len(r.agents))
	for _, a := range r.agents {
		result = append(result, a)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}
