// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// sandboxHome is the home directory of the sandbox user inside the guest.
// Config files are written here because /workspace is mounted via virtiofs
// and would shadow anything written into the rootfs at that path.
const sandboxHome = "home/sandbox"

// --- Claude Code ---
// Ref: https://code.claude.com/docs/en/mcp
// User-scope MCP servers live in ~/.claude.json under the top-level
// "mcpServers" key, available across all projects.

// claudeCodeConfig is the top-level ~/.claude.json structure.
type claudeCodeConfig struct {
	MCPServers map[string]claudeCodeServer `json:"mcpServers"`
}

// claudeCodeServer describes a single MCP server entry for Claude Code.
type claudeCodeServer struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

// injectClaudeCodeMCP writes ~/.claude.json with a user-scope MCP server
// so Claude Code discovers the vmcp endpoint in every project.
func injectClaudeCodeMCP(rootfsPath, gatewayIP string, port uint16) error {
	cfg := claudeCodeConfig{
		MCPServers: map[string]claudeCodeServer{
			"sandbox-tools": {
				Type: "http",
				URL:  fmt.Sprintf("http://%s:%d/mcp", gatewayIP, port),
			},
		},
	}

	return writeJSONToHome(rootfsPath, ".claude.json", cfg)
}

// --- Codex ---
// Ref: https://developers.openai.com/codex/config-reference/
// Global config lives at ~/.codex/config.toml (TOML, not JSON).

// injectCodexMCP writes ~/.codex/config.toml with an MCP server section.
func injectCodexMCP(rootfsPath, gatewayIP string, port uint16) error {
	mcpURL := fmt.Sprintf("http://%s:%d/mcp", gatewayIP, port)

	// Codex uses TOML — no encoder dep needed for two lines.
	tomlContent := fmt.Sprintf("[mcp_servers.sandbox-tools]\nurl = %q\n", mcpURL)

	codexDir := filepath.Join(rootfsPath, sandboxHome, ".codex")
	if err := mkdirAndChown(codexDir); err != nil {
		return fmt.Errorf("creating ~/.codex dir: %w", err)
	}

	configPath := filepath.Join(codexDir, "config.toml")
	if err := os.WriteFile(configPath, []byte(tomlContent), 0o644); err != nil {
		return fmt.Errorf("writing codex MCP config: %w", err)
	}

	return os.Chown(configPath, sandboxUID, sandboxGID)
}

// --- OpenCode ---
// Ref: https://opencode.ai/docs/mcp-servers/
// Global config lives at ~/.config/opencode/opencode.json.

// openCodeConfig is the top-level opencode.json structure.
type openCodeConfig struct {
	MCP map[string]openCodeServer `json:"mcp"`
}

// openCodeServer describes a single MCP server entry for OpenCode.
type openCodeServer struct {
	Type    string `json:"type"`
	URL     string `json:"url"`
	Enabled bool   `json:"enabled"`
}

// injectOpenCodeMCP writes ~/.config/opencode/opencode.json with the vmcp
// server configured as a remote MCP endpoint.
func injectOpenCodeMCP(rootfsPath, gatewayIP string, port uint16) error {
	cfg := openCodeConfig{
		MCP: map[string]openCodeServer{
			"sandbox-tools": {
				Type:    "remote",
				URL:     fmt.Sprintf("http://%s:%d/mcp", gatewayIP, port),
				Enabled: true,
			},
		},
	}

	opencodeDir := filepath.Join(rootfsPath, sandboxHome, ".config", "opencode")
	if err := mkdirAndChown(opencodeDir); err != nil {
		return fmt.Errorf("creating ~/.config/opencode dir: %w", err)
	}

	return writeJSONToDir(opencodeDir, "opencode.json", cfg)
}

// --- helpers ---

const (
	sandboxUID = 1000
	sandboxGID = 1000
)

// writeJSONToHome marshals v as indented JSON and writes it to
// ~sandbox/<filename>, chowned to the sandbox user.
func writeJSONToHome(rootfsPath, filename string, v any) error {
	homeDir := filepath.Join(rootfsPath, sandboxHome)
	// home dir already exists from image, but ensure it.
	if err := mkdirAndChown(homeDir); err != nil {
		return err
	}

	return writeJSONToDir(homeDir, filename, v)
}

// writeJSONToDir marshals v and writes it to dir/filename, chowned to sandbox.
func writeJSONToDir(dir, filename string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling JSON for %s: %w", filename, err)
	}

	path := filepath.Join(dir, filename)
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", filename, err)
	}

	return os.Chown(path, sandboxUID, sandboxGID)
}

// mkdirAndChown creates a directory tree and chowns every created component
// to the sandbox user.
func mkdirAndChown(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.Chown(dir, sandboxUID, sandboxGID)
}
