// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package opencode declares the OpenCode CLI client.
package opencode

import (
	"github.com/stacklok/brood-box/pkg/clients/internal/devhosts"
	"github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/bytesize"
	"github.com/stacklok/brood-box/pkg/domain/credential"
	"github.com/stacklok/brood-box/pkg/domain/egress"
	"github.com/stacklok/brood-box/pkg/domain/settings"
)

// Name is the canonical agent name for OpenCode.
const Name = "opencode"

// New returns the ClientEntry for OpenCode.
func New() agent.ClientEntry {
	return agent.ClientEntry{
		Agent:  spec(),
		Plugin: plugin{},
	}
}

type plugin struct{}

func (plugin) MCPConfig() agent.MCPInjector { return mcpInjector{} }
func (plugin) Seeder() credential.Seeder    { return nil }

func spec() agent.Agent {
	locked := []egress.Host{
		{Name: "api.anthropic.com", Ports: []uint16{443}},
		{Name: "*.anthropic.com", Ports: []uint16{443}},
		{Name: "claude.com", Ports: []uint16{443}},
		{Name: "*.claude.com", Ports: []uint16{443}},
		{Name: "api.openai.com", Ports: []uint16{443}},
		{Name: "*.openai.com", Ports: []uint16{443}},
		{Name: "openrouter.ai", Ports: []uint16{443}},
		{Name: "*.openrouter.ai", Ports: []uint16{443}},
	}
	return agent.Agent{
		Name:                 Name,
		Image:                "ghcr.io/stacklok/brood-box/opencode:latest",
		Command:              []string{"opencode"},
		EnvForward:           []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY", "OPENCODE_*"},
		GoMemLimitPercent:    70,
		DefaultCPUs:          2,
		DefaultMemory:        bytesize.ByteSize(4096),
		DefaultTmpSize:       bytesize.ByteSize(2048),
		DefaultEgressProfile: egress.ProfilePermissive,
		CredentialPaths:      []string{".config/opencode/"},
		EgressHosts: map[egress.ProfileName][]egress.Host{
			egress.ProfileLocked:   locked,
			egress.ProfileStandard: append(append([]egress.Host{}, locked...), devhosts.Standard()...),
		},
		SettingsManifest: &settings.Manifest{Entries: []settings.Entry{
			{Category: "settings", HostPath: ".config/opencode/opencode.json", GuestPath: ".config/opencode/opencode.json", Kind: settings.KindMergeFile, Optional: true,
				Format: "jsonc", Filter: &settings.FieldFilter{
					// NOTE: "mcp" is excluded — the VM cannot reach host MCP servers.
					// Only the toolhive-proxied sandbox-tools should be present.
					AllowKeys: []string{"provider", "models", "agent", "tools", "plugin", "theme", "command", "instructions", "formatter", "shell", "permission"},
					DenySubKeys: map[string][]string{"provider": {
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
	}
}
