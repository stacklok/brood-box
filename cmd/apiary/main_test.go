// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"strings"
	"testing"

	domainconfig "github.com/stacklok/apiary/pkg/domain/config"
)

// boolPtr is a test helper that returns a pointer to a bool.
func boolPtr(b bool) *bool { return &b }

// warnHeader is the banner printed before the warning list.
const warnHeader = "Security: .apiary.yaml in this workspace modifies sandbox settings:\n"

// warnFooter is the guidance printed after the warning list.
const warnFooter = "Review .apiary.yaml before proceeding if this is unexpected.\n"

// wrapWarnings builds the expected full output block: blank line, header, bullets, footer, blank line.
func wrapWarnings(bullets ...string) string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(warnHeader)
	for _, bullet := range bullets {
		b.WriteString("  - ")
		b.WriteString(bullet)
		b.WriteString("\n")
	}
	b.WriteString(warnFooter)
	b.WriteString("\n")
	return b.String()
}

func TestWarnLocalConfigOverrides(t *testing.T) {
	t.Parallel()

	// defaultGlobal is a zero-value global config used when
	// the test doesn't care about global state.
	defaultGlobal := &domainconfig.Config{}

	tests := []struct {
		name      string
		local     *domainconfig.Config
		global    *domainconfig.Config
		expected  string
		contains  []string // substring checks (used instead of exact match when set)
		notContai []string // must NOT appear
	}{
		{
			name:     "nil config produces no output",
			local:    nil,
			global:   defaultGlobal,
			expected: "",
		},
		{
			name:     "empty config produces no output",
			local:    &domainconfig.Config{},
			global:   defaultGlobal,
			expected: "",
		},
		// --- Network ---
		{
			name: "network allow_hosts",
			local: &domainconfig.Config{
				Network: domainconfig.NetworkConfig{
					AllowHosts: []domainconfig.EgressHostConfig{
						{Name: "foo.com"},
						{Name: "bar.com"},
					},
				},
			},
			global: defaultGlobal,
			expected: wrapWarnings(
				"adds egress hosts: foo.com, bar.com",
			),
		},
		// --- Review ---
		{
			name: "review.enabled ignored warning",
			local: &domainconfig.Config{
				Review: domainconfig.ReviewConfig{Enabled: boolPtr(false)},
			},
			global: defaultGlobal,
			expected: wrapWarnings(
				"review.enabled is ignored for security — use --no-review or global config",
			),
		},
		{
			name: "review exclude_patterns",
			local: &domainconfig.Config{
				Review: domainconfig.ReviewConfig{
					ExcludePatterns: []string{"**/*.go", "secrets/"},
				},
			},
			global: defaultGlobal,
			expected: wrapWarnings(
				"adds review exclude patterns: **/*.go, secrets/",
			),
		},
		// --- Defaults ---
		{
			name: "defaults egress profile tightens",
			local: &domainconfig.Config{
				Defaults: domainconfig.DefaultsConfig{EgressProfile: "locked"},
			},
			global:   defaultGlobal,
			expected: wrapWarnings("sets default egress profile: locked"),
		},
		{
			name: "defaults egress profile cannot widen",
			local: &domainconfig.Config{
				Defaults: domainconfig.DefaultsConfig{EgressProfile: "permissive"},
			},
			global: &domainconfig.Config{
				Defaults: domainconfig.DefaultsConfig{EgressProfile: "standard"},
			},
			expected: wrapWarnings(
				`default egress profile "permissive" cannot widen "standard" — ignored`,
			),
		},
		{
			name: "defaults CPUs",
			local: &domainconfig.Config{
				Defaults: domainconfig.DefaultsConfig{CPUs: 64},
			},
			global:   defaultGlobal,
			expected: wrapWarnings("sets default CPUs: 64"),
		},
		{
			name: "defaults memory",
			local: &domainconfig.Config{
				Defaults: domainconfig.DefaultsConfig{Memory: 131072},
			},
			global:   defaultGlobal,
			expected: wrapWarnings("sets default memory: 131072 MiB"),
		},
		// --- Git ---
		{
			name: "git forward_token set",
			local: &domainconfig.Config{
				Git: domainconfig.GitConfig{ForwardToken: boolPtr(false)},
			},
			global:   defaultGlobal,
			expected: wrapWarnings("sets git token forwarding: false"),
		},
		{
			name: "git forward_ssh_agent set",
			local: &domainconfig.Config{
				Git: domainconfig.GitConfig{ForwardSSHAgent: boolPtr(true)},
			},
			global:   defaultGlobal,
			expected: wrapWarnings("sets git SSH agent forwarding: true"),
		},
		// --- Agent overrides ---
		{
			name: "agent image override",
			local: &domainconfig.Config{
				Agents: map[string]domainconfig.AgentOverride{
					"myagent": {Image: "ghcr.io/evil/image:latest"},
				},
			},
			global: defaultGlobal,
			expected: wrapWarnings(
				"overrides myagent image: ghcr.io/evil/image:latest",
			),
		},
		{
			name: "agent command override",
			local: &domainconfig.Config{
				Agents: map[string]domainconfig.AgentOverride{
					"myagent": {Command: []string{"/bin/sh", "-c", "evil"}},
				},
			},
			global:   defaultGlobal,
			expected: wrapWarnings("overrides myagent command"),
		},
		{
			name: "agent env forwarding",
			local: &domainconfig.Config{
				Agents: map[string]domainconfig.AgentOverride{
					"myagent": {EnvForward: []string{"SECRET_*"}},
				},
			},
			global:   defaultGlobal,
			expected: wrapWarnings("overrides myagent env forwarding"),
		},
		{
			name: "agent allow_hosts",
			local: &domainconfig.Config{
				Agents: map[string]domainconfig.AgentOverride{
					"myagent": {
						AllowHosts: []domainconfig.EgressHostConfig{
							{Name: "evil.com"},
							{Name: "c2.example.org"},
						},
					},
				},
			},
			global: defaultGlobal,
			expected: wrapWarnings(
				"adds myagent egress hosts: evil.com, c2.example.org",
			),
		},
		{
			name: "agent egress profile",
			local: &domainconfig.Config{
				Agents: map[string]domainconfig.AgentOverride{
					"myagent": {EgressProfile: "locked"},
				},
			},
			global:   defaultGlobal,
			expected: wrapWarnings("sets myagent egress profile: locked"),
		},
		{
			name: "agent CPUs and memory",
			local: &domainconfig.Config{
				Agents: map[string]domainconfig.AgentOverride{
					"myagent": {CPUs: 128, Memory: 99999},
				},
			},
			global: defaultGlobal,
			expected: wrapWarnings(
				"sets myagent CPUs: 128",
				"sets myagent memory: 99999 MiB",
			),
		},
		// --- Agent MCP override (CRITICAL-1 fix) ---
		{
			name: "agent MCP override all fields",
			local: &domainconfig.Config{
				Agents: map[string]domainconfig.AgentOverride{
					"myagent": {
						MCP: &domainconfig.MCPConfig{
							Enabled:    boolPtr(false),
							Group:      "evil-group",
							Port:       9999,
							ConfigPath: "/tmp/evil-mcp.yaml",
						},
					},
				},
			},
			global: defaultGlobal,
			expected: wrapWarnings(
				"sets myagent MCP enabled: false",
				"sets myagent MCP group: evil-group",
				"sets myagent MCP port: 9999",
				"sets myagent MCP config path: /tmp/evil-mcp.yaml",
			),
		},
		// --- Ordering ---
		{
			name: "multiple agents sorted alphabetically",
			local: &domainconfig.Config{
				Agents: map[string]domainconfig.AgentOverride{
					"z-agent": {Image: "img-z"},
					"a-agent": {Image: "img-a"},
					"m-agent": {Image: "img-m"},
				},
			},
			global: defaultGlobal,
			expected: wrapWarnings(
				"overrides a-agent image: img-a",
				"overrides m-agent image: img-m",
				"overrides z-agent image: img-z",
			),
		},
		// --- ANSI escape sanitization (CRITICAL-2 fix) ---
		{
			name: "host name with ANSI escape sequences stripped",
			local: &domainconfig.Config{
				Network: domainconfig.NetworkConfig{
					AllowHosts: []domainconfig.EgressHostConfig{
						{Name: "\x1b[2Jevil.com"},
					},
				},
			},
			global:    defaultGlobal,
			contains:  []string{"adds egress hosts: [2Jevil.com"},
			notContai: []string{"\x1b"},
		},
		{
			name: "agent name with control chars stripped",
			local: &domainconfig.Config{
				Agents: map[string]domainconfig.AgentOverride{
					"evil\x1b[0m\x07agent": {Image: "img"},
				},
			},
			global:    defaultGlobal,
			contains:  []string{"overrides evil[0magent image: img"},
			notContai: []string{"\x1b", "\x07"},
		},
		{
			name: "image value with control chars stripped",
			local: &domainconfig.Config{
				Agents: map[string]domainconfig.AgentOverride{
					"myagent": {Image: "ghcr.io/evil\x1b[2J/image"},
				},
			},
			global:    defaultGlobal,
			contains:  []string{"overrides myagent image: ghcr.io/evil[2J/image"},
			notContai: []string{"\x1b"},
		},
		// --- Combined: all fields set ---
		{
			name: "all fields produce all warnings in order",
			local: &domainconfig.Config{
				Review: domainconfig.ReviewConfig{
					Enabled:         boolPtr(true),
					ExcludePatterns: []string{"*.log"},
				},
				Defaults: domainconfig.DefaultsConfig{
					EgressProfile: "locked",
					CPUs:          8,
					Memory:        4096,
				},
				Network: domainconfig.NetworkConfig{
					AllowHosts: []domainconfig.EgressHostConfig{
						{Name: "global-extra.com"},
					},
				},
				Git: domainconfig.GitConfig{
					ForwardToken: boolPtr(false),
				},
				Agents: map[string]domainconfig.AgentOverride{
					"myagent": {
						Image:         "custom:v1",
						Command:       []string{"run"},
						EnvForward:    []string{"KEY"},
						EgressProfile: "locked",
						AllowHosts: []domainconfig.EgressHostConfig{
							{Name: "agent-extra.com"},
						},
						MCP: &domainconfig.MCPConfig{
							Group: "custom",
						},
					},
				},
			},
			global: defaultGlobal,
			expected: wrapWarnings(
				"review.enabled is ignored for security — use --no-review or global config",
				"adds review exclude patterns: *.log",
				"sets default egress profile: locked",
				"sets default CPUs: 8",
				"sets default memory: 4096 MiB",
				"adds egress hosts: global-extra.com",
				"sets git token forwarding: false",
				"overrides myagent image: custom:v1",
				"overrides myagent command",
				"overrides myagent env forwarding",
				"adds myagent egress hosts: agent-extra.com",
				"sets myagent egress profile: locked",
				"sets myagent MCP group: custom",
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			warnLocalConfigOverrides(&buf, tt.local, tt.global)
			got := buf.String()

			if tt.expected != "" || (tt.contains == nil && tt.notContai == nil) {
				if got != tt.expected {
					t.Errorf("output mismatch:\ngot:\n%s\nwant:\n%s", got, tt.expected)
				}
			}
			for _, s := range tt.contains {
				if !strings.Contains(got, s) {
					t.Errorf("output missing expected substring %q:\n%s", s, got)
				}
			}
			for _, s := range tt.notContai {
				if strings.Contains(got, s) {
					t.Errorf("output contains forbidden substring %q:\n%s", s, got)
				}
			}
		})
	}
}

func TestSanitizeValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"clean string", "hello.com", "hello.com"},
		{"ANSI clear screen", "\x1b[2Jevil.com", "[2Jevil.com"},
		{"bell character", "evil\x07.com", "evil.com"},
		{"tab and newline", "evil\t.com\n", "evil.com"},
		{"null byte", "evil\x00.com", "evil.com"},
		{"mixed control chars", "\x1b[0m\x07\x00safe", "[0msafe"},
		{"empty string", "", ""},
		{"only control chars", "\x1b\x07\x00", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := sanitizeValue(tt.input); got != tt.expected {
				t.Errorf("sanitizeValue(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
