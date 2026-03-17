// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)

// ChownFunc abstracts file ownership changes for testability.
// Production code uses bestEffortLchown; tests can pass a recording mock.
type ChownFunc func(path string, uid, gid int) error

// bestEffortLchown attempts os.Lchown and silently ignores permission errors.
// On macOS non-root users cannot chown to a different UID; the guest init
// will fix ownership at boot time. Lchown is used instead of Chown to avoid
// following symlinks in the rootfs.
func bestEffortLchown(path string, uid, gid int) error {
	if err := os.Lchown(path, uid, gid); err != nil {
		if !os.IsPermission(err) {
			slog.Warn("lchown failed", "path", path, "uid", uid, "gid", gid, "err", err)
		}
	}
	return nil
}

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

// injectClaudeCodeMCP merges an MCP server entry into ~/.claude.json,
// preserving any pre-existing keys (auth tokens, onboarding flags, etc.).
// If credentials were already injected into the rootfs (by the credential
// hook that runs earlier), it also sets hasCompletedOnboarding so Claude
// Code skips the interactive setup wizard. Without credentials the wizard
// must run so the user can sign in.
func injectClaudeCodeMCP(rootfsPath, gatewayIP string, port uint16, chown ChownFunc) error {
	servers := map[string]claudeCodeServer{
		"sandbox-tools": {
			Type: "http",
			URL:  fmt.Sprintf("http://%s:%d/mcp", gatewayIP, port),
		},
	}

	homeDir := filepath.Join(rootfsPath, sandboxHome)
	if err := mkdirAndChown(homeDir, chown); err != nil {
		return err
	}

	// Deep-merge to preserve user MCP servers injected by settings hook.
	serversMap := make(map[string]any, len(servers))
	for k, v := range servers {
		serversMap[k] = v
	}
	if err := mergeJSONMapEntries(homeDir, ".claude.json", "mcpServers", serversMap, chown); err != nil {
		return err
	}

	// Only skip the onboarding wizard when credentials are available.
	// The credential injection hook runs before this hook, so the file
	// will be present if the store had saved credentials to inject.
	credFile := filepath.Join(homeDir, ".claude", ".credentials.json")
	if _, err := os.Stat(credFile); err == nil {
		slog.Debug("credentials found in rootfs, setting hasCompletedOnboarding")
		return mergeJSONKey(homeDir, ".claude.json", "hasCompletedOnboarding", true, chown)
	}

	slog.Debug("no credentials in rootfs, leaving onboarding wizard enabled")
	return nil
}

// --- Codex ---
// Ref: https://developers.openai.com/codex/config-reference/
// Global config lives at ~/.codex/config.toml (TOML, not JSON).

// injectCodexMCP merges an MCP server entry into ~/.codex/config.toml,
// preserving any pre-existing TOML sections.
func injectCodexMCP(rootfsPath, gatewayIP string, port uint16, chown ChownFunc) error {
	mcpURL := fmt.Sprintf("http://%s:%d/mcp", gatewayIP, port)

	codexDir := filepath.Join(rootfsPath, sandboxHome, ".codex")
	if err := mkdirAndChown(codexDir, chown); err != nil {
		return fmt.Errorf("creating ~/.codex dir: %w", err)
	}

	return mergeTOMLMapEntries(codexDir, "config.toml", "mcp_servers", map[string]any{
		"sandbox-tools": map[string]any{
			"url": mcpURL,
		},
	}, chown)
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

// injectOpenCodeMCP merges an MCP server entry into ~/.config/opencode/opencode.json,
// preserving any pre-existing keys.
func injectOpenCodeMCP(rootfsPath, gatewayIP string, port uint16, chown ChownFunc) error {
	servers := map[string]openCodeServer{
		"sandbox-tools": {
			Type:    "remote",
			URL:     fmt.Sprintf("http://%s:%d/mcp", gatewayIP, port),
			Enabled: true,
		},
	}

	opencodeDir := filepath.Join(rootfsPath, sandboxHome, ".config", "opencode")
	if err := mkdirAndChown(opencodeDir, chown); err != nil {
		return fmt.Errorf("creating ~/.config/opencode dir: %w", err)
	}

	// Deep-merge to preserve user MCP servers injected by settings hook.
	serversMap := make(map[string]any, len(servers))
	for k, v := range servers {
		serversMap[k] = v
	}
	return mergeJSONMapEntries(opencodeDir, "opencode.json", "mcp", serversMap, chown)
}

// --- helpers ---

const (
	sandboxUID = 1000
	sandboxGID = 1000
)

// mergeJSONKey reads an existing JSON file (if any) at dir/filename, sets
// the given top-level key to value, and writes the result back. If the file
// does not exist, a new object with just {key: value} is created.
func mergeJSONKey(dir, filename, key string, value any, chown ChownFunc) error {
	path := filepath.Join(dir, filename)

	existing := make(map[string]json.RawMessage)
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("parsing existing %s: %w", filename, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading existing %s: %w", filename, err)
	}

	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshaling %s value for %s: %w", key, filename, err)
	}
	existing[key] = raw

	merged, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling merged %s: %w", filename, err)
	}

	if err := os.WriteFile(path, append(merged, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", filename, err)
	}

	return chown(path, sandboxUID, sandboxGID)
}

