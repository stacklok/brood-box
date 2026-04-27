// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package workspace

import (
	"os"

	"golang.org/x/sys/unix"
)

// linuxCloner uses ioctl(FICLONE) for COW cloning with regular copy fallback.
type linuxCloner struct{}

// NewPlatformCloner returns a FileCloner that uses Linux FICLONE for COW.
func NewPlatformCloner() FileCloner {
	return &linuxCloner{}
}

// CloneFile attempts a FICLONE ioctl for COW, falling back to regular copy.
func (c *linuxCloner) CloneFile(src, dst string) error {
	// Try FICLONE first by opening both files.
	if err := c.tryFiclone(src, dst); err == nil {
		return nil
	}

	// FICLONE not supported (e.g. ext4, tmpfs) — fall back to regular copy.
	return copyFile(src, dst)
}

// tryFiclone attempts a FICLONE ioctl. Returns nil on success, error otherwise.
func (c *linuxCloner) tryFiclone(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = srcFile.Close() }()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	// Open writable and change permissions after clone to handle read-only src.
	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = dstFile.Close() }()

	if err := unix.IoctlFileClone(int(dstFile.Fd()), int(srcFile.Fd())); err != nil {
		return err
	}

	return os.Chmod(dst, srcInfo.Mode().Perm())
}
