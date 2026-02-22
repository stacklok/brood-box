// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package git provides infrastructure for sanitizing git configuration
// files before they are exposed inside sandbox VMs.
package git

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/stacklok/apiary/pkg/domain/workspace"
)

// Ensure ConfigSanitizer implements workspace.SnapshotPostProcessor at compile time.
var _ workspace.SnapshotPostProcessor = (*ConfigSanitizer)(nil)

// allowedSections is the set of git config section names that are safe to
// pass through to the sandbox. Any section not in this list is stripped.
var allowedSections = map[string]bool{
	"core": true, "remote": true, "branch": true, "user": true,
	"merge": true, "diff": true, "fetch": true, "push": true,
	"submodule": true, "color": true, "log": true, "rerere": true,
	"rebase": true, "tag": true, "pack": true, "gc": true,
	"lfs": true, "status": true, "advice": true, "init": true,
}

// ConfigSanitizer reads .git/config from the original workspace,
// sanitizes it by stripping dangerous sections and credentials,
// and writes the sanitized version into the snapshot workspace.
type ConfigSanitizer struct {
	logger *slog.Logger
}

// NewConfigSanitizer creates a new ConfigSanitizer.
func NewConfigSanitizer(logger *slog.Logger) *ConfigSanitizer {
	return &ConfigSanitizer{logger: logger}
}

// Process reads .git/config from originalPath, sanitizes it, and writes
// the result into snapshotPath/.git/config.
func (s *ConfigSanitizer) Process(_ context.Context, originalPath, snapshotPath string) error {
	srcPath := filepath.Join(originalPath, ".git", "config")

	data, err := os.ReadFile(srcPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reading git config: %w", err)
	}

	sanitized := SanitizeConfig(string(data))

	dstDir := filepath.Join(snapshotPath, ".git")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("creating .git directory in snapshot: %w", err)
	}

	dstPath := filepath.Join(dstDir, "config")
	if err := os.WriteFile(dstPath, []byte(sanitized), 0o644); err != nil {
		return fmt.Errorf("writing sanitized git config: %w", err)
	}

	return nil
}

