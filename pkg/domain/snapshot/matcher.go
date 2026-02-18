// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package snapshot

// Matcher decides whether a relative path should be excluded from the snapshot.
type Matcher interface {
	// Match returns true if the given relative path should be excluded.
	Match(relPath string) bool
}

// NopMatcher is a Matcher that excludes nothing.
var NopMatcher Matcher = nopMatcher{}

type nopMatcher struct{}

func (nopMatcher) Match(string) bool { return false }
