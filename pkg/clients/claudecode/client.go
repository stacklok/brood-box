// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package claudecode declares the Claude Code client: agent metadata, MCP
// config emission, and host-side credential seeding.
package claudecode

import (
	"log/slog"

	"github.com/stacklok/brood-box/pkg/clients/internal/devhosts"
	"github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/bytesize"
	"github.com/stacklok/brood-box/pkg/domain/credential"
	"github.com/stacklok/brood-box/pkg/domain/egress"
	"github.com/stacklok/brood-box/pkg/domain/settings"
)

// Name is the canonical agent name for Claude Code.
const Name = "claude-code"

// New returns the ClientEntry for Claude Code. logger is used by the
// credential seeder; pass a discard logger when seeding is not desired.
func New(logger *slog.Logger) agent.ClientEntry {
	return agent.ClientEntry{
		Agent:  spec(),
		Plugin: &plugin{logger: logger},
	}
}

type plugin struct {
	logger *slog.Logger
}

func (p *plugin) MCPConfig() agent.MCPInjector { return &mcpInjector{} }
func (p *plugin) Seeder() credential.Seeder    { return newSeeder(p.logger) }

func spec() agent.Agent {
	locked := []egress.Host{
		{Name: "api.anthropic.com", Ports: []uint16{443}},
		{Name: "*.anthropic.com", Ports: []uint16{443}},
		{Name: "claude.com", Ports: []uint16{443}},
		{Name: "*.claude.com", Ports: []uint16{443}},
	}
	return agent.Agent{
		Name:                 Name,
		Image:                "ghcr.io/stacklok/brood-box/claude-code:latest",
		Command:              []string{"claude"},
		EnvForward:           []string{"ANTHROPIC_API_KEY", "CLAUDE_*", "NODE_OPTIONS"},
		NodeHeapPercent:      75,
		GoMemLimitPercent:    70,
		DefaultCPUs:          2,
		DefaultMemory:        bytesize.ByteSize(4096),
		DefaultTmpSize:       bytesize.ByteSize(2048),
		DefaultEgressProfile: egress.ProfilePermissive,
		CredentialPaths:      []string{".claude/"},
		EgressHosts: map[egress.ProfileName][]egress.Host{
			egress.ProfileLocked:   locked,
			egress.ProfileStandard: append(append([]egress.Host{}, locked...), devhosts.Standard()...),
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
	}
}
