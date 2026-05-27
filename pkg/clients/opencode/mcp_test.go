// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/brood-box/pkg/clients/internal/configio"
	"github.com/stacklok/brood-box/pkg/domain/agent"
)

type chownCall struct {
	Path string
	UID  int
	GID  int
}

func recordingChown() (agent.ChownFunc, func() []chownCall) {
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

func setupRootfs(t *testing.T) string {
	t.Helper()
	rootfs := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(rootfs, configio.SandboxHome), 0o755))
	return rootfs
}

func TestInject_Basic(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, getCalls := recordingChown()
	require.NoError(t, (mcpInjector{}).Inject(rootfs, "192.168.127.1", 4483, chown))

	data, err := os.ReadFile(filepath.Join(rootfs, configio.SandboxHome, ".config", "opencode", "opencode.json"))
	require.NoError(t, err)

	var cfg struct {
		MCP map[string]openCodeServer `json:"mcp"`
	}
	require.NoError(t, json.Unmarshal(data, &cfg))

	require.Contains(t, cfg.MCP, "sandbox-tools")
	srv := cfg.MCP["sandbox-tools"]
	assert.Equal(t, "remote", srv.Type)
	assert.Equal(t, "http://192.168.127.1:4483/mcp", srv.URL)
	assert.True(t, srv.Enabled)

	calls := getCalls()
	require.NotEmpty(t, calls)
	for _, c := range calls {
		assert.Equal(t, configio.SandboxUID, c.UID)
		assert.Equal(t, configio.SandboxGID, c.GID)
	}
}

func TestInject_CustomPort(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	require.NoError(t, (mcpInjector{}).Inject(rootfs, "10.0.0.1", 7777, chown))

	data, err := os.ReadFile(filepath.Join(rootfs, configio.SandboxHome, ".config", "opencode", "opencode.json"))
	require.NoError(t, err)

	var cfg struct {
		MCP map[string]openCodeServer `json:"mcp"`
	}
	require.NoError(t, json.Unmarshal(data, &cfg))

	assert.Equal(t, "http://10.0.0.1:7777/mcp", cfg.MCP["sandbox-tools"].URL)
}

func TestInject_UsesCorrectTopLevelKey(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	require.NoError(t, (mcpInjector{}).Inject(rootfs, "192.168.127.1", 4483, chown))

	data, err := os.ReadFile(filepath.Join(rootfs, configio.SandboxHome, ".config", "opencode", "opencode.json"))
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.Contains(t, raw, "mcp", "top-level key must be 'mcp' for OpenCode")
	assert.NotContains(t, raw, "mcpServers", "must not use 'mcpServers' — that's Claude Code format")
}

func TestInject_PreservesExistingKeys(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	opencodeDir := filepath.Join(rootfs, configio.SandboxHome, ".config", "opencode")
	require.NoError(t, os.MkdirAll(opencodeDir, 0o755))
	configPath := filepath.Join(opencodeDir, "opencode.json")

	existing := `{"theme": "gruvbox", "editor": "nvim"}`
	require.NoError(t, os.WriteFile(configPath, []byte(existing), 0o644))

	require.NoError(t, (mcpInjector{}).Inject(rootfs, "192.168.127.1", 4483, chown))

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

func TestInject_PreservesExistingMCPServers(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	opencodeDir := filepath.Join(rootfs, configio.SandboxHome, ".config", "opencode")
	require.NoError(t, os.MkdirAll(opencodeDir, 0o755))
	configPath := filepath.Join(opencodeDir, "opencode.json")

	existing := `{"mcp": {"user-tool": {"type": "remote", "url": "http://user:1234"}}}`
	require.NoError(t, os.WriteFile(configPath, []byte(existing), 0o644))

	require.NoError(t, (mcpInjector{}).Inject(rootfs, "192.168.127.1", 4483, chown))

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var cfg struct {
		MCP map[string]openCodeServer `json:"mcp"`
	}
	require.NoError(t, json.Unmarshal(data, &cfg))

	assert.Contains(t, cfg.MCP, "sandbox-tools")
	assert.Equal(t, "http://192.168.127.1:4483/mcp", cfg.MCP["sandbox-tools"].URL)
	assert.Contains(t, cfg.MCP, "user-tool")
	assert.Equal(t, "http://user:1234", cfg.MCP["user-tool"].URL)
}

func TestInject_FilePermissions(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	require.NoError(t, (mcpInjector{}).Inject(rootfs, "127.0.0.1", 4483, chown))

	info, err := os.Stat(filepath.Join(rootfs, configio.SandboxHome, ".config", "opencode", "opencode.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
