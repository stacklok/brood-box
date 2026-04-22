// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/stacklok/brood-box/pkg/domain/agent"
)

// chownCall records a single chown invocation.
type chownCall struct {
	Path string
	UID  int
	GID  int
}

// recordingChown returns a ChownFunc that records calls and a function
// to retrieve the recorded calls. Safe for concurrent use.
func recordingChown() (ChownFunc, func() []chownCall) {
	var mu sync.Mutex
	var calls []chownCall
	fn := func(path string, uid, gid int) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, chownCall{Path: path, UID: uid, GID: gid})
		return nil
	}
	get := func() []chownCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]chownCall{}, calls...)
	}
	return fn, get
}

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
		{
			name:     "hermes writes ~/.hermes/config.yaml",
			format:   agent.MCPConfigFormatHermes,
			wantFile: "home/sandbox/.hermes/config.yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rootfs := setupRootfs(t)
			chown, _ := recordingChown()

			hook := InjectMCPConfig(tt.format, "192.168.127.1", 4483, chown)
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
	chown, getCalls := recordingChown()

	hook := InjectMCPConfig(agent.MCPConfigFormatNone, "192.168.127.1", 4483, chown)
	err := hook(rootfs, nil)
	require.NoError(t, err)

	// No new files should appear in the home directory beyond what setupRootfs created.
	entries, err := os.ReadDir(filepath.Join(rootfs, sandboxHome))
	require.NoError(t, err)
	assert.Empty(t, entries)
	assert.Empty(t, getCalls(), "chown should not be called for no-op format")
}

func TestInjectMCPConfig_UnknownFormat_NoOp(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, getCalls := recordingChown()

	hook := InjectMCPConfig("unknown-agent", "192.168.127.1", 4483, chown)
	err := hook(rootfs, nil)
	require.NoError(t, err)
	assert.Empty(t, getCalls(), "chown should not be called for unknown format")
}

func TestInjectClaudeCodeMCP(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, getCalls := recordingChown()
	err := injectClaudeCodeMCP(rootfs, "192.168.127.1", 4483, chown)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".claude.json"))
	require.NoError(t, err)

	var cfg claudeCodeConfig
	require.NoError(t, json.Unmarshal(data, &cfg))

	require.Contains(t, cfg.MCPServers, "sandbox-tools")
	srv := cfg.MCPServers["sandbox-tools"]
	assert.Equal(t, "http", srv.Type)
	assert.Equal(t, "http://192.168.127.1:4483/mcp", srv.URL)

	calls := getCalls()
	require.NotEmpty(t, calls, "chown must be called")
	for _, c := range calls {
		assert.Equal(t, sandboxUID, c.UID)
		assert.Equal(t, sandboxGID, c.GID)
	}
}

func TestInjectClaudeCodeMCP_SetsOnboardingFlag_WhenCredentialsExist(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfsWithCredentials(t)
	chown, _ := recordingChown()
	err := injectClaudeCodeMCP(rootfs, "192.168.127.1", 4483, chown)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".claude.json"))
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.Contains(t, raw, "hasCompletedOnboarding")
	assert.JSONEq(t, "true", string(raw["hasCompletedOnboarding"]))
}

func TestInjectClaudeCodeMCP_NoOnboardingFlag_WhenNoCredentials(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	err := injectClaudeCodeMCP(rootfs, "192.168.127.1", 4483, chown)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".claude.json"))
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.NotContains(t, raw, "hasCompletedOnboarding",
		"onboarding flag should not be set without credentials")
}

func TestInjectClaudeCodeMCP_CustomPort(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	err := injectClaudeCodeMCP(rootfs, "10.0.0.1", 9999, chown)
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
	chown, _ := recordingChown()
	err := injectClaudeCodeMCP(rootfs, "192.168.127.1", 4483, chown)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".claude.json"))
	require.NoError(t, err)

	// Unmarshal to raw map and verify only expected keys are present.
	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.Len(t, raw, 1, "top-level should have only mcpServers when no credentials")
	assert.Contains(t, raw, "mcpServers")

	servers := raw["mcpServers"].(map[string]any)
	assert.Len(t, servers, 1, "should have only sandbox-tools")

	entry := servers["sandbox-tools"].(map[string]any)
	assert.Len(t, entry, 2, "server entry should have only type and url")
}

func TestInjectCodexMCP(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, getCalls := recordingChown()
	err := injectCodexMCP(rootfs, "192.168.127.1", 4483, chown)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".codex", "config.toml"))
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "[mcp_servers.sandbox-tools]")
	assert.Contains(t, content, `url = 'http://192.168.127.1:4483/mcp'`)

	calls := getCalls()
	require.NotEmpty(t, calls, "chown must be called")
	for _, c := range calls {
		assert.Equal(t, sandboxUID, c.UID)
		assert.Equal(t, sandboxGID, c.GID)
	}
}

