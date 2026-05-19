// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

// Package homefs provides a writable overlay for the sandbox user's home
// directory. On certain host kernels, the root virtiofs rejects writes even
// though the mount is nominally read-write. This package works around the
// issue by mounting an overlayfs (with tmpfs upper) on top of the home
// directory, preserving all files injected by rootfs hooks while enabling
// writes from the sandbox user. If overlayfs is unavailable in the guest
// kernel, a tmpfs + copy fallback is used instead.
package homefs

import (
	"fmt"
	"log/slog"
	"os"
	"runtime"
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

// MakeWritable ensures the sandbox user's home directory is both writable
// and owned by the sandbox user. It mounts an overlayfs (with tmpfs upper)
// when the underlying virtiofs rejects writes, falling back to a tmpfs +
// copy approach on kernels without overlayfs. It then recursively chowns
// the tree so host-injected files (settings, credentials, skills) are
// readable by the sandbox user; macOS host-side rootfs hooks cannot chown
// to UID 1000, so reconciliation must happen inside the guest. Must be
// called before seccomp blocks mount(2).
func MakeWritable(logger *slog.Logger, home string, uid, gid int) error {
	// Reconcile ownership unconditionally on return so host-injected files
	// are readable by the sandbox user even if the mount step fails. On a
	// read-only FS, Lchown returns EROFS and is logged as a warning.
	defer chownRecursive(home, uid, gid, logger)

	// Probe writability as the sandbox user (not root). We run as PID 1
	// (root), so os.Create() would always succeed on a normal FS — we
	// must test as the actual sandbox user to catch permission issues.
	if probeWritableAs(home, uid, gid) {
		logger.Info("home directory is writable, skipping overlay")
		return nil
	}

	logger.Info("home directory is read-only via virtiofs, mounting overlay",
		"home", home)
	return mountOverlayOrTmpfs(logger, home)
}

// mountOverlayOrTmpfs mounts an overlayfs with a tmpfs upper layer on home,
// falling back to a plain tmpfs + copy when overlayfs is unavailable.
func mountOverlayOrTmpfs(logger *slog.Logger, home string) error {
	// Create upper and work directories on tmpfs.
	for _, dir := range []string{overlayUpper, overlayWork} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
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
		return fallbackTmpfs(logger, home)
	}

	logger.Info("overlay mounted on home directory", "home", home)
	return nil
}

// probeWritableAs tests whether the given uid:gid can create a file in dir
// by temporarily switching the thread's effective credentials. This must run
// in a dedicated goroutine locked to an OS thread to avoid affecting other
// goroutines.
func probeWritableAs(dir string, uid, gid int) bool {
	type result struct{ ok bool }
	ch := make(chan result, 1)

	go func() {
		// Lock this goroutine to a single OS thread so credential
		// changes don't leak to other goroutines.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		// Switch effective UID/GID to the sandbox user.
		if err := syscall.Setregid(-1, gid); err != nil {
			ch <- result{false}
			return
		}
		if err := syscall.Setreuid(-1, uid); err != nil {
			// Restore GID before returning.
			_ = syscall.Setregid(-1, 0)
			ch <- result{false}
			return
		}

		// Try to create a probe file as the sandbox user.
		probe := dir + "/.write-probe"
		f, err := os.Create(probe)
		ok := err == nil
		if ok {
			_ = f.Close()
			_ = os.Remove(probe)
		}

		// Restore root credentials. If restoration fails, terminate
		// this goroutine so the tainted thread is never returned to
		// the Go runtime pool.
		if err := syscall.Setreuid(-1, 0); err != nil {
			ch <- result{false}
			runtime.Goexit()
			return
		}
		if err := syscall.Setregid(-1, 0); err != nil {
			ch <- result{false}
			runtime.Goexit()
			return
		}

		ch <- result{ok}
	}()

	return (<-ch).ok
}

// fallbackTmpfs is used when overlayfs is not available in the guest kernel.
// It mounts a tmpfs directly on the home directory after copying existing
// contents into a staging area.
func fallbackTmpfs(logger *slog.Logger, home string) error {
	logger.Info("falling back to tmpfs + copy for writable home")

	staging := "/tmp/.home-staging"
	if err := os.MkdirAll(staging, 0o700); err != nil {
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

	// Copy staging back to home (now tmpfs). If this fails, unmount
	// the empty tmpfs to restore the original (read-only) virtiofs
	// contents — a read-only home with credentials is better than a
	// writable but empty one.
	if err := copyTree(staging, home); err != nil {
		_ = syscall.Unmount(home, 0)
		return fmt.Errorf("restoring home from staging: %w", err)
	}

	// Clean up staging. Ownership of the tmpfs contents is reconciled by
	// MakeWritable's recursive chown after this returns.
	_ = os.RemoveAll(staging)

	logger.Info("tmpfs mounted on home directory", "home", home)
	return nil
}
