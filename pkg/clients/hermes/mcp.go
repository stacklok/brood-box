// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package hermes

import (
	"fmt"
	"path/filepath"

	"github.com/stacklok/brood-box/pkg/clients/internal/configio"
	"github.com/stacklok/brood-box/pkg/domain/agent"
)

// Ref: https://github.com/NousResearch/hermes-agent
// Global config lives at ~/.hermes/config.yaml and exposes an `mcp_servers`
// mapping. Hermes is a provider-agnostic client CLI; the MCP entry points
// the agent at the brood-box vmcp proxy so it can call sandbox tools.

type mcpInjector struct{}

// Inject merges an MCP server entry into ~/.hermes/config.yaml, preserving
// any pre-existing YAML keys (provider config, skills, cron, etc.).
func (mcpInjector) Inject(rootfsPath, gatewayIP string, port uint16, chown agent.ChownFunc) error {
	mcpURL := fmt.Sprintf("http://%s:%d/mcp", gatewayIP, port)

	hermesDir := filepath.Join(rootfsPath, configio.SandboxHome, ".hermes")
	if err := configio.MkdirAndChown(hermesDir, chown); err != nil {
		return fmt.Errorf("creating ~/.hermes dir: %w", err)
	}

	// Hermes's MCP loader (upstream tools/mcp_tool.py) selects transport by
	// the presence of `url` vs `command`; no `transport:` field is read, so
	// we only emit `url` to avoid colliding with any future upstream schema.
	return configio.MergeYAMLMapEntries(hermesDir, "config.yaml", "mcp_servers", map[string]any{
		"sandbox-tools": map[string]any{
			"url": mcpURL,
		},
	}, chown)
}