// SanitizeConfig parses a git config file and returns a sanitized version
// with dangerous sections, credentials, and sensitive keys stripped.
func SanitizeConfig(input string) string {
	if input == "" {
		return ""
	}

	// Normalize Windows line endings before processing.
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")

	lines := strings.Split(input, "\n")
	var out []string

	var currentSection string
	inAllowedSection := false
	inContinuation := false

	for _, line := range lines {
		trimmedRight := strings.TrimRight(line, " \t")
		continuation := strings.HasSuffix(trimmedRight, `\`)

		// Handle continuation lines: they inherit the allow/block state
		// of the line that started the value.
		if inContinuation {
			if inAllowedSection {
				out = append(out, line)
			}
			inContinuation = continuation
			continue
		}

		trimmed := strings.TrimSpace(line)

		// Check for section header.
		if strings.HasPrefix(trimmed, "[") {
			section := parseSectionName(line)
			currentSection = section
			inAllowedSection = allowedSections[section]

			if !inAllowedSection {
				// Known dangerous or unknown section — skip.
				inContinuation = continuation
				continue
			}

			out = append(out, line)
			inContinuation = continuation
			continue
		}

		// Non-section-header line (key=value, comment, or blank).
		if !inAllowedSection {
			inContinuation = continuation
			continue
		}

		// Apply key-level filtering within allowed sections.
		if kept, rewritten := filterLine(currentSection, line); kept {
			out = append(out, rewritten)
		}

		inContinuation = continuation
	}

	return strings.Join(out, "\n")
}

// filterLine decides whether a key-value line within an allowed section
// should be included, and optionally rewrites it. Returns (keep, line).
func filterLine(section, line string) (bool, string) {
	key := extractKey(line)

	// Comments and blank lines always pass through in allowed sections.
	if key == "" {
		return true, line
	}

	lowerKey := strings.ToLower(key)

	switch section {
	case "user":
		// Only name and email are safe; signingkey etc. are stripped.
		if lowerKey == "name" || lowerKey == "email" {
			return true, line
		}
		return false, ""

	case "remote", "submodule":
		// URL and pushurl values need credential sanitization.
		if lowerKey == "url" || lowerKey == "pushurl" {
			rewritten := rewriteURLLine(line)
			if rewritten == "" {
				return false, ""
			}
			return true, rewritten
		}
		return true, line

	default:
		return true, line
	}
}

// extractKey returns the key name from a git config line.
// Returns "" for comments and blank lines.
func extractKey(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || trimmed[0] == '#' || trimmed[0] == ';' {
		return ""
	}
	if idx := strings.IndexByte(trimmed, '='); idx >= 0 {
		return strings.TrimSpace(trimmed[:idx])
	}
	// Bare boolean key (no = sign).
	return strings.TrimSpace(trimmed)
}

// parseSectionName extracts the lowercased section name from a header line
// like `[remote "origin"]`, `[core]`, or `[includeIf "gitdir:/path"]`.
func parseSectionName(line string) string {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 2 || trimmed[0] != '[' {
		return ""
	}

	// Skip the opening bracket.
	inner := trimmed[1:]

	// Find where the section name ends: at space, quote, dot, or closing bracket.
	end := len(inner)
	for i, ch := range inner {
		if ch == ' ' || ch == '"' || ch == '.' || ch == ']' {
			end = i
			break
		}
	}

	return strings.ToLower(inner[:end])
}

// rewriteURLLine sanitizes the URL value in a git config key=value line.
// Returns the rewritten line, or "" if the URL should be dropped entirely.
func rewriteURLLine(line string) string {
	idx := strings.IndexByte(line, '=')
	if idx < 0 {
		return line
	}

	prefix := line[:idx+1]
	value := line[idx+1:]

	trimmedValue := strings.TrimSpace(value)
	sanitized := sanitizeURL(trimmedValue)

	// If sanitized is empty (fail-closed), drop the line entirely.
	if sanitized == "" {
		return ""
	}

	return prefix + " " + sanitized
}

// ContainsCredentials checks whether a git config file at the given path
// contains embedded credentials (userinfo in URLs or [credential] sections).
// Returns false, nil for nonexistent files.
func ContainsCredentials(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("reading git config: %w", err)
	}
	return containsCredentials(string(data)), nil
}

// containsCredentials scans git config content for embedded credentials.
// It detects [credential] section headers, [url] sections with credential
// URLs in subsection strings, and URLs with password components in values.
func containsCredentials(input string) bool {
	if input == "" {
		return false
	}

	// Normalize Windows line endings before processing.
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")

	lines := strings.Split(input, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip comments and blank lines.
		if trimmed == "" || trimmed[0] == '#' || trimmed[0] == ';' {
			continue
		}

		// Check section headers.
		if strings.HasPrefix(trimmed, "[") {
			section := parseSectionName(line)
			if section == "credential" {
				return true
			}
			// [url "https://user:token@host/"] insteadOf patterns.
			if section == "url" {
				if sub := parseSubsection(line); sub != "" && urlHasCredentials(sub) {
					return true
				}
			}
			continue
		}

		// Check key=value lines for URLs with embedded passwords.
		if idx := strings.IndexByte(trimmed, '='); idx >= 0 {
			value := strings.TrimSpace(trimmed[idx+1:])
			if urlHasCredentials(value) {
				return true
			}
		}
	}
	return false
}

// urlHasCredentials checks whether a URL string contains a password component.
// Returns false for username-only URLs (e.g. ssh://git@host) since those use
// key-based auth, not embedded secrets. Fails closed: malformed URLs that look
// like they contain credentials are flagged.
func urlHasCredentials(value string) bool {
	u, err := url.Parse(value)
	if err == nil && u.Scheme != "" {
		if u.User != nil {
			_, hasPassword := u.User.Password()
			return hasPassword
		}
		return false
	}

	// Parse failed or no scheme — heuristic check mirroring sanitizeURL's
	// fail-closed logic: scheme + @ + colon suggests embedded credentials.
	hasScheme := strings.Contains(value, "://")
	hasAt := strings.Contains(value, "@")
	return hasScheme && hasAt
}

// parseSubsection extracts the quoted subsection value from a git config
// section header like `[url "https://github.com/"]`. Returns "" if no
// quoted subsection is present.
func parseSubsection(line string) string {
	start := strings.IndexByte(line, '"')
	if start < 0 {
		return ""
	}
	end := strings.LastIndexByte(line, '"')
	if end <= start {
		return ""
	}
	return line[start+1 : end]
}

// sanitizeURL strips embedded credentials from a URL value.
func sanitizeURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return trimmed
	}

	u, err := url.Parse(trimmed)
	if err == nil && u.Scheme != "" {
		if u.User != nil {
			u.User = nil
			return u.String()
		}
		return trimmed
	}

	// Parse failed or no scheme — check for SCP-style git URLs.
	hasAt := strings.Contains(trimmed, "@")
	hasScheme := strings.Contains(trimmed, "://")

	if hasAt && !hasScheme {
		// SCP-style URL like git@github.com:org/repo.git — safe (SSH key auth).
		return trimmed
	}

	if hasScheme && hasAt {
		// Looks like scheme://user:pass@host but failed to parse — fail closed.
		return ""
	}

	return trimmed
}
