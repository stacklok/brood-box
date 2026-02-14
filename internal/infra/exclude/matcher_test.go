// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package exclude

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatcher_SecurityPatternsNonOverridable(t *testing.T) {
	t.Parallel()

	// User tries to negate a security pattern — security tier still wins.
	userPatterns := []string{"!.env*"}
	securityPatterns := []string{".env*"}

	m := NewMatcher(userPatterns, securityPatterns)

	assert.True(t, m.Match(".env"), "security pattern should exclude .env")
	assert.True(t, m.Match(".env.local"), "security pattern should exclude .env.local")
}

func TestMatcher_PerformancePatternsOverridable(t *testing.T) {
	t.Parallel()

	// User negates a performance pattern — it should be un-excluded.
	userPatterns := []string{"node_modules/", "!node_modules/"}
	securityPatterns := []string{".env*"}

	m := NewMatcher(userPatterns, securityPatterns)

	assert.False(t, m.Match("node_modules/package.json"), "negated performance pattern should not exclude")
}

func TestMatcher_TableDriven(t *testing.T) {
	t.Parallel()

	userPatterns := []string{
		"node_modules/",
		"vendor/",
		"*.log",
		"build/",
	}
	securityPatterns := []string{
		".env*",
		"*.pem",
		"*.key",
		".ssh/",
	}

	m := NewMatcher(userPatterns, securityPatterns)

	tests := []struct {
		name    string
		path    string
		matched bool
	}{
		{"exact security match", ".env", true},
		{"security glob match", ".env.local", true},
		{"security extension match", "server.pem", true},
		{"security key match", "private.key", true},
		{"security directory match", ".ssh/id_rsa", true},
		{"performance directory match", "node_modules/foo/bar.js", true},
		{"performance vendor match", "vendor/github.com/foo", true},
		{"user glob match", "app.log", true},
		{"user directory match", "build/output.js", true},
		{"no match regular file", "main.go", false},
		{"no match nested file", "src/app/main.go", false},
		{"security glob catches similar name", ".environment", true},
		{"double star in path", "deep/nested/app.log", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.matched, m.Match(tt.path), "path: %s", tt.path)
		})
	}
}

func TestMatcher_EmptyPatterns(t *testing.T) {
	t.Parallel()

	m := NewMatcher(nil, nil)
	assert.False(t, m.Match("anything.go"))
}
