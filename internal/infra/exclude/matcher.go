// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package exclude provides gitignore-compatible pattern matching for workspace
// snapshot exclusion.
package exclude

import (
	ignore "github.com/sabhiram/go-gitignore"
)

// Matcher decides whether a relative path should be excluded from the snapshot.
type Matcher interface {
	// Match returns true if the given relative path should be excluded.
	Match(relPath string) bool
}

// tieredMatcher implements two-tier matching: user-overridable performance
// patterns and non-overridable security patterns.
type tieredMatcher struct {
	// userAndPerf matches performance patterns + user patterns.
	// User negation patterns can override performance patterns here.
	userAndPerf *ignore.GitIgnore

	// security matches non-overridable security patterns.
	// Checked last — if it matches, the file is always excluded.
	security *ignore.GitIgnore
}

// NewMatcher creates a two-tier Matcher.
//
// userAndPerfPatterns: performance defaults + .sandboxignore + CLI patterns.
// User negation patterns (e.g. !node_modules/) can override performance patterns.
//
// securityPatterns: non-overridable built-in patterns. If a security pattern
// matches, the file is excluded regardless of any user negation.
func NewMatcher(userAndPerfPatterns, securityPatterns []string) Matcher {
	return &tieredMatcher{
		userAndPerf: ignore.CompileIgnoreLines(userAndPerfPatterns...),
		security:    ignore.CompileIgnoreLines(securityPatterns...),
	}
}

// Match returns true if relPath should be excluded from the snapshot.
func (m *tieredMatcher) Match(relPath string) bool {
	// Security patterns are non-overridable — always exclude.
	if m.security.MatchesPath(relPath) {
		return true
	}
	// User + performance patterns — user negation can un-exclude performance patterns.
	return m.userAndPerf.MatchesPath(relPath)
}
