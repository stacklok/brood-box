// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package workspace

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/stacklok/sandbox-agent/internal/infra/exclude"
)

// snapshotDirPrefix is the prefix for snapshot temporary directories.
const snapshotDirPrefix = ".sandbox-snapshot-"

// Snapshot holds references to the original and snapshot workspace paths.
type Snapshot struct {
	// OriginalPath is the real workspace directory.
	OriginalPath string

	// SnapshotPath is the COW clone directory.
	SnapshotPath string
}

// Cleanup removes the snapshot directory.
func (s *Snapshot) Cleanup() error {
	if s.SnapshotPath == "" {
		return nil
	}
	return os.RemoveAll(s.SnapshotPath)
}

// WorkspaceCloner creates workspace snapshots.
type WorkspaceCloner interface {
	// CreateSnapshot creates a COW snapshot of the workspace.
	CreateSnapshot(ctx context.Context, workspacePath string, matcher exclude.Matcher) (*Snapshot, error)
}

// FSWorkspaceCloner creates file-system-based workspace snapshots.
type FSWorkspaceCloner struct {
	cloner FileCloner
	logger *slog.Logger
}

// NewFSWorkspaceCloner creates a WorkspaceCloner backed by the filesystem.
func NewFSWorkspaceCloner(cloner FileCloner, logger *slog.Logger) *FSWorkspaceCloner {
	return &FSWorkspaceCloner{
		cloner: cloner,
		logger: logger,
	}
}

// CreateSnapshot walks the source tree, selectively copying files that are not
// excluded by the matcher. Symlinks pointing outside the workspace are skipped.
func (c *FSWorkspaceCloner) CreateSnapshot(ctx context.Context, workspacePath string, matcher exclude.Matcher) (*Snapshot, error) {
	absWorkspace, err := filepath.Abs(workspacePath)
	if err != nil {
		return nil, fmt.Errorf("resolving workspace path: %w", err)
	}

	// Create temp dir in the same parent directory (same filesystem for COW).
	parentDir := filepath.Dir(absWorkspace)
	tmpDir, err := os.MkdirTemp(parentDir, snapshotDirPrefix)
	if err != nil {
		return nil, fmt.Errorf("creating snapshot temp dir: %w", err)
	}

	// On any failure, clean up the temp dir.
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(tmpDir)
		}
	}()

	err = filepath.WalkDir(absWorkspace, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		// Check for context cancellation.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		relPath, err := filepath.Rel(absWorkspace, path)
		if err != nil {
			return fmt.Errorf("computing relative path: %w", err)
		}

		// Skip the root directory itself.
		if relPath == "." {
			return nil
		}

		destPath := filepath.Join(tmpDir, relPath)

		// Handle symlinks first.
		if d.Type()&fs.ModeSymlink != 0 {
			return c.handleSymlink(absWorkspace, path, relPath, destPath, matcher)
		}

		// Handle directories.
		if d.IsDir() {
			if matcher.Match(relPath) || matcher.Match(relPath+"/") {
				c.logger.Debug("excluding directory from snapshot", "path", relPath)
				return filepath.SkipDir
			}
			info, err := d.Info()
			if err != nil {
				return fmt.Errorf("stat directory %s: %w", relPath, err)
			}
			return os.MkdirAll(destPath, info.Mode())
		}

		// Handle regular files.
		if matcher.Match(relPath) {
			c.logger.Debug("excluding file from snapshot", "path", relPath)
			return nil
		}

		return c.cloner.CloneFile(path, destPath)
	})

	if err != nil {
		return nil, fmt.Errorf("walking workspace: %w", err)
	}

	success = true
	return &Snapshot{
		OriginalPath: absWorkspace,
		SnapshotPath: tmpDir,
	}, nil
}

// handleSymlink checks whether a symlink target is within the workspace and
// copies it if so. Symlinks pointing outside the workspace are skipped.
func (c *FSWorkspaceCloner) handleSymlink(workspaceRoot, path, relPath, destPath string, matcher exclude.Matcher) error {
	if matcher.Match(relPath) {
		c.logger.Debug("excluding symlink from snapshot", "path", relPath)
		return nil
	}

	// Resolve the symlink target.
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		c.logger.Warn("skipping unresolvable symlink", "path", relPath, "error", err)
		return nil
	}

	// Validate symlink target is within workspace boundary.
	if err := ValidateInBounds(workspaceRoot, resolved); err != nil {
		c.logger.Warn("skipping symlink pointing outside workspace",
			"path", relPath,
			"target", resolved,
		)
		return nil
	}

	// Read and recreate the symlink.
	linkTarget, err := os.Readlink(path)
	if err != nil {
		return fmt.Errorf("reading symlink %s: %w", relPath, err)
	}

	return os.Symlink(linkTarget, destPath)
}

// CleanupStaleSnapshots removes orphaned snapshot directories from a previous
// crash. It scans the parent directory of workspacePath for dirs matching the
// snapshot prefix.
func CleanupStaleSnapshots(workspacePath string, logger *slog.Logger) {
	parentDir := filepath.Dir(workspacePath)
	entries, err := os.ReadDir(parentDir)
	if err != nil {
		logger.Warn("failed to scan for stale snapshots", "error", err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), snapshotDirPrefix) {
			stalePath := filepath.Join(parentDir, entry.Name())
			logger.Warn("removing stale snapshot directory", "path", stalePath)
			if err := os.RemoveAll(stalePath); err != nil {
				logger.Error("failed to remove stale snapshot", "path", stalePath, "error", err)
			}
		}
	}
}
