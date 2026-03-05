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
	"strconv"
	"strings"

	"github.com/stacklok/brood-box/internal/infra/process"
	"github.com/stacklok/brood-box/pkg/domain/snapshot"
	domws "github.com/stacklok/brood-box/pkg/domain/workspace"
)

// Ensure FSWorkspaceCloner implements domws.WorkspaceCloner at compile time.
var _ domws.WorkspaceCloner = (*FSWorkspaceCloner)(nil)

// snapshotDirPrefix is the prefix for snapshot temporary directories.
const snapshotDirPrefix = ".sandbox-snapshot-"

// snapshotSentinelSuffix is a marker file placed alongside snapshot directories
// to identify them as sandbox snapshots (vs unrelated directories).
const snapshotSentinelSuffix = ".sentinel"

// snapshotSentinelPath returns the path to the sentinel file for a given
// snapshot directory. The sentinel is a sibling of the directory, not inside it.
func snapshotSentinelPath(dir string) string {
	return dir + snapshotSentinelSuffix
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
func (c *FSWorkspaceCloner) CreateSnapshot(ctx context.Context, workspacePath string, matcher snapshot.Matcher) (*domws.Snapshot, error) {
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

	// Write sentinel file with our PID to identify this as an active snapshot.
	// The PID allows stale cleanup to distinguish dead-process snapshots from
	// a concurrently running instance's active snapshot.
	sentinelPath := snapshotSentinelPath(tmpDir)
	sentinelContent := fmt.Sprintf("%d", os.Getpid())
	if err := os.WriteFile(sentinelPath, []byte(sentinelContent), 0o600); err != nil {
		return nil, fmt.Errorf("writing snapshot sentinel: %w", err)
	}

	success = true
	snapshotDir := tmpDir
	return &domws.Snapshot{
		OriginalPath: absWorkspace,
		SnapshotPath: snapshotDir,
		Cleanup: func() error {
			// Sentinel removal is best-effort; don't let it block directory cleanup.
			_ = os.Remove(sentinelPath)
			return os.RemoveAll(snapshotDir)
		},
	}, nil
}

// handleSymlink checks whether a symlink target is within the workspace and
// copies it if so. Symlinks pointing outside the workspace are skipped.
//
// Security: reads the symlink target only once to prevent TOCTOU attacks
// where the symlink is modified between check and use.
func (c *FSWorkspaceCloner) handleSymlink(workspaceRoot, path, relPath, destPath string, matcher snapshot.Matcher) error {
	if matcher.Match(relPath) {
		c.logger.Debug("excluding symlink from snapshot", "path", relPath)
		return nil
	}

	// Read the symlink target ONCE to avoid TOCTOU between check and use.
	linkTarget, err := os.Readlink(path)
	if err != nil {
		c.logger.Warn("skipping unreadable symlink", "path", relPath, "error", err)
		return nil
	}

	// Resolve the target to an absolute path for boundary validation.
	absTarget := linkTarget
	if !filepath.IsAbs(linkTarget) {
		absTarget = filepath.Join(filepath.Dir(path), linkTarget)
	}
	absTarget = filepath.Clean(absTarget)

	// Validate symlink target is within workspace boundary.
	if err := ValidateInBounds(workspaceRoot, absTarget); err != nil {
		c.logger.Warn("skipping symlink pointing outside workspace",
			"path", relPath,
			"target", absTarget,
		)
		return nil
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
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), snapshotDirPrefix) {
			continue
		}

		stalePath := filepath.Join(parentDir, entry.Name())

		// Only remove directories that have our sentinel file to avoid
		// deleting unrelated directories.
		sentinelPath := snapshotSentinelPath(stalePath)
		data, err := os.ReadFile(sentinelPath)
		if err != nil {
			logger.Debug("skipping directory without sentinel", "path", stalePath)
			continue
		}

		// If the sentinel contains a PID, check if that process is still alive.
		// Skip cleanup for snapshots owned by a running process.
		if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
			if process.IsAlive(pid) {
				logger.Debug("skipping snapshot owned by running process",
					"path", stalePath, "pid", pid)
				continue
			}
		}

		logger.Warn("removing stale snapshot directory", "path", stalePath)
		if err := os.RemoveAll(stalePath); err != nil {
			logger.Error("failed to remove stale snapshot", "path", stalePath, "error", err)
			continue
		}
		if err := os.Remove(sentinelPath); err != nil && !os.IsNotExist(err) {
			logger.Error("failed to remove snapshot sentinel", "path", sentinelPath, "error", err)
		}
	}
}
