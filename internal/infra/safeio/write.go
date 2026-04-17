// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package safeio provides defensive file-I/O primitives. Today its
// single concern is opening files for write WITHOUT following
// symlinks at the final path component, so a pre-existing symlink
// cannot redirect a write to an unintended target.
//
// This matters in three brood-box flows:
//
//   - The flusher writes from the snapshot back to the host workspace.
//     A symlink at the destination (either user-planted or a TOCTOU
//     race) would otherwise cause bbox to overwrite whatever the
//     symlink points at (worst case: ~/.ssh/authorized_keys).
//
//   - The credential store writes credentials into the VM rootfs. A
//     malicious or buggy base image containing pre-existing symlinks
//     could otherwise redirect a credential write out of the rootfs.
//
//   - The settings injector writes per-agent settings (Claude Code's
//     ~/.claude/*, opencode's ~/.config/opencode/*) into the VM
//     rootfs. Same concern as the credential store.
//
// In each case the destination path is path-component-controlled by
// brood-box, but the final inode may not be: O_NOFOLLOW ensures the
// open fails cleanly with ELOOP rather than silently traversing.
//
// Scope note: O_NOFOLLOW only covers the final path component. A
// symlinked parent directory is still resolved normally — that layer
// is the responsibility of the caller's containment check
// (ValidateInBounds, validateContainment) rather than safeio. This
// matches POSIX semantics and is documented here so callers do not
// over-rely on safeio for defense-in-depth that only the full layering
// provides.
package safeio

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// OpenForWrite opens path for writing with create-or-truncate
// semantics, refusing to follow a symlink at the final path
// component. If path IS a symlink, open fails with syscall.ELOOP
// wrapped in a clear error.
//
// The caller owns the returned *os.File and is responsible for Close.
func OpenForWrite(path string, mode os.FileMode) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC|syscall.O_NOFOLLOW, mode)
	if err != nil {
		return nil, wrapSymlinkError(path, err)
	}
	return f, nil
}

// WriteFileNoFollow is the O_NOFOLLOW equivalent of os.WriteFile:
// it writes data to path with create-or-truncate semantics, refusing
// to follow a symlink at the final path component.
func WriteFileNoFollow(path string, data []byte, mode os.FileMode) error {
	f, err := OpenForWrite(path, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// wrapSymlinkError augments an ELOOP error with a clear message so
// users hitting the rejection (via a symlink in their workspace or
// a base image) can tell what happened, what the symlink points at,
// and how to recover.
func wrapSymlinkError(path string, err error) error {
	if !isSymlinkError(err) {
		return err
	}
	target, linkErr := os.Readlink(path)
	if linkErr != nil || target == "" {
		return fmt.Errorf("%s: refusing to follow symlink — edit the target file directly or add the path to .broodboxignore", path)
	}
	return fmt.Errorf("%s: refusing to follow symlink to %q — edit that file directly or add the path to .broodboxignore", path, target)
}

// isSymlinkError reports whether err indicates that an O_NOFOLLOW
// open failed because the final path component is a symlink. Both
// Linux and Darwin return ELOOP from open(2) in this case (per the
// POSIX spec); we check both via errors.As unwrapping the
// syscall.Errno out of *os.PathError.
func isSymlinkError(err error) bool {
	if err == nil {
		return false
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == syscall.ELOOP
}
