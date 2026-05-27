// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package hermes

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

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

	data, err := os.ReadFile(filepath.Join(rootfs, configio.SandboxHome, ".hermes", "config.yaml"))
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, yaml.Unmarshal(data, &raw))

	servers, ok := raw["mcp_servers"].(map[string]any)
	require.True(t, ok)

	sandboxTools, ok := servers["sandbox-tools"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "http://192.168.127.1:4483/mcp", sandboxTools["url"])
	_, hasTransport := sandboxTools["transport"]
	assert.False(t, hasTransport, "transport key must not be written; Hermes selects transport by url vs command")

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

	data, err := os.ReadFile(filepath.Join(rootfs, configio.SandboxHome, ".hermes", "config.yaml"))
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, yaml.Unmarshal(data, &raw))

	servers := raw["mcp_servers"].(map[string]any)
	sandboxTools := servers["sandbox-tools"].(map[string]any)
	assert.Equal(t, "http://10.0.0.1:7777/mcp", sandboxTools["url"])
}

func TestInject_PreservesExistingKeys(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	hermesDir := filepath.Join(rootfs, configio.SandboxHome, ".hermes")
	require.NoError(t, os.MkdirAll(hermesDir, 0o755))
	configPath := filepath.Join(hermesDir, "config.yaml")

	existing := "provider: openrouter\n" +
		"model: anthropic/claude-opus-4.6\n" +
		"mcp_servers:\n" +
		"  user-tool:\n" +
		"    transport: http\n" +
		"    url: http://user:1234/mcp\n"
	require.NoError(t, os.WriteFile(configPath, []byte(existing), 0o644))

	require.NoError(t, (mcpInjector{}).Inject(rootfs, "192.168.127.1", 4483, chown))

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, yaml.Unmarshal(data, &raw))

	assert.Equal(t, "openrouter", raw["provider"])
	assert.Equal(t, "anthropic/claude-opus-4.6", raw["model"])

	servers, ok := raw["mcp_servers"].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, servers, "sandbox-tools")
	assert.Contains(t, servers, "user-tool")

	userTool := servers["user-tool"].(map[string]any)
	assert.Equal(t, "http://user:1234/mcp", userTool["url"])
}

func TestInject_FilePermissions(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	require.NoError(t, (mcpInjector{}).Inject(rootfs, "127.0.0.1", 4483, chown))

	info, err := os.Stat(filepath.Join(rootfs, configio.SandboxHome, ".hermes", "config.yaml"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
