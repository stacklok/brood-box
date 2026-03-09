// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

// Package homefs provides a writable overlay for the sandbox user's home
// directory. On certain host kernels, the root virtiofs rejects writes even
// though the mount is nominally read-write. This package works around the
// issue by mounting an overlayfs (with tmpfs upper) on top of the home
// directory, preserving all files injected by rootfs hooks while enabling
// writes from the sandbox user.
package homefs

import (
	"fmt"
	"log/slog"
	"os"
	"syscall"
)

const (
	// SandboxHome is the home directory of the sandbox user inside the guest.
	SandboxHome = "/home/sandbox"
	// SandboxUID is the UID of the sandbox user inside the guest.
	SandboxUID = 1000
	// SandboxGID is the GID of the sandbox user inside the guest.
	SandboxGID = 1000

	// overlayUpper and overlayWork live on /tmp which is already tmpfs.
	overlayUpper = "/tmp/.home-upper"
	overlayWork  = "/tmp/.home-work"
)

// MakeWritable mounts an overlayfs on the sandbox home directory so that
// writes go to a tmpfs-backed upper layer. The original virtiofs contents
// remain visible as the lower layer. This must be called before seccomp
// blocks mount(2).
//
// If the home directory is already writable, this is a no-op.
func MakeWritable(logger *slog.Logger, home string, uid, gid int) error {
	// Quick probe: if we can create and remove a temp file, the home
	// directory is already writable and no overlay is needed.
	probe := home + "/.write-probe"
	if f, err := os.Create(probe); err == nil {
		_ = f.Close()
		_ = os.Remove(probe)
		logger.Info("home directory is writable, skipping overlay")
		return nil
	}

	logger.Info("home directory is read-only via virtiofs, mounting overlay",
		"home", home)

	// Create upper and work directories on tmpfs.
	for _, dir := range []string{overlayUpper, overlayWork} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating overlay dir %s: %w", dir, err)
		}
	}

	// Mount overlayfs: lower=home (virtiofs, read-only in practice),
	// upper+work on tmpfs → writes land in tmpfs.
	opts := fmt.Sprintf("lowerdir=%s,upperdir=%s,workdir=%s",
		home, overlayUpper, overlayWork)
	if err := syscall.Mount("overlay", home, "overlay", 0, opts); err != nil {
		logger.Warn("overlayfs mount failed, falling back to tmpfs copy",
			"error", err)
		return fallbackTmpfs(logger, home, uid, gid)
	}

	// Chown the overlay mount point so the sandbox user owns it.
	if err := os.Chown(home, uid, gid); err != nil {
		logger.Warn("chown overlay mount point failed", "error", err)
	}

	logger.Info("overlay mounted on home directory", "home", home)
	return nil
}

// fallbackTmpfs is used when overlayfs is not available in the guest kernel.
// It mounts a tmpfs directly on the home directory after copying existing
// contents into a staging area.
func fallbackTmpfs(logger *slog.Logger, home string, uid, gid int) error {
	logger.Info("falling back to tmpfs + copy for writable home")

	staging := "/tmp/.home-staging"
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return fmt.Errorf("creating staging dir: %w", err)
	}

	// Copy existing home contents to staging.
	if err := copyTree(home, staging); err != nil {
		return fmt.Errorf("copying home to staging: %w", err)
	}

	// Mount tmpfs on home.
	if err := syscall.Mount("tmpfs", home, "tmpfs",
		syscall.MS_NOSUID|syscall.MS_NODEV, "size=512m"); err != nil {
		return fmt.Errorf("mounting tmpfs on %s: %w", home, err)
	}

	// Copy staging back to home (now tmpfs).
	if err := copyTree(staging, home); err != nil {
		return fmt.Errorf("restoring home from staging: %w", err)
	}

	// Fix ownership.
	chownRecursive(home, uid, gid)

	// Clean up staging.
	_ = os.RemoveAll(staging)

	logger.Info("tmpfs mounted on home directory", "home", home)
	return nil
}
