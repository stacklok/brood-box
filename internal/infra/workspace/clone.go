// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package workspace provides COW workspace cloning for snapshot isolation.
package workspace

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FileCloner clones individual files, ideally using COW (copy-on-write).
type FileCloner interface {
	// CloneFile creates a clone of src at dst. Implementations should attempt
	// COW (e.g. FICLONE on Linux, clonefile on macOS) and fall back to a
	// regular copy.
	CloneFile(src, dst string) error
}

// copyFile performs a regular file copy from src to dst, preserving permission
// bits. Setuid/setgid/sticky bits are stripped for security.
func copyFile(src, dst string) error {
	sf, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening source: %w", err)
	}
	defer func() { _ = sf.Close() }()

	info, err := sf.Stat()
	if err != nil {
		return fmt.Errorf("stat source: %w", err)
	}

	// Strip setuid/setgid/sticky — only preserve rwx permissions.
	mode := info.Mode().Perm()

	// Open writable and change permissions after copy to handle read-only src.
	df, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("creating destination: %w", err)
	}

	if _, err := io.Copy(df, sf); err != nil {
		_ = df.Close()
		return fmt.Errorf("copying data: %w", err)
	}

	// Explicitly close and return any error (e.g., NFS write-back failure).
	if err := df.Close(); err != nil {
		return err
	}

	return os.Chmod(dst, mode)
}

// ValidateInBounds verifies that targetPath (after resolution) is within
// basePath. Symlinks in targetPath are resolved to prevent symlink-based
// traversal attacks.
func ValidateInBounds(basePath, targetPath string) error {
	absBase, err := filepath.Abs(basePath)
	if err != nil {
		return fmt.Errorf("resolving base path: %w", err)
	}

	// Resolve symlinks in the base path itself so the comparison is
	// against the real filesystem location (prevents base-path symlink attacks).
	resolvedBase, err := resolveExistingPrefix(absBase)
	if err != nil {
		return fmt.Errorf("resolving symlinks in base: %w", err)
	}

	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return fmt.Errorf("resolving target path: %w", err)
	}

	// Resolve symlinks in the target to catch symlink-based traversal.
	// If the target doesn't exist yet (e.g., new file being flushed),
	// resolve the longest existing prefix.
	resolvedTarget, err := resolveExistingPrefix(absTarget)
	if err != nil {
		return fmt.Errorf("resolving symlinks in target: %w", err)
	}

	// Ensure base ends with separator for prefix check.
	basePrefix := resolvedBase + string(filepath.Separator)
	if resolvedTarget != resolvedBase && !strings.HasPrefix(resolvedTarget, basePrefix) {
		return fmt.Errorf("path %q (resolved: %q) escapes base directory %q", absTarget, resolvedTarget, resolvedBase)
	}

	return nil
}

// resolveExistingPrefix resolves symlinks for the longest existing prefix
// of the given path. For paths where the final component(s) don't exist yet,
// this resolves the parent directories that do exist.
func resolveExistingPrefix(path string) (string, error) {
	// Try resolving the full path first.
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}

	// Walk up until we find an existing directory, resolve it,
	// then append the remaining components.
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	if dir == path {
		// Reached the root without finding an existing path.
		return path, nil
	}

	resolvedDir, err := resolveExistingPrefix(dir)
	if err != nil {
		return "", err
	}

	return filepath.Join(resolvedDir, base), nil
}
