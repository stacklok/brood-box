// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package workspace

import (
	"fmt"
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
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening source for clone: %w", err)
	}
	defer func() { _ = srcFile.Close() }()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return fmt.Errorf("stat source for clone: %w", err)
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return fmt.Errorf("creating dest for clone: %w", err)
	}
	defer func() { _ = dstFile.Close() }()

	// Try FICLONE — this is a reflink/COW clone.
	err = unix.IoctlFileClone(int(dstFile.Fd()), int(srcFile.Fd()))
	if err == nil {
		return nil
	}

	// FICLONE not supported (e.g. ext4, tmpfs) — fall back to regular copy.
	// Close and re-create to reset file position.
	_ = dstFile.Close()
	return copyFile(src, dst)
}
