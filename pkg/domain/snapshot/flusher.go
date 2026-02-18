// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package snapshot

// Flusher copies accepted changes from the snapshot back to the original
// workspace.
type Flusher interface {
	// Flush applies accepted changes from snapshotDir to originalDir.
	Flush(originalDir, snapshotDir string, accepted []FileChange) error
}
