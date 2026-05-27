// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package codex declares the Codex CLI client.
package codex

import (
	"github.com/stacklok/brood-box/pkg/clients/internal/devhosts"
	"github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/bytesize"
	"github.com/stacklok/brood-box/pkg/domain/credential"
	"github.com/stacklok/brood-box/pkg/domain/egress"
	"github.com/stacklok/brood-box/pkg/domain/settings"
)

// Name is the canonical agent name for Codex.
const Name = "codex"

// New returns the ClientEntry for Codex.
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
		{Name: "api.openai.com", Ports: []uint16{443}},
		{Name: "*.openai.com", Ports: []uint16{443}},
	}
	return agent.Agent{
		Name:                 Name,
		Image:                "ghcr.io/stacklok/brood-box/codex:latest",
		Command:              []string{"codex"},
		EnvForward:           []string{"OPENAI_API_KEY", "CODEX_*"},
		GoMemLimitPercent:    70,
		DefaultCPUs:          2,
		DefaultMemory:        bytesize.ByteSize(4096),
		DefaultTmpSize:       bytesize.ByteSize(2048),
		DefaultEgressProfile: egress.ProfilePermissive,
		CredentialPaths:      []string{".codex/"},
		EgressHosts: map[egress.ProfileName][]egress.Host{
			egress.ProfileLocked:   locked,
			egress.ProfileStandard: append(append([]egress.Host{}, locked...), devhosts.Standard()...),
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
	}
}
