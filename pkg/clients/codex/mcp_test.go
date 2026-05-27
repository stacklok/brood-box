// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
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

	data, err := os.ReadFile(filepath.Join(rootfs, configio.SandboxHome, ".codex", "config.toml"))
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "[mcp_servers.sandbox-tools]")
	assert.Contains(t, content, `url = 'http://192.168.127.1:4483/mcp'`)

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
	require.NoError(t, (mcpInjector{}).Inject(rootfs, "10.0.0.1", 8080, chown))

	data, err := os.ReadFile(filepath.Join(rootfs, configio.SandboxHome, ".codex", "config.toml"))
	require.NoError(t, err)
	assert.Contains(t, string(data), `url = 'http://10.0.0.1:8080/mcp'`)
}

func TestInject_ValidTOML_NotJSON(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	require.NoError(t, (mcpInjector{}).Inject(rootfs, "192.168.127.1", 4483, chown))

	data, err := os.ReadFile(filepath.Join(rootfs, configio.SandboxHome, ".codex", "config.toml"))
	require.NoError(t, err)

	var jsonCheck map[string]any
	assert.Error(t, json.Unmarshal(data, &jsonCheck), "codex config should be TOML, not JSON")
}

func TestInject_PreservesExistingSections(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	codexDir := filepath.Join(rootfs, configio.SandboxHome, ".codex")
	require.NoError(t, os.MkdirAll(codexDir, 0o755))
	configPath := filepath.Join(codexDir, "config.toml")

	existing := "[some_other_section]\nfoo = \"bar\"\n"
	require.NoError(t, os.WriteFile(configPath, []byte(existing), 0o644))

	require.NoError(t, (mcpInjector{}).Inject(rootfs, "192.168.127.1", 4483, chown))

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, toml.Unmarshal(data, &parsed))

	otherSection, ok := parsed["some_other_section"].(map[string]any)
	require.True(t, ok, "some_other_section must be preserved")
	assert.Equal(t, "bar", otherSection["foo"])

	mcpServers, ok := parsed["mcp_servers"].(map[string]any)
	require.True(t, ok)
	sandboxTools, ok := mcpServers["sandbox-tools"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "http://192.168.127.1:4483/mcp", sandboxTools["url"])
}

func TestInject_PreservesExistingMCPServers(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	codexDir := filepath.Join(rootfs, configio.SandboxHome, ".codex")
	require.NoError(t, os.MkdirAll(codexDir, 0o755))
	configPath := filepath.Join(codexDir, "config.toml")

	existing := "[mcp_servers.user-tool]\nurl = 'http://user:1234/mcp'\n"
	require.NoError(t, os.WriteFile(configPath, []byte(existing), 0o644))

	require.NoError(t, (mcpInjector{}).Inject(rootfs, "192.168.127.1", 4483, chown))

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var parsed map[string]any
	require.NoError(t, toml.Unmarshal(data, &parsed))

	mcpServers := parsed["mcp_servers"].(map[string]any)
	sandboxTools := mcpServers["sandbox-tools"].(map[string]any)
	assert.Equal(t, "http://192.168.127.1:4483/mcp", sandboxTools["url"])

	userTool := mcpServers["user-tool"].(map[string]any)
	assert.Equal(t, "http://user:1234/mcp", userTool["url"])
}

func TestInject_FilePermissions(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	require.NoError(t, (mcpInjector{}).Inject(rootfs, "127.0.0.1", 4483, chown))

	info, err := os.Stat(filepath.Join(rootfs, configio.SandboxHome, ".codex", "config.toml"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
