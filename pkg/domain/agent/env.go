// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"fmt"
	"strings"
)

// ValidateEnvForwardPatterns rejects env-forward patterns that would
// match every (or no) host environment variable. Specifically:
//
//   - An empty / whitespace-only pattern matches the empty string key,
//     which is never a real env var but is also clearly a typo.
//   - A bare "*" (or whitespace-trimmed "*") matches every key because
//     its trimmed prefix is the empty string — this is a blanket
//     "forward everything" that lets an untrusted workspace config
//     or a footgun CLI flag scoop up secrets like SSH_AUTH_SOCK,
//     GITHUB_TOKEN, AWS_*, etc., without the operator knowing.
//   - A leading-star pattern like "*_API_KEY" is silently useless
//     (only trailing-star globs are honored by matchPattern). Reject
//     it with a clear error so users learn the correct syntax.
//
// Explicit exact names ("HOME") and trailing-star globs with a
// non-empty prefix ("AWS_*", "CARGO_*") are always accepted.
//
// Callers should apply this to every user-supplied source:
// global config, workspace-local config, and CLI flags. The matcher
// also defensively skips empty/bare-"*" patterns at evaluation time.
func ValidateEnvForwardPatterns(patterns []string) error {
	for i, p := range patterns {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			return fmt.Errorf("env_forward[%d]: empty pattern — specify an exact name or a prefix like \"AWS_*\"", i)
		}
		if trimmed == "*" {
			return fmt.Errorf("env_forward[%d]: bare \"*\" matches every host env var — specify an exact name or a prefix like \"AWS_*\"", i)
		}
		if strings.HasPrefix(trimmed, "*") {
			return fmt.Errorf("env_forward[%d]: leading-star patterns are not supported (%q) — only trailing-star globs like \"AWS_*\"", i, trimmed)
		}
	}
	return nil
}

// EnvProvider abstracts access to environment variables for testability.
type EnvProvider interface {
	// Environ returns the environment as a slice of "KEY=VALUE" strings.
	Environ() []string
}

// OSEnvProvider implements EnvProvider using the real OS environment.
type OSEnvProvider struct {
	environFunc func() []string
}

// NewOSEnvProvider creates an EnvProvider backed by the given function.
// Pass os.Environ for production use.
func NewOSEnvProvider(fn func() []string) *OSEnvProvider {
	return &OSEnvProvider{environFunc: fn}
}

// Environ returns the OS environment variables.
func (p *OSEnvProvider) Environ() []string {
	return p.environFunc()
}

// ForwardEnv collects environment variables matching the given patterns
// from the provider. Patterns support exact match ("FOO") and glob
// suffix ("FOO_*" matches "FOO_BAR", "FOO_BAZ", etc.).
//
// Returns a map of variable name to value.
func ForwardEnv(patterns []string, provider EnvProvider) map[string]string {
	if len(patterns) == 0 {
		return nil
	}

	env := provider.Environ()
	result := make(map[string]string)

	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if matchesAny(key, patterns) {
			result[key] = value
		}
	}

	if len(result) == 0 {
		return nil
	}
	return result
}

// matchesAny returns true if the key matches any of the patterns.
func matchesAny(key string, patterns []string) bool {
	for _, p := range patterns {
		if matchPattern(key, p) {
			return true
		}
	}
	return false
}

// matchPattern checks if key matches the pattern.
// Supports exact match and glob suffix (e.g., "CLAUDE_*").
//
// Defense-in-depth: an empty pattern or a bare "*" (after whitespace
// trimming) is never a match — callers should reject these via
// ValidateEnvForwardPatterns at load time, but this guard makes sure a
// bypass cannot silently forward every host env var.
func matchPattern(key, pattern string) bool {
	trimmed := strings.TrimSpace(pattern)
	if trimmed == "" || trimmed == "*" {
		return false
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		if prefix == "" {
			return false
		}
		return strings.HasPrefix(key, prefix)
	}
	return key == pattern
}

// ShellEscape wraps a value in single quotes for safe use in shell commands.
// Single quotes within the value are escaped as '\” (end quote, escaped quote, start quote).
func ShellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
