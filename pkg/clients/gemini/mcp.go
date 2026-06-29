// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package gemini

import (
	"fmt"
	"path/filepath"

	"github.com/stacklok/brood-box/pkg/clients/internal/configio"
	"github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/config"
)

// Ref: https://github.com/google-gemini/gemini-cli/blob/main/docs/tools/mcp-server.md
// User config lives at ~/.gemini/settings.json (JSON, nested-categories
// format from v0.3.0+). MCP servers go under the top-level "mcpServers"
// key. We use httpUrl (HTTP streaming) since the vmcp proxy speaks
// streamable HTTP at /mcp; "url" would be SSE.

type mcpInjector struct{}

// Inject merges an MCP server entry into ~/.gemini/settings.json,
// preserving any pre-existing keys.
func (mcpInjector) Inject(rootfsPath, gatewayIP string, port uint16, chown agent.ChownFunc) error {
	geminiDir := filepath.Join(rootfsPath, configio.SandboxHome, ".gemini")
	if err := configio.MkdirAndChown(geminiDir, chown); err != nil {
		return fmt.Errorf("creating ~/.gemini dir: %w", err)
	}

	return configio.MergeJSONMapEntries(geminiDir, "settings.json", "mcpServers", map[string]any{
		"sandbox-tools": map[string]any{
			"httpUrl": fmt.Sprintf("http://%s:%d%s", gatewayIP, port, config.MCPEndpointPath),
		},
	}, chown)
}
