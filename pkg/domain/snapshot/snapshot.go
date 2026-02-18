// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package snapshot defines pure domain types for workspace snapshot isolation.
// It contains no I/O dependencies.
package snapshot

import "fmt"

// ChangeKind describes the type of file change between snapshot and original.
type ChangeKind int

const (
	// Added means the file exists in the snapshot but not in the original.
	Added ChangeKind = iota
	// Modified means the file exists in both but differs.
	Modified
	// Deleted means the file was removed from the snapshot.
	Deleted
)

// String returns a human-readable label for a ChangeKind.
func (k ChangeKind) String() string {
	switch k {
	case Added:
		return "added"
	case Modified:
		return "modified"
	case Deleted:
		return "deleted"
	default:
		return fmt.Sprintf("unknown(%d)", int(k))
	}
}

// FileChange represents a single changed file between the original workspace
// and the snapshot.
type FileChange struct {
	// RelPath is the path relative to the workspace root.
	RelPath string

	// Kind describes the type of change.
	Kind ChangeKind

	// UnifiedDiff is the unified diff string for text files.
	// For binary files this contains "Binary file differs".
	UnifiedDiff string

	// Hash is the SHA-256 hex digest of the snapshot file at diff time.
	// Used to detect modifications between diff and flush.
	Hash string
}

// ReviewDecision represents a user's decision for a single file change.
type ReviewDecision int

const (
	// Accept means the change should be flushed to the original workspace.
	Accept ReviewDecision = iota
	// Reject means the change should be discarded.
	Reject
)

// ReviewResult holds the categorized results of an interactive review session.
type ReviewResult struct {
	// Accepted are the changes the user approved for flushing.
	Accepted []FileChange
	// Rejected are the changes the user rejected or skipped.
	Rejected []FileChange
}
