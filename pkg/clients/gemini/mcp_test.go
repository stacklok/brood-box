// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package gemini

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

	data, err := os.ReadFile(filepath.Join(rootfs, configio.SandboxHome, ".gemini", "settings.json"))
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))

	servers, ok := raw["mcpServers"].(map[string]any)
	require.True(t, ok)

	sandboxTools, ok := servers["sandbox-tools"].(map[string]any)
	require.True(t, ok)
	// Gemini CLI uses httpUrl for streamable HTTP (vmcp's transport).
	assert.Equal(t, "http://192.168.127.1:4483/mcp", sandboxTools["httpUrl"])
	_, hasURL := sandboxTools["url"]
	assert.False(t, hasURL, "must not use 'url' — Gemini interprets that as SSE")

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

	data, err := os.ReadFile(filepath.Join(rootfs, configio.SandboxHome, ".gemini", "settings.json"))
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))

	servers := raw["mcpServers"].(map[string]any)
	sandboxTools := servers["sandbox-tools"].(map[string]any)
	assert.Equal(t, "http://10.0.0.1:7777/mcp", sandboxTools["httpUrl"])
}

func TestInject_PreservesExistingKeys(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	geminiDir := filepath.Join(rootfs, configio.SandboxHome, ".gemini")
	require.NoError(t, os.MkdirAll(geminiDir, 0o755))
	configPath := filepath.Join(geminiDir, "settings.json")

	existing := `{"ui": {"theme": "dark"}, "model": "gemini-2.5-pro"}`
	require.NoError(t, os.WriteFile(configPath, []byte(existing), 0o644))

	require.NoError(t, (mcpInjector{}).Inject(rootfs, "192.168.127.1", 4483, chown))

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.Contains(t, raw, "ui")
	assert.Contains(t, raw, "model")
	assert.Contains(t, raw, "mcpServers")
}

func TestInject_FilePermissions(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	require.NoError(t, (mcpInjector{}).Inject(rootfs, "127.0.0.1", 4483, chown))

	info, err := os.Stat(filepath.Join(rootfs, configio.SandboxHome, ".gemini", "settings.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