func TestInjectCodexMCP_CustomPort(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	err := injectCodexMCP(rootfs, "10.0.0.1", 8080, chown)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".codex", "config.toml"))
	require.NoError(t, err)

	assert.Contains(t, string(data), `url = 'http://10.0.0.1:8080/mcp'`)
}

func TestInjectCodexMCP_ValidTOML(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	err := injectCodexMCP(rootfs, "192.168.127.1", 4483, chown)
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
	chown, getCalls := recordingChown()
	err := injectOpenCodeMCP(rootfs, "192.168.127.1", 4483, chown)
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

	calls := getCalls()
	require.NotEmpty(t, calls, "chown must be called")
	for _, c := range calls {
		assert.Equal(t, sandboxUID, c.UID)
		assert.Equal(t, sandboxGID, c.GID)
	}
}

func TestInjectOpenCodeMCP_CustomPort(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	err := injectOpenCodeMCP(rootfs, "10.0.0.1", 7777, chown)
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
	chown, _ := recordingChown()
	err := injectOpenCodeMCP(rootfs, "192.168.127.1", 4483, chown)
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
	chown, _ := recordingChown()
	err := injectOpenCodeMCP(rootfs, "192.168.127.1", 4483, chown)
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

	chown, _ := recordingChown()

	tests := []struct {
		name   string
		inject func(string) error
		path   string
	}{
		{
			name:   "claude-code",
			inject: func(root string) error { return injectClaudeCodeMCP(root, "127.0.0.1", 4483, chown) },
			path:   "home/sandbox/.claude.json",
		},
		{
			name:   "codex",
			inject: func(root string) error { return injectCodexMCP(root, "127.0.0.1", 4483, chown) },
			path:   "home/sandbox/.codex/config.toml",
		},
		{
			name:   "opencode",
			inject: func(root string) error { return injectOpenCodeMCP(root, "127.0.0.1", 4483, chown) },
			path:   "home/sandbox/.config/opencode/opencode.json",
		},
		{
			name:   "hermes",
			inject: func(root string) error { return injectHermesMCP(root, "127.0.0.1", 4483, chown) },
			path:   "home/sandbox/.hermes/config.yaml",
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
			assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
				"MCP config files should be owner-only (0600)")
		})
	}
}

// --- Merge tests: verify pre-existing config is preserved ---

func TestInjectClaudeCodeMCP_PreservesExistingKeys(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	configPath := filepath.Join(rootfs, sandboxHome, ".claude.json")

	// Pre-populate with existing user config.
	existing := `{"hasCompletedOnboarding": true, "theme": "dark"}`
	require.NoError(t, os.WriteFile(configPath, []byte(existing), 0o644))

	err := injectClaudeCodeMCP(rootfs, "192.168.127.1", 4483, chown)
	require.NoError(t, err)

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.Contains(t, raw, "hasCompletedOnboarding")
	assert.Contains(t, raw, "theme")
	assert.Contains(t, raw, "mcpServers")

	// Verify the preserved values are correct.
	assert.JSONEq(t, "true", string(raw["hasCompletedOnboarding"]))
	assert.JSONEq(t, `"dark"`, string(raw["theme"]))
}

func TestInjectClaudeCodeMCP_PreservesExistingMCPServers(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	configPath := filepath.Join(rootfs, sandboxHome, ".claude.json")

	// Pre-populate with a user MCP server entry.
	existing := `{"mcpServers": {"user-server": {"type": "http", "url": "http://user:1234/mcp"}}}`
	require.NoError(t, os.WriteFile(configPath, []byte(existing), 0o644))

	err := injectClaudeCodeMCP(rootfs, "192.168.127.1", 4483, chown)
	require.NoError(t, err)

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var cfg claudeCodeConfig
	require.NoError(t, json.Unmarshal(data, &cfg))

	assert.Contains(t, cfg.MCPServers, "sandbox-tools", "new entry must be present")
	assert.Contains(t, cfg.MCPServers, "user-server", "existing user entry must be preserved")
	assert.Equal(t, "http://192.168.127.1:4483/mcp", cfg.MCPServers["sandbox-tools"].URL)
	assert.Equal(t, "http://user:1234/mcp", cfg.MCPServers["user-server"].URL)
}

