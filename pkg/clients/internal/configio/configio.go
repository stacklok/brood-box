// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package configio provides shared JSON / TOML / YAML merge helpers used by
// the per-client MCP config injectors under pkg/clients/. It is internal to
// the clients tree so only client packages can import it.
package configio

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"

	"github.com/stacklok/brood-box/pkg/domain/agent"
)

// SandboxHome is the home directory of the sandbox user inside the guest.
// Config files are written here because /workspace is mounted via virtiofs
// and would shadow anything written into the rootfs at that path.
const SandboxHome = "home/sandbox"

// Sandbox uid/gid used to chown injected files. Kept in sync with the guest
// init code in go-microvm's guest/ tree.
const (
	SandboxUID = 1000
	SandboxGID = 1000
)

// BestEffortLchown attempts os.Lchown and silently ignores permission errors.
// On macOS non-root users cannot chown to a different UID; the guest init
// will fix ownership at boot time. Lchown is used instead of Chown to avoid
// following symlinks in the rootfs.
func BestEffortLchown(path string, uid, gid int) error {
	if err := os.Lchown(path, uid, gid); err != nil {
		if !os.IsPermission(err) {
			slog.Warn("lchown failed", "path", path, "uid", uid, "gid", gid, "err", err)
		}
	}
	return nil
}

// MkdirAndChown creates a directory tree and chowns it to the sandbox user.
func MkdirAndChown(dir string, chown agent.ChownFunc) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return chown(dir, SandboxUID, SandboxGID)
}

// MergeJSONKey reads an existing JSON file (if any) at dir/filename, sets
// the given top-level key to value, and writes the result back. If the file
// does not exist, a new object with just {key: value} is created.
func MergeJSONKey(dir, filename, key string, value any, chown agent.ChownFunc) error {
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

	return chown(path, SandboxUID, SandboxGID)
}

// MergeJSONMapEntries reads an existing JSON file, merges entries from value
// into the map at key, and writes back. Individual map entries from value
// override existing entries with the same name, but other entries are
// preserved. This is a single-level merge: each map entry is replaced
// atomically (not recursively). This is correct for MCP server maps where
// each server entry is an independent unit.
func MergeJSONMapEntries(dir, filename, key string, value map[string]any, chown agent.ChownFunc) error {
	path := filepath.Join(dir, filename)

	existing := make(map[string]json.RawMessage)
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("parsing existing %s: %w", filename, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading existing %s: %w", filename, err)
	}

	existingMap := make(map[string]any)
	if raw, ok := existing[key]; ok {
		if err := json.Unmarshal(raw, &existingMap); err != nil {
			existingMap = make(map[string]any)
		}
	}

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

	return chown(path, SandboxUID, SandboxGID)
}

// MergeTOMLMapEntries reads an existing TOML file, merges entries from value
// into the table at key, and writes back. Single-level merge — see
// MergeJSONMapEntries for rationale.
func MergeTOMLMapEntries(dir, filename, key string, value map[string]any, chown agent.ChownFunc) error {
	path := filepath.Join(dir, filename)

	existing := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		if err := toml.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("parsing existing %s: %w", filename, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading existing %s: %w", filename, err)
	}

	existingMap := make(map[string]any)
	if raw, ok := existing[key]; ok {
		if m, ok := raw.(map[string]any); ok {
			existingMap = m
		}
	}

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

	return chown(path, SandboxUID, SandboxGID)
}

// MergeYAMLMapEntries reads an existing YAML file, merges entries from value
// into the map at key, and writes back. Single-level merge — see
// MergeJSONMapEntries for rationale.
func MergeYAMLMapEntries(dir, filename, key string, value map[string]any, chown agent.ChownFunc) error {
	path := filepath.Join(dir, filename)

	existing := make(map[string]any)
	if data, err := os.ReadFile(path); err == nil {
		if len(data) > 0 {
			if err := yaml.Unmarshal(data, &existing); err != nil {
				return fmt.Errorf("parsing existing %s: %w", filename, err)
			}
			// An empty YAML document unmarshals to nil, not an empty map.
			if existing == nil {
				existing = make(map[string]any)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading existing %s: %w", filename, err)
	}

	existingMap := make(map[string]any)
	if raw, ok := existing[key]; ok {
		if m, ok := raw.(map[string]any); ok {
			existingMap = m
		}
	}

	for k, v := range value {
		existingMap[k] = v
	}
	existing[key] = existingMap

	merged, err := yaml.Marshal(existing)
	if err != nil {
		return fmt.Errorf("marshaling merged %s: %w", filename, err)
	}

	if err := os.WriteFile(path, merged, 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", filename, err)
	}

	return chown(path, SandboxUID, SandboxGID)
}
