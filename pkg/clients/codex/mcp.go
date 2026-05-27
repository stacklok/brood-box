// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package codex

import (
	"fmt"
	"path/filepath"

	"github.com/stacklok/brood-box/pkg/clients/internal/configio"
	"github.com/stacklok/brood-box/pkg/domain/agent"
)

// Ref: https://developers.openai.com/codex/config-reference/
// Global config lives at ~/.codex/config.toml (TOML, not JSON).

type mcpInjector struct{}

// Inject merges an MCP server entry into ~/.codex/config.toml, preserving
// any pre-existing TOML sections.
func (mcpInjector) Inject(rootfsPath, gatewayIP string, port uint16, chown agent.ChownFunc) error {
	mcpURL := fmt.Sprintf("http://%s:%d/mcp", gatewayIP, port)

	codexDir := filepath.Join(rootfsPath, configio.SandboxHome, ".codex")
	if err := configio.MkdirAndChown(codexDir, chown); err != nil {
		return fmt.Errorf("creating ~/.codex dir: %w", err)
	}

	return configio.MergeTOMLMapEntries(codexDir, "config.toml", "mcp_servers", map[string]any{
		"sandbox-tools": map[string]any{
			"url": mcpURL,
		},
	}, chown)
}
