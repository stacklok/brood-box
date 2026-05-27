// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package gemini declares the Gemini CLI client.
package gemini

import (
	"github.com/stacklok/brood-box/pkg/clients/internal/devhosts"
	"github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/bytesize"
	"github.com/stacklok/brood-box/pkg/domain/credential"
	"github.com/stacklok/brood-box/pkg/domain/egress"
	"github.com/stacklok/brood-box/pkg/domain/settings"
)

// Name is the canonical agent name for the Gemini CLI.
const Name = "gemini"

// New returns the ClientEntry for the Gemini CLI.
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
	// Gemini CLI defaults to OAuth (Code Assist endpoint) but also supports
	// direct Gemini Developer API and Vertex AI. Locked profile covers all
	// three plus the Google OAuth endpoints needed for first-run sign-in.
	locked := []egress.Host{
		{Name: "generativelanguage.googleapis.com", Ports: []uint16{443}},
		{Name: "aiplatform.googleapis.com", Ports: []uint16{443}},
		{Name: "cloudaicompanion.googleapis.com", Ports: []uint16{443}},
		{Name: "oauth2.googleapis.com", Ports: []uint16{443}},
		{Name: "accounts.google.com", Ports: []uint16{443}},
	}
	return agent.Agent{
		Name:    Name,
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
		CredentialPaths:      []string{".gemini/"},
		EgressHosts: map[egress.ProfileName][]egress.Host{
			egress.ProfileLocked:   locked,
			egress.ProfileStandard: append(append([]egress.Host{}, locked...), devhosts.Standard()...),
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
	}
}
