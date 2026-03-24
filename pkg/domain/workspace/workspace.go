// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package workspace defines domain interfaces and types for workspace
// snapshot management.
package workspace

import (
	"context"

	"github.com/stacklok/brood-box/pkg/domain/snapshot"
)

// Snapshot holds references to the original and snapshot workspace paths.
type Snapshot struct {
	// OriginalPath is the real workspace directory.
	OriginalPath string

	// SnapshotPath is the COW clone directory.
	SnapshotPath string

	// Cleanup removes the snapshot directory. Set by the WorkspaceCloner
	// implementation. Safe to call multiple times.
	Cleanup func() error
}

// WorkspaceCloner creates workspace snapshots.
type WorkspaceCloner interface {
	// CreateSnapshot creates a COW snapshot of the workspace.
	CreateSnapshot(ctx context.Context, workspacePath string, matcher snapshot.Matcher) (*Snapshot, error)
}
