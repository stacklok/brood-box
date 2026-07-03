// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/stacklok/brood-box/pkg/domain/agent"
)

func newTestMCPInjector(entries []agent.MCPInjectEntry, env map[string]string) *MCPConfigInjector {
	return NewMCPConfigInjector(entries, env, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// noopChown is a ChownFunc that always succeeds (host tests are not root).
func noopChown(string, int, int) error { return nil }

// guestPath returns the on-disk path of a guest-home-relative config file.
func guestPath(rootfs, rel string) string {
	return filepath.Join(rootfs, sandboxHome, rel)
}

func TestMCPConfigInjector_CreatesJSONWithSubstitution(t *testing.T) {
	t.Parallel()
	rootfs := t.TempDir()

	inj := newTestMCPInjector(
		[]agent.MCPInjectEntry{{
			GuestPath: ".config/aider/mcp.json",
			Format:    agent.MCPInjectFormatJSON,
			Merge: map[string]any{
				"mcpServers": map[string]any{
					"broodbox": map[string]any{
						"type": "streamable-http",
						"url":  "${BBOX_MCP_URL}",
					},
				},
			},
		}},
		map[string]string{"BBOX_MCP_URL": "http://192.168.127.1:4483/mcp"},
	)

	require.NoError(t, inj.Inject(rootfs, "192.168.127.1", 4483, noopChown))

	raw, err := os.ReadFile(guestPath(rootfs, ".config/aider/mcp.json"))
	require.NoError(t, err)

	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	servers := got["mcpServers"].(map[string]any)
	broodbox := servers["broodbox"].(map[string]any)
	assert.Equal(t, "http://192.168.127.1:4483/mcp", broodbox["url"])
	assert.Equal(t, "streamable-http", broodbox["type"])
}

func TestMCPConfigInjector_MergesIntoExistingFile(t *testing.T) {
	t.Parallel()
	rootfs := t.TempDir()

	// Pre-existing file with an unrelated key and a pre-existing server.
	dst := guestPath(rootfs, ".claude.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o755))
	existing := map[string]any{
		"hasCompletedOnboarding": true,
		"mcpServers": map[string]any{
			"other": map[string]any{"url": "http://example.test"},
		},
	}
	data, err := json.Marshal(existing)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dst, data, 0o600))

	inj := newTestMCPInjector(
		[]agent.MCPInjectEntry{{
			GuestPath: ".claude.json",
			Format:    agent.MCPInjectFormatJSON,
			Merge: map[string]any{
				"mcpServers": map[string]any{
					"broodbox": map[string]any{"url": "${BBOX_MCP_URL}"},
				},
			},
		}},
		map[string]string{"BBOX_MCP_URL": "http://gw:4483/mcp"},
	)
	require.NoError(t, inj.Inject(rootfs, "gw", 4483, noopChown))

	raw, err := os.ReadFile(dst)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))

	// Pre-existing top-level key preserved.
	assert.Equal(t, true, got["hasCompletedOnboarding"])
	servers := got["mcpServers"].(map[string]any)
	// Pre-existing server preserved, new server added.
	assert.Contains(t, servers, "other")
	assert.Contains(t, servers, "broodbox")
	assert.Equal(t, "http://gw:4483/mcp", servers["broodbox"].(map[string]any)["url"])
}

