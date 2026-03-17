// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
				Defaults: domainconfig.DefaultsConfig{Memory: domainconfig.ByteSize(131072)},
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
				Defaults: domainconfig.DefaultsConfig{Memory: domainconfig.ByteSize(999999)},
			},
			global: defaultGlobal,
			expected: wrapWarnings(fmt.Sprintf("sets default memory: %s (clamped to %s)",
				domainconfig.ByteSize(999999), domainconfig.MaxMemory)),
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
					"myagent": {CPUs: 128, Memory: domainconfig.ByteSize(99999)},
				},
			},
			global: defaultGlobal,
			expected: wrapWarnings(
				"sets myagent CPUs: 128",
				fmt.Sprintf("sets myagent memory: %s", domainconfig.ByteSize(99999)),
			),
		},
		{
			name: "agent CPUs and memory clamped",
			local: &domainconfig.Config{
				Agents: map[string]domainconfig.AgentOverride{
					"myagent": {CPUs: 256, Memory: domainconfig.ByteSize(999999)},
				},
			},
			global: defaultGlobal,
			expected: wrapWarnings(
				fmt.Sprintf("sets myagent CPUs: 256 (clamped to %d)", domainconfig.MaxCPUs),
				fmt.Sprintf("sets myagent memory: %s (clamped to %s)", domainconfig.ByteSize(999999), domainconfig.MaxMemory),
			),
		},
		// --- Agent MCP override (CRITICAL-1 fix) ---
		{
			name: "agent MCP override all fields",
			local: &domainconfig.Config{
				Agents: map[string]domainconfig.AgentOverride{
					"myagent": {
						MCP: &domainconfig.MCPConfig{
							Enabled: boolPtr(false),
							Group:   "evil-group",
							Port:    9999,
							Config: &domainconfig.MCPFileConfig{
								Authz: &domainconfig.MCPFileAuthzConfig{
									Policies: []string{`permit(principal, action, resource);`},
								},
							},
						},
					},
				},
			},
			global: defaultGlobal,
			expected: wrapWarnings(
				"sets myagent MCP enabled: false",
				"sets myagent MCP group: evil-group",
				"sets myagent MCP port: 9999",
				"sets myagent MCP config (inline Cedar policies/aggregation)",
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
				Auth: domainconfig.AuthConfig{
					SaveCredentials:     boolPtr(true),
					SeedHostCredentials: boolPtr(true),
				},
				Defaults: domainconfig.DefaultsConfig{
					EgressProfile: "locked",
					CPUs:          8,
					Memory:        domainconfig.ByteSize(4096),
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
				"review.enabled (interactive review) is ignored for security — use --review or global config",
				"auth.save_credentials is ignored in workspace config — use --no-save-credentials flag or global config",
				"auth.seed_host_credentials is ignored in workspace config — use --seed-credentials flag or global config",
				"adds review exclude patterns: *.log",
				"sets default egress profile: locked",
				"sets default CPUs: 8",
				"sets default memory: 4g",
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
