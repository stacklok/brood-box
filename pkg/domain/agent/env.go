// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"strings"
)

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
func matchPattern(key, pattern string) bool {
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(key, prefix)
	}
	return key == pattern
}

// ShellEscape wraps a value in single quotes for safe use in shell commands.
// Single quotes within the value are escaped as '\” (end quote, escaped quote, start quote).
func ShellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
