// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package hermes declares the Hermes agent client.
package hermes

import (
	"github.com/stacklok/brood-box/pkg/clients/internal/devhosts"
	"github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/bytesize"
	"github.com/stacklok/brood-box/pkg/domain/credential"
	"github.com/stacklok/brood-box/pkg/domain/egress"
	"github.com/stacklok/brood-box/pkg/domain/settings"
)

// Name is the canonical agent name for Hermes.
const Name = "hermes"

// New returns the ClientEntry for Hermes.
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
	// Hermes is provider-agnostic and ships a default of Anthropic's Claude
	// Opus, but routinely dispatches to Nous Portal (flagship), OpenRouter,
	// and direct OpenAI / Gemini / HuggingFace router endpoints. Locked
	// profile covers the providers reachable out of the box (matched against
	// the EnvForward provider-key set below). Messaging-gateway hosts
	// (Telegram / Discord / Slack / WhatsApp / Matrix) are intentionally NOT
	// added here — enabling those extras is an explicit, per-workspace opt-in.
	locked := []egress.Host{
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
	return agent.Agent{
		Name:    Name,
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
		CredentialPaths:      []string{".hermes/"},
		EgressHosts: map[egress.ProfileName][]egress.Host{
			egress.ProfileLocked:   locked,
			egress.ProfileStandard: append(append([]egress.Host{}, locked...), devhosts.Standard()...),
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
	}
}