// mergeJSONMapEntries reads an existing JSON file, merges entries from value
// into the map at key, and writes back. Individual map entries from value
// override existing entries with the same name, but other entries are preserved.
// This is a single-level merge: each map entry is replaced atomically (not
// recursively). This is correct for MCP server maps where each server entry
// is an independent unit.
func mergeJSONMapEntries(dir, filename, key string, value map[string]any, chown ChownFunc) error {
	path := filepath.Join(dir, filename)

	existing := make(map[string]json.RawMessage)
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("parsing existing %s: %w", filename, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading existing %s: %w", filename, err)
	}

	// Parse existing map at key (if any).
	existingMap := make(map[string]any)
	if raw, ok := existing[key]; ok {
		if err := json.Unmarshal(raw, &existingMap); err != nil {
			// Not a map — replace entirely.
			existingMap = make(map[string]any)
		}
	}

	// Merge new entries into existing map.
	for k, v := range value {
		existingMap[k] = v
	}

	raw, err := json.Marshal(existingMap)
	if err != nil {
		return fmt.Errorf("marshaling %s value for %s: %w", key, filename, err)
	}
	existing[key] = raw

	merged, err := json.MarshalIndent(existing, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling merged %s: %w", filename, err)
	}

	if err := os.WriteFile(path, append(merged, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", filename, err)
	}

	return chown(path, sandboxUID, sandboxGID)
}

// mergeTOMLMapEntries reads an existing TOML file, merges entries from value
// into the table at key, and writes back. Individual entries from value
// override existing entries with the same name, but other entries are preserved.
// This is a single-level merge (see mergeJSONMapEntries for rationale).
func mergeTOMLMapEntries(dir, filename, key string, value map[string]any, chown ChownFunc) error {
	path := filepath.Join(dir, filename)

	existing := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		if err := toml.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("parsing existing %s: %w", filename, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading existing %s: %w", filename, err)
	}

	// Parse existing map at key (if any).
	existingMap := make(map[string]any)
	if raw, ok := existing[key]; ok {
		if m, ok := raw.(map[string]any); ok {
			existingMap = m
		}
	}

	// Merge new entries into existing map.
	for k, v := range value {
		existingMap[k] = v
	}
	existing[key] = existingMap

	merged, err := toml.Marshal(existing)
	if err != nil {
		return fmt.Errorf("marshaling merged %s: %w", filename, err)
	}

	if err := os.WriteFile(path, merged, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", filename, err)
	}

	return chown(path, sandboxUID, sandboxGID)
}

// mkdirAndChown creates a directory tree and chowns every created component
// to the sandbox user.
func mkdirAndChown(dir string, chown ChownFunc) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return chown(dir, sandboxUID, sandboxGID)
}