func TestMCPConfigInjector_TOMLAndYAML(t *testing.T) {
	t.Parallel()
	rootfs := t.TempDir()

	inj := newTestMCPInjector(
		[]agent.MCPInjectEntry{
			{
				GuestPath: ".codex/config.toml",
				Format:    agent.MCPInjectFormatTOML,
				Merge:     map[string]any{"mcp_servers": map[string]any{"broodbox": map[string]any{"url": "${BBOX_MCP_URL}"}}},
			},
			{
				GuestPath: ".config/app/config.yaml",
				Format:    agent.MCPInjectFormatYAML,
				Merge:     map[string]any{"servers": map[string]any{"broodbox": "${BBOX_MCP_URL}"}},
			},
		},
		map[string]string{"BBOX_MCP_URL": "http://gw:4483/mcp"},
	)
	require.NoError(t, inj.Inject(rootfs, "gw", 4483, noopChown))

	tomlRaw, err := os.ReadFile(guestPath(rootfs, ".codex/config.toml"))
	require.NoError(t, err)
	var tomlGot map[string]any
	require.NoError(t, toml.Unmarshal(tomlRaw, &tomlGot))
	assert.Equal(t, "http://gw:4483/mcp",
		tomlGot["mcp_servers"].(map[string]any)["broodbox"].(map[string]any)["url"])

	yamlRaw, err := os.ReadFile(guestPath(rootfs, ".config/app/config.yaml"))
	require.NoError(t, err)
	var yamlGot map[string]any
	require.NoError(t, yaml.Unmarshal(yamlRaw, &yamlGot))
	assert.Equal(t, "http://gw:4483/mcp", yamlGot["servers"].(map[string]any)["broodbox"])
}

func TestMCPConfigInjector_UnknownEnvLeftLiteral(t *testing.T) {
	t.Parallel()
	rootfs := t.TempDir()

	inj := newTestMCPInjector(
		[]agent.MCPInjectEntry{{
			GuestPath: "cfg.json",
			Format:    agent.MCPInjectFormatJSON,
			// $HOME is not BBOX_-prefixed; ${BBOX_MISSING} is absent from env.
			Merge: map[string]any{"a": "${HOME}", "b": "${BBOX_MISSING}", "c": "${BBOX_MCP_URL}"},
		}},
		map[string]string{"BBOX_MCP_URL": "http://gw/mcp", "HOME": "/root"},
	)
	require.NoError(t, inj.Inject(rootfs, "gw", 4483, noopChown))

	raw, err := os.ReadFile(guestPath(rootfs, "cfg.json"))
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	assert.Equal(t, "${HOME}", got["a"], "non-BBOX vars must not be substituted")
	assert.Equal(t, "${BBOX_MISSING}", got["b"], "absent BBOX vars must stay literal")
	assert.Equal(t, "http://gw/mcp", got["c"])
}

func TestExpandBBOXString(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"BBOX_MCP_URL": "http://gw:4483/mcp",
		"BBOX_A":       "valA",
		"BBOX_B":       "valB",
		"HOME":         "/root",
	}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "substitutes known BBOX var",
			in:   "${BBOX_MCP_URL}",
			want: "http://gw:4483/mcp",
		},
		{
			name: "leaves non-BBOX-prefixed var literal",
			in:   "${HOME}",
			want: "${HOME}",
		},
		{
			name: "leaves absent BBOX var literal",
			in:   "${BBOX_MISSING}",
			want: "${BBOX_MISSING}",
		},
		{
			name: "unterminated token left entirely literal",
			in:   "prefix ${unterminated",
			want: "prefix ${unterminated",
		},
		{
			name: "empty token does not match identifier pattern",
			in:   "${}",
			want: "${}",
		},
		{
			name: "back-to-back tokens both resolve independently",
			in:   "${BBOX_A}${BBOX_B}",
			want: "valAvalB",
		},
		{
			name: "malformed earlier span does not swallow later real token",
			in:   "prefix ${nomatch more ${BBOX_MCP_URL} end",
			want: "prefix ${nomatch more http://gw:4483/mcp end",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, expandBBOXString(tt.in, env))
		})
	}
}

func TestMCPConfigInjector_RejectsPathEscape(t *testing.T) {
	t.Parallel()
	rootfs := t.TempDir()

	inj := newTestMCPInjector(
		[]agent.MCPInjectEntry{{
			GuestPath: "../../escape.json",
			Format:    agent.MCPInjectFormatJSON,
			Merge:     map[string]any{"a": "b"},
		}},
		nil,
	)
	err := inj.Inject(rootfs, "gw", 4483, noopChown)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "containment")
}
