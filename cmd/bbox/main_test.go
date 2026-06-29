// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	infraagent "github.com/stacklok/brood-box/internal/infra/agent"
	"github.com/stacklok/brood-box/pkg/clients"
	"github.com/stacklok/brood-box/pkg/domain/bytesize"
	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
)

// boolPtr is a test helper that returns a pointer to a bool.
func boolPtr(b bool) *bool { return &b }

// warnHeader is the banner printed before the warning list.
const warnHeader = "Security: .broodbox.yaml in this workspace modifies sandbox settings:\n"

// warnFooter is the guidance printed after the warning list.
const warnFooter = "Review .broodbox.yaml before proceeding if this is unexpected.\n"

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
				"review.enabled (interactive review) is ignored for security — use --review or global config",
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
		// --- Auth seed_host_credentials ---
		{
			name: "auth.seed_host_credentials ignored warning",
			local: &domainconfig.Config{
				Auth: domainconfig.AuthConfig{SeedHostCredentials: boolPtr(true)},
			},
			global: defaultGlobal,
			expected: wrapWarnings(
				"auth.seed_host_credentials is ignored in workspace config — use --seed-credentials flag or global config",
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
				Defaults: domainconfig.DefaultsConfig{Memory: bytesize.ByteSize(131072)},
			},
			global:   defaultGlobal,
			expected: wrapWarnings("sets default memory: 128g"),
		},
		{
			name: "defaults CPUs clamped",
			local: &domainconfig.Config{
				Defaults: domainconfig.DefaultsConfig{CPUs: 256},
			},
			global: defaultGlobal,
			expected: wrapWarnings(fmt.Sprintf("sets default CPUs: 256 (clamped to %d)",
				domainconfig.MaxCPUs)),
		},
		{
			name: "defaults memory clamped",
			local: &domainconfig.Config{
				Defaults: domainconfig.DefaultsConfig{Memory: bytesize.ByteSize(999999)},
			},
			global: defaultGlobal,
			expected: wrapWarnings(fmt.Sprintf("sets default memory: %s (clamped to %s)",
				bytesize.ByteSize(999999), domainconfig.MaxMemory)),
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
				"attempts to override myagent image: ghcr.io/evil/image:latest — ignored (image must be declared in global config)",
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
			expected: wrapWarnings("attempts to override myagent command — ignored (command must be declared in global config)"),
		},
		{
			name: "agent env forwarding",
			local: &domainconfig.Config{
				Agents: map[string]domainconfig.AgentOverride{
					"myagent": {EnvForward: []string{"SECRET_*"}},
				},
			},
			global:   defaultGlobal,
			expected: wrapWarnings("narrows myagent env forwarding (can only restrict, not widen)"),
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
					"myagent": {CPUs: 128, Memory: bytesize.ByteSize(99999)},
				},
			},
			global: defaultGlobal,
			expected: wrapWarnings(
				"sets myagent CPUs: 128",
				fmt.Sprintf("sets myagent memory: %s", bytesize.ByteSize(99999)),
			),
		},
		{
			name: "agent CPUs and memory clamped",
			local: &domainconfig.Config{
				Agents: map[string]domainconfig.AgentOverride{
					"myagent": {CPUs: 256, Memory: bytesize.ByteSize(999999)},
				},
			},
			global: defaultGlobal,
			expected: wrapWarnings(
				fmt.Sprintf("sets myagent CPUs: 256 (clamped to %d)", domainconfig.MaxCPUs),
				fmt.Sprintf("sets myagent memory: %s (clamped to %s)", bytesize.ByteSize(999999), domainconfig.MaxMemory),
			),
		},
		// --- Agent MCP override ---
		{
			name: "agent MCP override enabled and authz",
			local: &domainconfig.Config{
				Agents: map[string]domainconfig.AgentOverride{
					"myagent": {
						MCP: &domainconfig.MCPAgentOverride{
							Enabled: boolPtr(false),
							Authz:   &domainconfig.MCPAuthzConfig{Profile: domainconfig.MCPAuthzProfileObserve},
						},
					},
				},
			},
			global: defaultGlobal,
			expected: wrapWarnings(
				"sets myagent MCP enabled: false",
				"sets myagent MCP authz profile: observe (can only tighten, not widen)",
			),
		},
		{
			name: "agent MCP authz custom ignored",
			local: &domainconfig.Config{
				Agents: map[string]domainconfig.AgentOverride{
					"myagent": {
						MCP: &domainconfig.MCPAgentOverride{
							Authz: &domainconfig.MCPAuthzConfig{Profile: domainconfig.MCPAuthzProfileCustom},
						},
					},
				},
			},
			global: defaultGlobal,
			expected: wrapWarnings(
				`myagent MCP authz profile "custom" is ignored — custom profiles cannot be set from workspace config`,
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
				"attempts to override a-agent image: img-a — ignored (image must be declared in global config)",
				"attempts to override m-agent image: img-m — ignored (image must be declared in global config)",
				"attempts to override z-agent image: img-z — ignored (image must be declared in global config)",
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
			contains:  []string{"attempts to override evil[0magent image: img"},
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
			contains:  []string{"attempts to override myagent image: ghcr.io/evil[2J/image"},
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
				Auth: domainconfig.AuthConfig{
					SaveCredentials:     boolPtr(true),
					SeedHostCredentials: boolPtr(true),
				},
				Defaults: domainconfig.DefaultsConfig{
					EgressProfile: "locked",
					CPUs:          8,
					Memory:        bytesize.ByteSize(4096),
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
						MCP: &domainconfig.MCPAgentOverride{
							Authz: &domainconfig.MCPAuthzConfig{Profile: domainconfig.MCPAuthzProfileObserve},
						},
					},
				},
			},
			global: defaultGlobal,
			expected: wrapWarnings(
				"review.enabled (interactive review) is ignored for security — use --review or global config",
				"auth.save_credentials is ignored in workspace config — use --no-save-credentials flag or global config",
				"auth.seed_host_credentials is ignored in workspace config — use --seed-credentials flag or global config",
				"adds review exclude patterns: *.log",
				"sets default egress profile: locked",
				"sets default CPUs: 8",
				"sets default memory: 4g",
				"adds egress hosts: global-extra.com",
				"sets git token forwarding: false",
				"attempts to override myagent image: custom:v1 — ignored (image must be declared in global config)",
				"attempts to override myagent command — ignored (command must be declared in global config)",
				"narrows myagent env forwarding (can only restrict, not widen)",
				"adds myagent egress hosts: agent-extra.com",
				"sets myagent egress profile: locked",
				"sets myagent MCP authz profile: observe (can only tighten, not widen)",
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// These cases exercise the per-field OVERRIDE warnings for agents
			// that already exist globally. To isolate that behavior from the
			// separately tested "local adds a new agent" / "local adds
			// credentials" warnings, ensure every agent key present locally is
			// also present in the global config (with no credential paths).
			global := tt.global
			if tt.local != nil && len(tt.local.Agents) > 0 {
				merged := &domainconfig.Config{}
				if tt.global != nil {
					*merged = *tt.global
				}
				agents := map[string]domainconfig.AgentOverride{}
				for k, v := range merged.Agents {
					agents[k] = v
				}
				for k := range tt.local.Agents {
					if _, ok := agents[k]; !ok {
						agents[k] = domainconfig.AgentOverride{Image: "ghcr.io/global/base:latest"}
					}
				}
				merged.Agents = agents
				global = merged
			}

			var buf bytes.Buffer
			warnLocalConfigOverrides(&buf, tt.local, global)
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

func TestWarnLocalConfigOverrides_BlockedAgentAdditions(t *testing.T) {
	t.Parallel()

	t.Run("local adds a new agent", func(t *testing.T) {
		t.Parallel()
		local := &domainconfig.Config{
			Agents: map[string]domainconfig.AgentOverride{
				"sneaky": {Image: "ghcr.io/evil/agent:latest"},
			},
		}
		var buf bytes.Buffer
		warnLocalConfigOverrides(&buf, local, &domainconfig.Config{})
		got := buf.String()
		if !strings.Contains(got, `attempts to define a new agent "sneaky"`) {
			t.Errorf("expected new-agent warning, got:\n%s", got)
		}
	})

	t.Run("local adds credential paths to existing agent", func(t *testing.T) {
		t.Parallel()
		local := &domainconfig.Config{
			Agents: map[string]domainconfig.AgentOverride{
				"existing": {Credentials: &domainconfig.AgentCredentialsConfig{Persist: []string{".x/creds"}}},
			},
		}
		global := &domainconfig.Config{
			Agents: map[string]domainconfig.AgentOverride{
				"existing": {Image: "ghcr.io/global/agent:latest"},
			},
		}
		var buf bytes.Buffer
		warnLocalConfigOverrides(&buf, local, global)
		got := buf.String()
		if !strings.Contains(got, "attempts to add existing credential paths") {
			t.Errorf("expected credential warning, got:\n%s", got)
		}
	})
}

func TestRegisterCustomAgents(t *testing.T) {
	t.Parallel()

	registry := infraagent.NewRegistry(clients.Builtins(slog.New(slog.NewTextHandler(io.Discard, nil)))...)

	merged := &domainconfig.Config{
		Agents: map[string]domainconfig.AgentOverride{
			// Valid custom agent — should be registered.
			"good": {
				Image:         "ghcr.io/acme/good:latest",
				Command:       []string{"run"},
				EgressProfile: "permissive",
			},
			// Invalid custom agent (bad image ref + escaping credential path) —
			// must be warned and skipped, not abort the run.
			"badagent": {
				Image:         "::::bad ref::::",
				Command:       []string{"run"},
				EgressProfile: "permissive",
				Credentials:   &domainconfig.AgentCredentialsConfig{Persist: []string{"../escape.json"}},
			},
			// Override with empty Image (an override for an unregistered agent) —
			// skipped silently (no warning).
			"emptyimage": {
				Command: []string{"run"},
			},
			// A built-in name present in config — must be skipped (kept as built-in).
			"claude-code": {
				Image: "ghcr.io/evil/replacement:latest",
			},
		},
	}

	var warn bytes.Buffer
	registerCustomAgents(registry, merged, &warn)

	// Valid custom agent is registered as a data-only entry (nil Plugin).
	entry, err := registry.Get("good")
	if err != nil {
		t.Fatalf("expected custom agent %q to be registered: %v", "good", err)
	}
	if entry.Plugin != nil {
		t.Errorf("custom agent %q should have a nil Plugin (data-only entry)", "good")
	}
	if entry.Agent.Image != "ghcr.io/acme/good:latest" {
		t.Errorf("custom agent image = %q, want %q", entry.Agent.Image, "ghcr.io/acme/good:latest")
	}

	// Invalid agent is not registered.
	if _, err := registry.Get("badagent"); err == nil {
		t.Errorf("invalid custom agent %q must not be registered", "badagent")
	}

	// Empty-image override is not registered.
	if _, err := registry.Get("emptyimage"); err == nil {
		t.Errorf("empty-image override %q must not be registered", "emptyimage")
	}

	// Built-in keeps its own plugin entry — not replaced by the local override.
	cc, err := registry.Get("claude-code")
	if err != nil {
		t.Fatalf("built-in claude-code must remain registered: %v", err)
	}
	if cc.Plugin == nil {
		t.Errorf("built-in claude-code must keep its Plugin (override must not replace it)")
	}
	if cc.Agent.Image == "ghcr.io/evil/replacement:latest" {
		t.Errorf("built-in claude-code image must not be replaced by a local override")
	}

	got := warn.String()
	if !strings.Contains(got, `skipping custom agent "badagent"`) {
		t.Errorf("expected warning for invalid agent, got:\n%s", got)
	}
	// Empty-image override and built-in skip are silent.
	if strings.Contains(got, "emptyimage") {
		t.Errorf("empty-image override should be skipped silently, got:\n%s", got)
	}
	if strings.Contains(got, "claude-code") {
		t.Errorf("built-in name in config should be skipped silently, got:\n%s", got)
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

// ---------------------------------------------------------------------------
// setupLogger tests
// ---------------------------------------------------------------------------

func TestSetupLogger_DebugLevel(t *testing.T) {
	t.Parallel()

	f, err := os.CreateTemp(t.TempDir(), "log-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	logger := setupLogger(f, true)
	logger.Debug("debug-msg")
	logger.Info("info-msg")

	// Flush by syncing.
	_ = f.Sync()

	content, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(content), "debug-msg") {
		t.Errorf("debug message should appear in log when debug=true, got:\n%s", content)
	}
	if !strings.Contains(string(content), "info-msg") {
		t.Errorf("info message should appear in log when debug=true, got:\n%s", content)
	}
}

func TestSetupLogger_InfoLevel(t *testing.T) {
	t.Parallel()

	f, err := os.CreateTemp(t.TempDir(), "log-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()

	logger := setupLogger(f, false)
	logger.Debug("debug-msg")
	logger.Info("info-msg")

	_ = f.Sync()

	content, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(content), "debug-msg") {
		t.Errorf("debug message should NOT appear in log when debug=false, got:\n%s", content)
	}
	if !strings.Contains(string(content), "info-msg") {
		t.Errorf("info message should appear in log when debug=false, got:\n%s", content)
	}
}

func TestSetupLogger_NilFile(t *testing.T) {
	t.Parallel()

	logger := setupLogger(nil, false)
	if logger == nil {
		t.Fatal("setupLogger(nil, false) returned nil, expected non-nil logger")
	}

	// Should not panic when used.
	logger.Info("test message")
	logger.Debug("debug message")
	logger.With("key", "value").Info("with attrs")
}

// ---------------------------------------------------------------------------
// openLogFile tests
// ---------------------------------------------------------------------------

func TestOpenLogFile_DefaultPath(t *testing.T) {
	// Cannot use t.Parallel() with t.Setenv().
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	vmName := "test-vm-abc123"

	logPath, f, closer, err := openLogFile("", vmName)
	if err != nil {
		t.Fatalf("openLogFile returned error: %v", err)
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}

	if f == nil {
		t.Fatal("expected non-nil file")
	}

	// Path should contain expected segments.
	if !strings.Contains(logPath, ".config") {
		t.Errorf("log path should contain '.config', got: %s", logPath)
	}
	if !strings.Contains(logPath, "broodbox") {
		t.Errorf("log path should contain 'broodbox', got: %s", logPath)
	}
	if !strings.Contains(logPath, "vms") {
		t.Errorf("log path should contain 'vms', got: %s", logPath)
	}
	if !strings.Contains(logPath, vmName) {
		t.Errorf("log path should contain VM name %q, got: %s", vmName, logPath)
	}
	if !strings.Contains(logPath, defaultLogFile) {
		t.Errorf("log path should contain %q, got: %s", defaultLogFile, logPath)
	}

	// Directory should exist.
	dir := filepath.Dir(logPath)
	info, statErr := os.Stat(dir)
	if statErr != nil {
		t.Fatalf("log directory does not exist: %v", statErr)
	}
	if !info.IsDir() {
		t.Error("log path parent is not a directory")
	}
}

func TestOpenLogFile_OverridePath(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	overridePath := filepath.Join(tmpDir, "custom.log")

	logPath, f, closer, err := openLogFile(overridePath, "ignored-vm-name")
	if err != nil {
		t.Fatalf("openLogFile returned error: %v", err)
	}
	if closer != nil {
		defer func() { _ = closer.Close() }()
	}

	if logPath != overridePath {
		t.Errorf("expected override path %q, got %q", overridePath, logPath)
	}
	if f == nil {
		t.Fatal("expected non-nil file")
	}

	// Verify the file was created at the override path.
	_, statErr := os.Stat(overridePath)
	if statErr != nil {
		t.Fatalf("override log file not created: %v", statErr)
	}
}

// ---------------------------------------------------------------------------
// sanitizeAll tests
// ---------------------------------------------------------------------------

func TestSanitizeAll(t *testing.T) {
	t.Parallel()

	input := []string{"hello", "evil\x1b[2J.com", "clean"}
	got := sanitizeAll(input)

	expected := []string{"hello", "evil[2J.com", "clean"}
	if len(got) != len(expected) {
		t.Fatalf("length mismatch: got %d, want %d", len(got), len(expected))
	}
	for i := range expected {
		if got[i] != expected[i] {
			t.Errorf("sanitizeAll[%d] = %q, want %q", i, got[i], expected[i])
		}
	}
}

// ---------------------------------------------------------------------------
// Cache directory function tests
// ---------------------------------------------------------------------------

func TestCacheDirFunctions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		fn        func() (string, error)
		expectSub string
	}{
		{
			name:      "runtimeCacheDir",
			fn:        runtimeCacheDir,
			expectSub: "runtime",
		},
		{
			name:      "firmwareCacheDir",
			fn:        firmwareCacheDir,
			expectSub: "firmware",
		},
		{
			name:      "imageCacheDir",
			fn:        imageCacheDir,
			expectSub: "images",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path, err := tt.fn()
			if err != nil {
				t.Fatalf("%s() returned error: %v", tt.name, err)
			}
			if !strings.Contains(path, "broodbox") {
				t.Errorf("%s() path should contain 'broodbox', got: %s", tt.name, path)
			}
			if !strings.Contains(path, tt.expectSub) {
				t.Errorf("%s() path should contain %q, got: %s", tt.name, tt.expectSub, path)
			}
		})
	}
}

