// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package review

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/stacklok/sandbox-agent/internal/domain/snapshot"
	"github.com/stacklok/sandbox-agent/internal/infra/diff"
	"github.com/stacklok/sandbox-agent/internal/infra/workspace"
)

// Flusher copies accepted changes from the snapshot back to the original
// workspace.
type Flusher interface {
	// Flush applies accepted changes from snapshotDir to originalDir.
	Flush(originalDir, snapshotDir string, accepted []snapshot.FileChange) error
}

// FSFlusher implements Flusher using filesystem operations.
type FSFlusher struct{}

// NewFSFlusher creates a new filesystem-based flusher.
func NewFSFlusher() *FSFlusher {
	return &FSFlusher{}
}

// Flush copies accepted added/modified files from snapshotDir to originalDir,
// and deletes files marked as Deleted from originalDir.
//
// Security: each target path is validated to be within originalDir, and each
// snapshot file's SHA-256 is re-verified against the hash recorded at diff time.
func (f *FSFlusher) Flush(originalDir, snapshotDir string, accepted []snapshot.FileChange) error {
	for _, ch := range accepted {
		targetPath := filepath.Join(originalDir, ch.RelPath)

		// Validate path stays within bounds.
		if err := workspace.ValidateInBounds(originalDir, targetPath); err != nil {
			return fmt.Errorf("path traversal rejected for %s: %w", ch.RelPath, err)
		}

		switch ch.Kind {
		case snapshot.Added, snapshot.Modified:
			snapPath := filepath.Join(snapshotDir, ch.RelPath)

			// Re-verify hash before copying.
			currentHash, err := diff.HashFile(snapPath)
			if err != nil {
				return fmt.Errorf("re-hashing %s: %w", ch.RelPath, err)
			}
			if currentHash != ch.Hash {
				return fmt.Errorf("hash mismatch for %s: file modified between diff and flush", ch.RelPath)
			}

			// Ensure parent directory exists.
			if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return fmt.Errorf("creating parent dir for %s: %w", ch.RelPath, err)
			}

			if err := copyFilePreserveMode(snapPath, targetPath); err != nil {
				return fmt.Errorf("flushing %s: %w", ch.RelPath, err)
			}

		case snapshot.Deleted:
			if err := os.Remove(targetPath); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("deleting %s: %w", ch.RelPath, err)
			}
		}
	}

	return nil
}

// copyFilePreserveMode copies src to dst preserving file permissions.
func copyFilePreserveMode(src, dst string) error {
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = sf.Close() }()

	info, err := sf.Stat()
	if err != nil {
		return err
	}

	df, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer func() { _ = df.Close() }()

	_, err = io.Copy(df, sf)
	return err
}