func TestInjectOpenCodeMCP_PreservesExistingKeys(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	opencodeDir := filepath.Join(rootfs, sandboxHome, ".config", "opencode")
	require.NoError(t, os.MkdirAll(opencodeDir, 0o755))
	configPath := filepath.Join(opencodeDir, "opencode.json")

	// Pre-populate with existing user config.
	existing := `{"theme": "gruvbox", "editor": "nvim"}`
	require.NoError(t, os.WriteFile(configPath, []byte(existing), 0o644))

	err := injectOpenCodeMCP(rootfs, "192.168.127.1", 4483, chown)
	require.NoError(t, err)

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.Contains(t, raw, "theme")
	assert.Contains(t, raw, "editor")
	assert.Contains(t, raw, "mcp")

	assert.JSONEq(t, `"gruvbox"`, string(raw["theme"]))
	assert.JSONEq(t, `"nvim"`, string(raw["editor"]))
}

func TestInjectCodexMCP_PreservesExistingSections(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	codexDir := filepath.Join(rootfs, sandboxHome, ".codex")
	require.NoError(t, os.MkdirAll(codexDir, 0o755))
	configPath := filepath.Join(codexDir, "config.toml")

	// Pre-populate with an existing TOML section.
	existing := "[some_other_section]\nfoo = \"bar\"\n"
	require.NoError(t, os.WriteFile(configPath, []byte(existing), 0o644))

	err := injectCodexMCP(rootfs, "192.168.127.1", 4483, chown)
	require.NoError(t, err)

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, toml.Unmarshal(data, &parsed))

	// Verify original section is preserved.
	otherSection, ok := parsed["some_other_section"].(map[string]any)
	require.True(t, ok, "some_other_section must be preserved")
	assert.Equal(t, "bar", otherSection["foo"])

	// Verify MCP section is present.
	mcpServers, ok := parsed["mcp_servers"].(map[string]any)
	require.True(t, ok, "mcp_servers must be present")

	sandboxTools, ok := mcpServers["sandbox-tools"].(map[string]any)
	require.True(t, ok, "sandbox-tools must be present")
	assert.Equal(t, "http://192.168.127.1:4483/mcp", sandboxTools["url"])
}

func TestInjectCodexMCP_PreservesExistingMCPServers(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	codexDir := filepath.Join(rootfs, sandboxHome, ".codex")
	require.NoError(t, os.MkdirAll(codexDir, 0o755))
	configPath := filepath.Join(codexDir, "config.toml")

	// Pre-populate with a user MCP server entry.
	existing := "[mcp_servers.user-tool]\nurl = 'http://user:1234/mcp'\n"
	require.NoError(t, os.WriteFile(configPath, []byte(existing), 0o644))

	err := injectCodexMCP(rootfs, "192.168.127.1", 4483, chown)
	require.NoError(t, err)

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, toml.Unmarshal(data, &parsed))

	mcpServers, ok := parsed["mcp_servers"].(map[string]any)
	require.True(t, ok, "mcp_servers must be present")

	// Verify sandbox-tools was injected.
	sandboxTools, ok := mcpServers["sandbox-tools"].(map[string]any)
	require.True(t, ok, "sandbox-tools must be present")
	assert.Equal(t, "http://192.168.127.1:4483/mcp", sandboxTools["url"])

	// Verify user-tool is preserved.
	userTool, ok := mcpServers["user-tool"].(map[string]any)
	require.True(t, ok, "user-tool must be preserved")
	assert.Equal(t, "http://user:1234/mcp", userTool["url"])
}

func TestInjectOpenCodeMCP_PreservesExistingMCPServers(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	opencodeDir := filepath.Join(rootfs, sandboxHome, ".config", "opencode")
	require.NoError(t, os.MkdirAll(opencodeDir, 0o755))
	configPath := filepath.Join(opencodeDir, "opencode.json")

	// Pre-populate with a user MCP server entry.
	existing := `{"mcp": {"user-tool": {"type": "remote", "url": "http://user:1234"}}}`
	require.NoError(t, os.WriteFile(configPath, []byte(existing), 0o644))

	err := injectOpenCodeMCP(rootfs, "192.168.127.1", 4483, chown)
	require.NoError(t, err)

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var cfg openCodeConfig
	require.NoError(t, json.Unmarshal(data, &cfg))

	// Verify sandbox-tools was injected.
	assert.Contains(t, cfg.MCP, "sandbox-tools", "sandbox-tools must be present")
	assert.Equal(t, "http://192.168.127.1:4483/mcp", cfg.MCP["sandbox-tools"].URL)

	// Verify user-tool is preserved.
	assert.Contains(t, cfg.MCP, "user-tool", "user-tool must be preserved")
	assert.Equal(t, "http://user:1234", cfg.MCP["user-tool"].URL)
}

func TestMergeJSONMapEntries_CorruptFile(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	dir := filepath.Join(rootfs, sandboxHome)

	// Write invalid JSON.
	corruptPath := filepath.Join(dir, "corrupt.json")
	require.NoError(t, os.WriteFile(corruptPath, []byte(`{not valid json`), 0o644))

	err := mergeJSONMapEntries(dir, "corrupt.json", "key", map[string]any{
		"new-entry": map[string]any{"url": "http://localhost"},
	}, chown)
	assert.Error(t, err, "should fail on corrupt JSON")
	assert.Contains(t, err.Error(), "parsing existing")
}

