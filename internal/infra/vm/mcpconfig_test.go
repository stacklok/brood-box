// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/sandbox-agent/internal/domain/agent"
)

func TestInjectMCPConfig_Dispatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		format   agent.MCPConfigFormat
		wantFile string // relative to rootfs
	}{
		{
			name:     "claude-code writes ~/.claude.json",
			format:   agent.MCPConfigFormatClaudeCode,
			wantFile: "home/sandbox/.claude.json",
		},
		{
			name:     "codex writes ~/.codex/config.toml",
			format:   agent.MCPConfigFormatCodex,
			wantFile: "home/sandbox/.codex/config.toml",
		},
		{
			name:     "opencode writes ~/.config/opencode/opencode.json",
			format:   agent.MCPConfigFormatOpenCode,
			wantFile: "home/sandbox/.config/opencode/opencode.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rootfs := setupRootfs(t)

			hook := InjectMCPConfig(tt.format, "192.168.127.1", 4483)
			err := hook(rootfs, nil)
			require.NoError(t, err)

			_, err = os.Stat(filepath.Join(rootfs, tt.wantFile))
			assert.NoError(t, err, "expected %s to exist", tt.wantFile)
		})
	}
}

func TestInjectMCPConfig_NoneFormat_NoOp(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)

	hook := InjectMCPConfig(agent.MCPConfigFormatNone, "192.168.127.1", 4483)
	err := hook(rootfs, nil)
	require.NoError(t, err)

	// No new files should appear in the home directory beyond what setupRootfs created.
	entries, err := os.ReadDir(filepath.Join(rootfs, sandboxHome))
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestInjectMCPConfig_UnknownFormat_NoOp(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)

	hook := InjectMCPConfig("unknown-agent", "192.168.127.1", 4483)
	err := hook(rootfs, nil)
	require.NoError(t, err)
}

func TestInjectClaudeCodeMCP(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	err := injectClaudeCodeMCP(rootfs, "192.168.127.1", 4483)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".claude.json"))
	require.NoError(t, err)

	var cfg claudeCodeConfig
	require.NoError(t, json.Unmarshal(data, &cfg))

	require.Contains(t, cfg.MCPServers, "sandbox-tools")
	srv := cfg.MCPServers["sandbox-tools"]
	assert.Equal(t, "http", srv.Type)
	assert.Equal(t, "http://192.168.127.1:4483/mcp", srv.URL)
}

func TestInjectClaudeCodeMCP_CustomPort(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	err := injectClaudeCodeMCP(rootfs, "10.0.0.1", 9999)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".claude.json"))
	require.NoError(t, err)

	var cfg claudeCodeConfig
	require.NoError(t, json.Unmarshal(data, &cfg))

	srv := cfg.MCPServers["sandbox-tools"]
	assert.Equal(t, "http://10.0.0.1:9999/mcp", srv.URL)
}

func TestInjectClaudeCodeMCP_NoExtraFields(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	err := injectClaudeCodeMCP(rootfs, "192.168.127.1", 4483)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".claude.json"))
	require.NoError(t, err)

	// Unmarshal to raw map and verify only expected keys are present.
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.Len(t, raw, 1, "top-level should have only mcpServers")
	assert.Contains(t, raw, "mcpServers")

	servers := raw["mcpServers"].(map[string]any)
	assert.Len(t, servers, 1, "should have only sandbox-tools")

	entry := servers["sandbox-tools"].(map[string]any)
	assert.Len(t, entry, 2, "server entry should have only type and url")
}

func TestInjectCodexMCP(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	err := injectCodexMCP(rootfs, "192.168.127.1", 4483)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".codex", "config.toml"))
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "[mcp_servers.sandbox-tools]")
	assert.Contains(t, content, `url = "http://192.168.127.1:4483/mcp"`)
}

