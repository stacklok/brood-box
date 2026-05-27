// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package claudecode

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

func setupRootfsWithCredentials(t *testing.T) string {
	t.Helper()
	rootfs := setupRootfs(t)
	claudeDir := filepath.Join(rootfs, configio.SandboxHome, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(claudeDir, ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"test"}}`),
		0o600,
	))
	return rootfs
}

func TestInject_Basic(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, getCalls := recordingChown()
	require.NoError(t, (mcpInjector{}).Inject(rootfs, "192.168.127.1", 4483, chown))

	data, err := os.ReadFile(filepath.Join(rootfs, configio.SandboxHome, ".claude.json"))
	require.NoError(t, err)

	var cfg struct {
		MCPServers map[string]claudeCodeServer `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(data, &cfg))

	require.Contains(t, cfg.MCPServers, "sandbox-tools")
	srv := cfg.MCPServers["sandbox-tools"]
	assert.Equal(t, "http", srv.Type)
	assert.Equal(t, "http://192.168.127.1:4483/mcp", srv.URL)

	calls := getCalls()
	require.NotEmpty(t, calls)
	for _, c := range calls {
		assert.Equal(t, configio.SandboxUID, c.UID)
		assert.Equal(t, configio.SandboxGID, c.GID)
	}
}

func TestInject_SetsOnboardingFlag_WhenCredentialsExist(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfsWithCredentials(t)
	chown, _ := recordingChown()
	require.NoError(t, (mcpInjector{}).Inject(rootfs, "192.168.127.1", 4483, chown))

	data, err := os.ReadFile(filepath.Join(rootfs, configio.SandboxHome, ".claude.json"))
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.Contains(t, raw, "hasCompletedOnboarding")
	assert.JSONEq(t, "true", string(raw["hasCompletedOnboarding"]))
}

func TestInject_NoOnboardingFlag_WhenNoCredentials(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	require.NoError(t, (mcpInjector{}).Inject(rootfs, "192.168.127.1", 4483, chown))

	data, err := os.ReadFile(filepath.Join(rootfs, configio.SandboxHome, ".claude.json"))
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.NotContains(t, raw, "hasCompletedOnboarding")
}

func TestInject_CustomPort(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	require.NoError(t, (mcpInjector{}).Inject(rootfs, "10.0.0.1", 9999, chown))

	data, err := os.ReadFile(filepath.Join(rootfs, configio.SandboxHome, ".claude.json"))
	require.NoError(t, err)

	var cfg struct {
		MCPServers map[string]claudeCodeServer `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(data, &cfg))
	assert.Equal(t, "http://10.0.0.1:9999/mcp", cfg.MCPServers["sandbox-tools"].URL)
}

func TestInject_NoExtraFields(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	require.NoError(t, (mcpInjector{}).Inject(rootfs, "192.168.127.1", 4483, chown))

	data, err := os.ReadFile(filepath.Join(rootfs, configio.SandboxHome, ".claude.json"))
	require.NoError(t, err)

	var raw map[string]any
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.Len(t, raw, 1, "top-level should have only mcpServers when no credentials")
	assert.Contains(t, raw, "mcpServers")

	servers := raw["mcpServers"].(map[string]any)
	assert.Len(t, servers, 1, "should have only sandbox-tools")

	entry := servers["sandbox-tools"].(map[string]any)
	assert.Len(t, entry, 2, "server entry should have only type and url")
}

func TestInject_PreservesExistingKeys(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	configPath := filepath.Join(rootfs, configio.SandboxHome, ".claude.json")

	existing := `{"hasCompletedOnboarding": true, "theme": "dark"}`
	require.NoError(t, os.WriteFile(configPath, []byte(existing), 0o644))

	require.NoError(t, (mcpInjector{}).Inject(rootfs, "192.168.127.1", 4483, chown))

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.Contains(t, raw, "hasCompletedOnboarding")
	assert.Contains(t, raw, "theme")
	assert.Contains(t, raw, "mcpServers")
	assert.JSONEq(t, "true", string(raw["hasCompletedOnboarding"]))
	assert.JSONEq(t, `"dark"`, string(raw["theme"]))
}

func TestInject_PreservesExistingMCPServers(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	configPath := filepath.Join(rootfs, configio.SandboxHome, ".claude.json")

	existing := `{"mcpServers": {"user-server": {"type": "http", "url": "http://user:1234/mcp"}}}`
	require.NoError(t, os.WriteFile(configPath, []byte(existing), 0o644))

	require.NoError(t, (mcpInjector{}).Inject(rootfs, "192.168.127.1", 4483, chown))

	data, err := os.ReadFile(configPath)
	require.NoError(t, err)

	var cfg struct {
		MCPServers map[string]claudeCodeServer `json:"mcpServers"`
	}
	require.NoError(t, json.Unmarshal(data, &cfg))

	assert.Contains(t, cfg.MCPServers, "sandbox-tools")
	assert.Contains(t, cfg.MCPServers, "user-server")
	assert.Equal(t, "http://192.168.127.1:4483/mcp", cfg.MCPServers["sandbox-tools"].URL)
	assert.Equal(t, "http://user:1234/mcp", cfg.MCPServers["user-server"].URL)
}

func TestInject_FilePermissions(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	require.NoError(t, (mcpInjector{}).Inject(rootfs, "127.0.0.1", 4483, chown))

	info, err := os.Stat(filepath.Join(rootfs, configio.SandboxHome, ".claude.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