func TestMergeJSONMapEntries_NonMapValue(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	dir := filepath.Join(rootfs, sandboxHome)

	// Write JSON where the target key is a string, not a map.
	filePath := filepath.Join(dir, "nonmap.json")
	require.NoError(t, os.WriteFile(filePath, []byte(`{"key": "string_not_map"}`), 0o644))

	err := mergeJSONMapEntries(dir, "nonmap.json", "key", map[string]any{
		"new-entry": map[string]any{"url": "http://localhost"},
	}, chown)
	require.NoError(t, err, "should succeed by replacing the non-map value")

	data, err := os.ReadFile(filePath)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	// The key should now contain the injected map, not the old string.
	var innerMap map[string]any
	require.NoError(t, json.Unmarshal(raw["key"], &innerMap))
	assert.Contains(t, innerMap, "new-entry", "new-entry must be present after replacing non-map value")
}

func TestInjectHermesMCP(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, getCalls := recordingChown()
	err := injectHermesMCP(rootfs, "192.168.127.1", 4483, chown)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".hermes", "config.yaml"))
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, yaml.Unmarshal(data, &raw))

	servers, ok := raw["mcp_servers"].(map[string]any)
	require.True(t, ok, "mcp_servers must be a map")

	sandboxTools, ok := servers["sandbox-tools"].(map[string]any)
	require.True(t, ok, "sandbox-tools must be present")
	assert.Equal(t, "http://192.168.127.1:4483/mcp", sandboxTools["url"])
	_, hasTransport := sandboxTools["transport"]
	assert.False(t, hasTransport, "transport key must not be written; Hermes selects transport by url vs command")

	calls := getCalls()
	require.NotEmpty(t, calls, "chown must be called")
	for _, c := range calls {
		assert.Equal(t, sandboxUID, c.UID)
		assert.Equal(t, sandboxGID, c.GID)
	}
}

func TestInjectHermesMCP_CustomPort(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	err := injectHermesMCP(rootfs, "10.0.0.1", 7777, chown)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".hermes", "config.yaml"))
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, yaml.Unmarshal(data, &raw))

	servers := raw["mcp_servers"].(map[string]any)
	sandboxTools := servers["sandbox-tools"].(map[string]any)
	assert.Equal(t, "http://10.0.0.1:7777/mcp", sandboxTools["url"])
}

func TestInjectHermesMCP_PreservesExistingKeys(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	hermesDir := filepath.Join(rootfs, sandboxHome, ".hermes")
	require.NoError(t, os.MkdirAll(hermesDir, 0o755))
	configPath := filepath.Join(hermesDir, "config.yaml")

	// Pre-populate with an existing user model/provider selection + a
	// pre-existing MCP server that must survive the merge.
	existing := "provider: openrouter\n" +
		"model: anthropic/claude-opus-4.6\n" +
		"mcp_servers:\n" +
		"  user-tool:\n" +
		"    transport: http\n" +
		"    url: http://user:1234/mcp\n"
	require.NoError(t, os.WriteFile(configPath, []byte(existing), 0o644))

	err := injectHermesMCP(rootfs, "192.168.127.1", 4483, chown)
	require.NoError(t, err)

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, yaml.Unmarshal(data, &raw))

	assert.Equal(t, "openrouter", raw["provider"], "top-level provider must be preserved")
	assert.Equal(t, "anthropic/claude-opus-4.6", raw["model"], "top-level model must be preserved")

	servers, ok := raw["mcp_servers"].(map[string]any)
	require.True(t, ok)

	assert.Contains(t, servers, "sandbox-tools", "sandbox-tools must be present")
	assert.Contains(t, servers, "user-tool", "existing user entry must be preserved")

	userTool := servers["user-tool"].(map[string]any)
	assert.Equal(t, "http://user:1234/mcp", userTool["url"])
}

// setupRootfs creates a minimal rootfs with /home/sandbox/ pre-created,
// mimicking what the OCI image extraction provides.
func setupRootfs(t *testing.T) string {
	t.Helper()
	rootfs := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(rootfs, sandboxHome), 0o755))
	return rootfs
}

// setupRootfsWithCredentials creates a rootfs with a dummy credentials file
// at /home/sandbox/.claude/.credentials.json, simulating what the credential
// injection hook produces before the MCP hook runs.
func setupRootfsWithCredentials(t *testing.T) string {
	t.Helper()
	rootfs := setupRootfs(t)
	claudeDir := filepath.Join(rootfs, sandboxHome, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(claudeDir, ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"test"}}`),
		0o600,
	))
	return rootfs
}