func TestInjectCodexMCP_CustomPort(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	err := injectCodexMCP(rootfs, "10.0.0.1", 8080)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".codex", "config.toml"))
	require.NoError(t, err)

	assert.Contains(t, string(data), `url = "http://10.0.0.1:8080/mcp"`)
}

func TestInjectCodexMCP_ValidTOML(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	err := injectCodexMCP(rootfs, "192.168.127.1", 4483)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".codex", "config.toml"))
	require.NoError(t, err)

	// Verify it's not JSON (a common mistake).
	var jsonCheck map[string]any
	assert.Error(t, json.Unmarshal(data, &jsonCheck), "codex config should be TOML, not JSON")
}

func TestInjectOpenCodeMCP(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	err := injectOpenCodeMCP(rootfs, "192.168.127.1", 4483)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".config", "opencode", "opencode.json"))
	require.NoError(t, err)

	var cfg openCodeConfig
	require.NoError(t, json.Unmarshal(data, &cfg))

	require.Contains(t, cfg.MCP, "sandbox-tools")
	srv := cfg.MCP["sandbox-tools"]
	assert.Equal(t, "remote", srv.Type)
	assert.Equal(t, "http://192.168.127.1:4483/mcp", srv.URL)
	assert.True(t, srv.Enabled)
}

func TestInjectOpenCodeMCP_CustomPort(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	err := injectOpenCodeMCP(rootfs, "10.0.0.1", 7777)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".config", "opencode", "opencode.json"))
	require.NoError(t, err)

	var cfg openCodeConfig
	require.NoError(t, json.Unmarshal(data, &cfg))

	assert.Equal(t, "http://10.0.0.1:7777/mcp", cfg.MCP["sandbox-tools"].URL)
}

func TestInjectOpenCodeMCP_UsesCorrectTopLevelKey(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	err := injectOpenCodeMCP(rootfs, "192.168.127.1", 4483)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".config", "opencode", "opencode.json"))
	require.NoError(t, err)

	// Verify top-level key is "mcp", not "mcpServers".
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.Contains(t, raw, "mcp", "top-level key must be 'mcp' for OpenCode")
	assert.NotContains(t, raw, "mcpServers", "must not use 'mcpServers' — that's Claude Code format")
}

func TestInjectOpenCodeMCP_ServerTypeIsRemote(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	err := injectOpenCodeMCP(rootfs, "192.168.127.1", 4483)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".config", "opencode", "opencode.json"))
	require.NoError(t, err)

	var cfg openCodeConfig
	require.NoError(t, json.Unmarshal(data, &cfg))

	// OpenCode uses "remote" (not "http") for HTTP MCP servers.
	assert.Equal(t, "remote", cfg.MCP["sandbox-tools"].Type)
}

func TestMCPConfigFilePermissions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		inject func(string) error
		path   string
	}{
		{
			name:   "claude-code",
			inject: func(root string) error { return injectClaudeCodeMCP(root, "127.0.0.1", 4483) },
			path:   "home/sandbox/.claude.json",
		},
		{
			name:   "codex",
			inject: func(root string) error { return injectCodexMCP(root, "127.0.0.1", 4483) },
			path:   "home/sandbox/.codex/config.toml",
		},
		{
			name:   "opencode",
			inject: func(root string) error { return injectOpenCodeMCP(root, "127.0.0.1", 4483) },
			path:   "home/sandbox/.config/opencode/opencode.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rootfs := setupRootfs(t)

			err := tt.inject(rootfs)
			require.NoError(t, err)

			info, err := os.Stat(filepath.Join(rootfs, tt.path))
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0o644), info.Mode().Perm(),
				"MCP config files should be world-readable (0644)")
		})
	}
}

// setupRootfs creates a minimal rootfs with /home/sandbox/ pre-created,
// mimicking what the OCI image extraction provides.
func setupRootfs(t *testing.T) string {
	t.Helper()
	rootfs := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(rootfs, sandboxHome), 0o755))
	return rootfs
}
