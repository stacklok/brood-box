// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package opencode

import (
	"fmt"
	"path/filepath"

	"github.com/stacklok/brood-box/pkg/clients/internal/configio"
	"github.com/stacklok/brood-box/pkg/domain/agent"
)

// Ref: https://opencode.ai/docs/mcp-servers/
// Global config lives at ~/.config/opencode/opencode.json.

type mcpInjector struct{}

// openCodeServer describes a single MCP server entry for OpenCode.
type openCodeServer struct {
	Type    string `json:"type"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}

// Inject merges an MCP server entry into ~/.config/opencode/opencode.json,
// preserving any pre-existing keys.
func (mcpInjector) Inject(rootfsPath, gatewayIP string, port uint16, chown agent.ChownFunc) error {
	servers := map[string]openCodeServer{
		"sandbox-tools": {
			Type:    "remote",
			URL:     fmt.Sprintf("http://%s:%d/mcp", gatewayIP, port),
			Enabled: true,
		},
	}

	opencodeDir := filepath.Join(rootfsPath, configio.SandboxHome, ".config", "opencode")
	if err := configio.MkdirAndChown(opencodeDir, chown); err != nil {
		return fmt.Errorf("creating ~/.config/opencode dir: %w", err)
	}

	// Deep-merge to preserve user MCP servers injected by settings hook.
	serversMap := make(map[string]any, len(servers))
	for k, v := range servers {
		serversMap[k] = v
	}
	return configio.MergeJSONMapEntries(opencodeDir, "opencode.json", "mcp", serversMap, chown)
}
