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

// copyFile performs a regular file copy from src to dst, preserving mode bits.
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

	df, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return fmt.Errorf("creating destination: %w", err)
	}
	defer func() { _ = df.Close() }()

	if _, err := io.Copy(df, sf); err != nil {
		return fmt.Errorf("copying data: %w", err)
	}

	return nil
}

// ValidateInBounds verifies that targetPath (after resolution) is within
// basePath. Returns an error if the target escapes the base directory.
func ValidateInBounds(basePath, targetPath string) error {
	absBase, err := filepath.Abs(basePath)
	if err != nil {
		return fmt.Errorf("resolving base path: %w", err)
	}

	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return fmt.Errorf("resolving target path: %w", err)
	}

	// Ensure base ends with separator for prefix check.
	basePrefix := absBase + string(filepath.Separator)
	if absTarget != absBase && !strings.HasPrefix(absTarget, basePrefix) {
		return fmt.Errorf("path %q escapes base directory %q", absTarget, absBase)
	}

	return nil
}
