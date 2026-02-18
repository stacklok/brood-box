// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package snapshot

// Differ computes the differences between an original workspace and its snapshot.
type Differ interface {
	// Diff returns the list of file changes between originalDir and snapshotDir.
	// The matcher is used to skip excluded files (which shouldn't appear in the
	// snapshot but we check defensively).
	Diff(originalDir, snapshotDir string, matcher Matcher) ([]FileChange, error)
}
