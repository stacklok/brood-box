// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package mount

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"syscall"
	"time"
)

type mountEntry struct {
	source string
	target string
	fstype string
	flags  uintptr
	data   string
}

// Essential mounts the core filesystems required for a minimal Linux userspace.
func Essential() error {
	mounts := []mountEntry{
		{"proc", "/proc", "proc", syscall.MS_NOSUID | syscall.MS_NODEV | syscall.MS_NOEXEC, ""},
		{"sysfs", "/sys", "sysfs", syscall.MS_NOSUID | syscall.MS_NODEV | syscall.MS_NOEXEC, ""},
		{"devtmpfs", "/dev", "devtmpfs", 0, ""},
		{"devpts", "/dev/pts", "devpts", syscall.MS_NOSUID | syscall.MS_NOEXEC, ""},
		{"tmpfs", "/tmp", "tmpfs", syscall.MS_NOSUID | syscall.MS_NODEV, ""},
		{"tmpfs", "/run", "tmpfs", syscall.MS_NOSUID | syscall.MS_NODEV, ""},
	}

	for _, m := range mounts {
		if err := os.MkdirAll(m.target, 0o755); err != nil {
			return fmt.Errorf("creating mount point %s: %w", m.target, err)
		}

		if err := syscall.Mount(m.source, m.target, m.fstype, m.flags, m.data); err != nil {
			if errors.Is(err, syscall.EBUSY) {
				slog.Debug("filesystem already mounted", "target", m.target)
				continue
			}
			return fmt.Errorf("mounting %s on %s: %w", m.fstype, m.target, err)
		}
	}

	// Create standard /dev symlinks.
	symlinks := [][2]string{
		{"/proc/self/fd", "/dev/fd"},
		{"/proc/self/fd/0", "/dev/stdin"},
		{"/proc/self/fd/1", "/dev/stdout"},
		{"/proc/self/fd/2", "/dev/stderr"},
	}

	for _, sl := range symlinks {
		if err := os.Symlink(sl[0], sl[1]); err != nil && !os.IsExist(err) {
			return fmt.Errorf("creating symlink %s: %w", sl[1], err)
		}
	}

	return nil
}

// Workspace mounts a virtiofs share at the given mount point, retrying up to
// maxRetries times to allow the host to expose the filesystem.
func Workspace(mountPoint, tag string, maxRetries int) error {
	if err := os.MkdirAll(mountPoint, 0o755); err != nil {
		return fmt.Errorf("creating workspace mount point %s: %w", mountPoint, err)
	}

	var lastErr error
	for i := range maxRetries {
		lastErr = syscall.Mount(tag, mountPoint, "virtiofs", 0, "")
		if lastErr == nil {
			if err := os.Chown(mountPoint, 1000, 1000); err != nil {
				return fmt.Errorf("chown workspace %s: %w", mountPoint, err)
			}
			return nil
		}
		slog.Warn("virtiofs mount failed, retrying",
			"tag", tag,
			"attempt", i+1,
			"max", maxRetries,
			"err", lastErr,
		)
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("mounting virtiofs tag %q on %s after %d retries: %w", tag, mountPoint, maxRetries, lastErr)
}