// TestApplyCustomAgentAuthzDefault asserts the safe-tools default is applied
// to custom agents with MCP enabled and no explicit authz, while built-ins and
// already-resolved configs are left unchanged.
func TestApplyCustomAgentAuthzDefault(t *testing.T) {
	t.Parallel()

	explicit := &domainconfig.MCPAuthzConfig{Profile: domainconfig.MCPAuthzProfileObserve}

	tests := []struct {
		name     string
		in       *domainconfig.MCPAuthzConfig
		isCustom bool
		want     *domainconfig.MCPAuthzConfig
	}{
		{
			name:     "custom agent with no authz defaults to safe-tools",
			in:       nil,
			isCustom: true,
			want:     &domainconfig.MCPAuthzConfig{Profile: domainconfig.DefaultCustomAgentMCPAuthzProfile},
		},
		{
			name:     "built-in agent with no authz stays full-access (nil)",
			in:       nil,
			isCustom: false,
			want:     nil,
		},
		{
			name:     "custom agent with explicit authz is unchanged",
			in:       explicit,
			isCustom: true,
			want:     explicit,
		},
		{
			name:     "built-in agent with explicit authz is unchanged",
			in:       explicit,
			isCustom: false,
			want:     explicit,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := applyCustomAgentAuthzDefault(tt.in, tt.isCustom)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("expected nil authz config, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("expected %+v, got nil", tt.want)
			}
			if got.Profile != tt.want.Profile {
				t.Errorf("profile = %q, want %q", got.Profile, tt.want.Profile)
			}
		})
	}

	// Confirm the default constant really is safe-tools (the secure default).
	if domainconfig.DefaultCustomAgentMCPAuthzProfile != domainconfig.MCPAuthzProfileSafeTools {
		t.Errorf("DefaultCustomAgentMCPAuthzProfile = %q, want %q",
			domainconfig.DefaultCustomAgentMCPAuthzProfile, domainconfig.MCPAuthzProfileSafeTools)
	}
}

// TestIsCustomAgent asserts built-ins (Plugin != nil) are not custom, while
// data-only registered agents (nil Plugin) are.
func TestIsCustomAgent(t *testing.T) {
	t.Parallel()

	registry := infraagent.NewRegistry(clients.Builtins(slog.New(slog.NewTextHandler(io.Discard, nil)))...)
	customAgent, err := domainconfig.AgentFromOverride("my-custom", domainconfig.AgentOverride{
		Image:         "ghcr.io/acme/my-custom:latest",
		Command:       []string{"run"},
		EgressProfile: "permissive",
	}, domainconfig.DefaultsConfig{})
	if err != nil {
		t.Fatalf("building custom agent: %v", err)
	}
	if err := registry.Add(customAgent); err != nil {
		t.Fatalf("adding custom agent: %v", err)
	}

	if isCustomAgent(registry, "claude-code") {
		t.Error("claude-code is a built-in, isCustomAgent should be false")
	}
	if !isCustomAgent(registry, "my-custom") {
		t.Error("my-custom is data-only, isCustomAgent should be true")
	}
	if isCustomAgent(registry, "does-not-exist") {
		t.Error("unknown agent should not be reported as custom")
	}
}
