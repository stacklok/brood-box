// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	domaincredential "github.com/stacklok/brood-box/pkg/domain/credential"
)

// claudeCodeCredPath is the relative path to the Claude Code credentials file
// within the sandbox user's home directory.
const claudeCodeCredPath = ".claude/.credentials.json"

// keychainService is the macOS Keychain service name used by Claude Code.
const keychainService = "Claude Code-credentials"

// ClaudeCodeSeeder seeds Claude Code OAuth credentials from the host into the
// credential store. It reads from the macOS Keychain (preferred) or the host's
// ~/.claude/.credentials.json and writes to the store when:
//   - No credentials exist yet (first run), or
//   - The stored access token has expired and the host has a fresher one.
type ClaudeCodeSeeder struct {
	logger    *slog.Logger
	readHost  func() ([]byte, string, error) // reads host credentials
	nowMs     func() int64                   // current time in epoch ms
}

// NewClaudeCodeSeeder creates a new ClaudeCodeSeeder with production defaults.
func NewClaudeCodeSeeder(logger *slog.Logger) *ClaudeCodeSeeder {
	return &ClaudeCodeSeeder{
		logger:   logger,
		readHost: readHostClaudeCredentials,
		nowMs:    func() int64 { return time.Now().UnixMilli() },
	}
}

// Seed implements credential.Seeder. It ensures the file store has a
// fresh OAuth token for Claude Code by comparing host and stored expiry
// timestamps. Returns nil when no host credentials are available (not an error).
func (s *ClaudeCodeSeeder) Seed(store domaincredential.FileStore) error {
	const agentName = "claude-code"

	// Read host credentials first — if unavailable, nothing to do.
	hostCreds, source, err := s.readHost()
	if err != nil {
		s.logger.Debug("no host Claude Code credentials found", "error", err)
		return nil
	}

	storedData, readErr := store.ReadFile(agentName, claudeCodeCredPath)

	// Distinguish "not found" (first run) from real errors (permissions, etc).
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return fmt.Errorf("reading stored credentials: %w", readErr)
	}

	// Stored credentials exist — check expiry before overwriting.
	if readErr == nil {
		storedExp := extractExpiresAt(storedData)
		hostExp := extractExpiresAt(hostCreds)
		if storedExp > 0 && storedExp > s.nowMs() {
			s.logger.Debug("stored credentials still valid, skipping seed",
				"expires_at", storedExp)
			return nil
		}
		// Stored token expired — check if host has a fresher one.
		if hostExp <= storedExp {
			s.logger.Debug("host credentials not fresher than stored, skipping seed",
				"stored_expires", storedExp, "host_expires", hostExp)
			return nil
		}
		s.logger.Info("stored credentials expired, refreshing from host",
			"source", source)
		if err := store.OverwriteFile(agentName, claudeCodeCredPath, hostCreds); err != nil {
			return fmt.Errorf("overwriting Claude Code credentials: %w", err)
		}
		s.logger.Info("seeded Claude Code credentials from host", "source", source)
		return nil
	}

	// No stored credentials — initial seed.
	if err := store.SeedFile(agentName, claudeCodeCredPath, hostCreds); err != nil {
		return fmt.Errorf("seeding Claude Code credentials: %w", err)
	}
	s.logger.Info("seeded Claude Code credentials from host", "source", source)
	return nil
}

// extractExpiresAt parses the expiresAt field from Claude Code credentials JSON.
// Returns 0 if parsing fails.
func extractExpiresAt(data []byte) int64 {
	var wrapper struct {
		ClaudeAiOauth struct {
			ExpiresAt int64 `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &wrapper); err != nil {
		return 0
	}
	return wrapper.ClaudeAiOauth.ExpiresAt
}

// readHostClaudeCredentials attempts to read Claude Code credentials from the
// host system. Returns the raw JSON content, the source description, and any error.
// On macOS, tries the Keychain first, then falls back to the credentials file.
// On other platforms, only checks the credentials file.
func readHostClaudeCredentials() ([]byte, string, error) {
	if runtime.GOOS == "darwin" {
		creds, err := readKeychainCredentials()
		if err == nil {
			return creds, "macOS Keychain", nil
		}
		slog.Debug("keychain read failed, falling back to credentials file", "error", err)
	}

	// Fall back to credentials file.
	creds, err := readCredentialsFile()
	if err != nil {
		return nil, "", fmt.Errorf("no host credentials found: %w", err)
	}
	return creds, "~/.claude/.credentials.json", nil
}

// readKeychainCredentials reads Claude Code credentials from the macOS Keychain.
func readKeychainCredentials() ([]byte, error) {
	//nolint:gosec // Arguments are constant strings, not user input.
	out, err := exec.Command("security", "find-generic-password", "-s", keychainService, "-w").Output()
	if err != nil {
		return nil, fmt.Errorf("keychain lookup failed: %w", err)
	}

	// The macOS security command appends a trailing newline — trim it.
	out = bytes.TrimSpace(out)

	if len(out) > int(domaincredential.MaxFileSize) {
		return nil, fmt.Errorf("keychain credential exceeds max size (%d bytes)", domaincredential.MaxFileSize)
	}

	// Validate it's valid JSON before using it.
	if !json.Valid(out) {
		return nil, fmt.Errorf("keychain entry is not valid JSON")
	}

	return out, nil
}

// readCredentialsFile reads Claude Code credentials from ~/.claude/.credentials.json.
func readCredentialsFile() ([]byte, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolving home dir: %w", err)
	}

	path := filepath.Join(home, ".claude", ".credentials.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading credentials file: %w", err)
	}

	if len(data) > int(domaincredential.MaxFileSize) {
		return nil, fmt.Errorf("credentials file exceeds max size (%d bytes)", domaincredential.MaxFileSize)
	}

	if !json.Valid(data) {
		return nil, fmt.Errorf("credentials file is not valid JSON")
	}

	return data, nil
}
