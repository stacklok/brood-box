// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package claudecode

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/stacklok/brood-box/pkg/clients/internal/configio"
	"github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/config"
)

// Ref: https://code.claude.com/docs/en/mcp
// User-scope MCP servers live in ~/.claude.json under the top-level
// "mcpServers" key, available across all projects.

type mcpInjector struct{}

// claudeCodeServer describes a single MCP server entry for Claude Code.
type claudeCodeServer struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// Inject merges an MCP server entry into ~/.claude.json, preserving any
// pre-existing keys (auth tokens, onboarding flags, etc.). If credentials
// were already injected into the rootfs (by the credential hook that runs
// earlier), it also sets hasCompletedOnboarding so Claude Code skips the
// interactive setup wizard. Without credentials the wizard must run so
// the user can sign in.
func (mcpInjector) Inject(rootfsPath, gatewayIP string, port uint16, chown agent.ChownFunc) error {
	servers := map[string]claudeCodeServer{
		"sandbox-tools": {
			Type: "http",
			URL:  fmt.Sprintf("http://%s:%d%s", gatewayIP, port, config.MCPEndpointPath),
		},
	}

	homeDir := filepath.Join(rootfsPath, configio.SandboxHome)
	if err := configio.MkdirAndChown(homeDir, chown); err != nil {
		return err
	}

	// Deep-merge to preserve user MCP servers injected by settings hook.
	serversMap := make(map[string]any, len(servers))
	for k, v := range servers {
		serversMap[k] = v
	}
	if err := configio.MergeJSONMapEntries(homeDir, ".claude.json", "mcpServers", serversMap, chown); err != nil {
		return err
	}

	// Only skip the onboarding wizard when credentials are available.
	// The credential injection hook runs before this hook, so the file
	// will be present if the store had saved credentials to inject.
	credFile := filepath.Join(homeDir, ".claude", ".credentials.json")
	if _, err := os.Stat(credFile); err == nil {
		slog.Debug("credentials found in rootfs, setting hasCompletedOnboarding")
		return configio.MergeJSONKey(homeDir, ".claude.json", "hasCompletedOnboarding", true, chown)
	}

	slog.Debug("no credentials in rootfs, leaving onboarding wizard enabled")
	return nil
}
