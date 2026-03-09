// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package homefs

import (
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
)

// copyTree recursively copies the contents of src into dst, preserving
// permissions and ownership on a best-effort basis. Symlinks are recreated.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("computing relative path: %w", err)
		}

		// Skip root — dst already exists.
		if rel == "." {
			return nil
		}

		dstPath := filepath.Join(dst, rel)

		// Handle symlinks.
		if d.Type()&fs.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return fmt.Errorf("readlink %s: %w", rel, err)
			}
			return os.Symlink(target, dstPath)
		}

		// Handle directories.
		if d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return fmt.Errorf("stat dir %s: %w", rel, err)
			}
			if err := os.MkdirAll(dstPath, info.Mode().Perm()); err != nil {
				return fmt.Errorf("mkdir %s: %w", rel, err)
			}
			copyOwnership(path, dstPath)
			return nil
		}

		// Handle regular files.
		return copyFile(path, dstPath)
	})
}

// copyFile copies a single file preserving permissions.
func copyFile(src, dst string) error {
	sf, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open %s: %w", src, err)
	}
	defer func() { _ = sf.Close() }()

	info, err := sf.Stat()
	if err != nil {
		return fmt.Errorf("stat %s: %w", src, err)
	}

	df, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}

	if _, err := io.Copy(df, sf); err != nil {
		_ = df.Close()
		return fmt.Errorf("copy %s: %w", dst, err)
	}

	if err := df.Close(); err != nil {
		return err
	}

	copyOwnership(src, dst)
	return nil
}

// copyOwnership copies uid/gid from src to dst (best-effort).
func copyOwnership(src, dst string) {
	info, err := os.Lstat(src)
	if err != nil {
		return
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return
	}
	_ = os.Lchown(dst, int(stat.Uid), int(stat.Gid))
}

// chownRecursive sets uid:gid on all entries under root. Failures are
// logged rather than silently discarded because a failed chown means
// the sandbox user cannot access its own SSH keys or config files.
func chownRecursive(root string, uid, gid int, logger *slog.Logger) {
	_ = filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := os.Lchown(path, uid, gid); err != nil {
			logger.Warn("chown failed, sandbox user may lack access",
				"path", path, "error", err)
		}
		return nil
	})
}
