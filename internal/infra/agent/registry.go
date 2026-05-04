// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package agent provides the built-in agent registry.
package agent

import (
	"fmt"
	"sort"

	domainagent "github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/bytesize"
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

	// Hermes is provider-agnostic and ships a default of Anthropic's Claude
	// Opus, but routinely dispatches to Nous Portal (flagship), OpenRouter,
	// and direct OpenAI / Gemini / HuggingFace router endpoints. Locked
	// profile covers the providers reachable out of the box (matched against
	// the EnvForward provider-key set below). Messaging-gateway hosts
	// (Telegram / Discord / Slack / WhatsApp / Matrix) are intentionally NOT
	// added here — enabling those extras is an explicit, per-workspace opt-in.
	hermesLockedHosts := []egress.Host{
		{Name: "api.anthropic.com", Ports: []uint16{443}},
		{Name: "*.anthropic.com", Ports: []uint16{443}},
		{Name: "api.openai.com", Ports: []uint16{443}},
		{Name: "*.openai.com", Ports: []uint16{443}},
		{Name: "openrouter.ai", Ports: []uint16{443}},
		{Name: "*.openrouter.ai", Ports: []uint16{443}},
		{Name: "generativelanguage.googleapis.com", Ports: []uint16{443}},
		{Name: "router.huggingface.co", Ports: []uint16{443}},
		{Name: "*.nousresearch.com", Ports: []uint16{443}},
	}

	// Gemini CLI defaults to OAuth (Code Assist endpoint) but also supports
	// direct Gemini Developer API and Vertex AI. Locked profile covers all
	// three plus the Google OAuth endpoints needed for first-run sign-in.
	geminiLockedHosts := []egress.Host{
		{Name: "generativelanguage.googleapis.com", Ports: []uint16{443}},
		{Name: "aiplatform.googleapis.com", Ports: []uint16{443}},
		{Name: "cloudaicompanion.googleapis.com", Ports: []uint16{443}},
		{Name: "oauth2.googleapis.com", Ports: []uint16{443}},
		{Name: "accounts.google.com", Ports: []uint16{443}},
	}

	return map[string]domainagent.Agent{
		"claude-code": {
			Name:                 "claude-code",
			Image:                "ghcr.io/stacklok/brood-box/claude-code:latest",
			Command:              []string{"claude"},
			EnvForward:           []string{"ANTHROPIC_API_KEY", "CLAUDE_*", "NODE_OPTIONS"},
			NodeHeapPercent:      75,
			GoMemLimitPercent:    70,
			DefaultCPUs:          2,
			DefaultMemory:        bytesize.ByteSize(4096),
			DefaultTmpSize:       bytesize.ByteSize(2048),
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
			GoMemLimitPercent:    70,
			DefaultCPUs:          2,
			DefaultMemory:        bytesize.ByteSize(4096),
			DefaultTmpSize:       bytesize.ByteSize(2048),
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
			GoMemLimitPercent:    70,
			DefaultCPUs:          2,
			DefaultMemory:        bytesize.ByteSize(4096),
			DefaultTmpSize:       bytesize.ByteSize(2048),
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
		"hermes": {
			Name:    "hermes",
			Image:   "ghcr.io/stacklok/brood-box/hermes:latest",
			Command: []string{"hermes"},
			// Hermes reads provider keys from ~/.hermes/.env or env vars.
			// Forward the common ones; HERMES_* covers Hermes-specific knobs
			// like HERMES_TUI and MESSAGING_CWD.
			EnvForward: []string{
				"ANTHROPIC_API_KEY",
				"OPENAI_API_KEY",
				"OPENROUTER_API_KEY",
				"GEMINI_API_KEY",
				"GOOGLE_API_KEY",
				"HF_TOKEN",
				"HERMES_*",
			},
			DefaultCPUs:          2,
			DefaultMemory:        bytesize.ByteSize(4096),
			DefaultTmpSize:       bytesize.ByteSize(2048),
			DefaultEgressProfile: egress.ProfilePermissive,
			MCPConfigFormat:      domainagent.MCPConfigFormatHermes,
			CredentialPaths:      []string{".hermes/"},
			EgressHosts: map[egress.ProfileName][]egress.Host{
				egress.ProfileLocked:   hermesLockedHosts,
				egress.ProfileStandard: append(hermesLockedHosts, devInfraHosts...),
			},
			// Hermes persists its whole ~/.hermes/ tree (config.yaml, .env,
			// sessions, skills) via CredentialPaths. We intentionally do NOT
			// inject the host config.yaml via the settings injector: the
			// injector has no YAML format yet, and the file can contain
			// host-side mcp_servers / gateway endpoints that must not leak
			// into the guest. SOUL.md (the HERMES_HOME identity file) and
			// skill directories are safe. Project-level AGENTS.md is picked
			// up from the workspace CWD by Hermes itself, so no injection
			// needed here.
			SettingsManifest: &settings.Manifest{Entries: []settings.Entry{
				{Category: "instructions", HostPath: ".hermes/SOUL.md", GuestPath: ".hermes/SOUL.md", Kind: settings.KindFile, Optional: true},
				{Category: "skills", HostPath: ".hermes/skills", GuestPath: ".hermes/skills", Kind: settings.KindDirectory, Optional: true},
				{Category: "skills", HostPath: ".agents/skills", GuestPath: ".agents/skills", Kind: settings.KindDirectory, Optional: true},
			}},
		},
		"gemini": {
			Name:    "gemini",
			Image:   "ghcr.io/stacklok/brood-box/gemini:latest",
			Command: []string{"gemini"},
			// GEMINI_API_KEY: Gemini Developer API key.
			// GOOGLE_API_KEY: Vertex AI express-mode key.
			// GOOGLE_CLOUD_PROJECT/LOCATION: Vertex AI / Code Assist.
			// GOOGLE_GENAI_USE_VERTEXAI: switch to Vertex.
			// GOOGLE_APPLICATION_CREDENTIALS is forwarded by name only —
			// the pointed-to JSON file is NOT auto-injected. Users who
			// need ADC inside the VM must set GEMINI_API_KEY instead or
			// inject the credential file themselves.
			// GEMINI_*: catch-all for documented Gemini knobs (e.g.
			// GEMINI_TELEMETRY_ENABLED, GEMINI_SYSTEM_MD).
			EnvForward: []string{
				"GEMINI_API_KEY",
				"GOOGLE_API_KEY",
				"GOOGLE_CLOUD_PROJECT",
				"GOOGLE_CLOUD_LOCATION",
				"GOOGLE_GENAI_USE_VERTEXAI",
				"GOOGLE_APPLICATION_CREDENTIALS",
				"GEMINI_*",
			},
			NodeHeapPercent:      75,
			GoMemLimitPercent:    70,
			DefaultCPUs:          2,
			DefaultMemory:        bytesize.ByteSize(4096),
			DefaultTmpSize:       bytesize.ByteSize(2048),
			DefaultEgressProfile: egress.ProfilePermissive,
			MCPConfigFormat:      domainagent.MCPConfigFormatGemini,
			CredentialPaths:      []string{".gemini/"},
			EgressHosts: map[egress.ProfileName][]egress.Host{
				egress.ProfileLocked:   geminiLockedHosts,
				egress.ProfileStandard: append(geminiLockedHosts, devInfraHosts...),
			},
			// NOTE: "mcpServers", "mcp", "tools", "hooks", "hooksConfig",
			// "security", "advanced", "telemetry", "policyPaths",
			// "adminPolicyPaths", "admin", and "ide" are intentionally
			// excluded from AllowKeys. mcpServers would point the guest at
			// host-only servers; tools.discoveryCommand/callCommand and
			// hooks reference host-side executables; security/advanced
			// control env-var redaction and other host-coupled toggles.
			// Only host-portable categories are allowed through.
			SettingsManifest: &settings.Manifest{Entries: []settings.Entry{
				{Category: "settings", HostPath: ".gemini/settings.json", GuestPath: ".gemini/settings.json", Kind: settings.KindMergeFile, Optional: true,
					Format: "json", Filter: &settings.FieldFilter{AllowKeys: []string{
						"general", "ui", "model", "modelConfigs", "context",
						"agents", "skills", "useWriteTodos", "experimental",
						"output", "privacy",
					}}},
				// GEMINI.md is the user-level memory/context file —
				// directly analogous to ~/.claude/CLAUDE.md.
				{Category: "instructions", HostPath: ".gemini/GEMINI.md", GuestPath: ".gemini/GEMINI.md", Kind: settings.KindFile, Optional: true},
				// Cross-tool fallback: users with a single CLAUDE.md on
				// the host get context in Gemini too. Gemini's
				// `context.fileName` accepts multiple filenames.
				{Category: "instructions", HostPath: ".claude/CLAUDE.md", GuestPath: ".gemini/CLAUDE.md", Kind: settings.KindFile, Optional: true},
				{Category: "agents", HostPath: ".gemini/agents", GuestPath: ".gemini/agents", Kind: settings.KindDirectory, Optional: true},
				{Category: "skills", HostPath: ".gemini/skills", GuestPath: ".gemini/skills", Kind: settings.KindDirectory, Optional: true},
				{Category: "skills", HostPath: ".agents/skills", GuestPath: ".agents/skills", Kind: settings.KindDirectory, Optional: true},
				{Category: "commands", HostPath: ".gemini/commands", GuestPath: ".gemini/commands", Kind: settings.KindDirectory, Optional: true},
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
