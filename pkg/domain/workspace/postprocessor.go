// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package workspace

import "context"

// MountRequest describes an additional filesystem mount that a
// post-processor needs exposed inside the guest VM.
type MountRequest struct {
	// Tag is the virtiofs tag visible in the guest (e.g. "git-objects").
	Tag string

	// HostPath is the host directory to mount.
	HostPath string

	// GuestPath is the mount point inside the guest (e.g. "/mnt/git-objects").
	GuestPath string

	// ReadOnly mounts the filesystem read-only when true.
	ReadOnly bool
}

// PostProcessResult carries side-effects produced by a post-processor
// that must be wired into the VM configuration or diff pipeline.
type PostProcessResult struct {
	// Mounts are additional virtiofs mounts to expose inside the VM.
	Mounts []MountRequest

	// DiffExclude lists additional path prefixes to exclude from the diff
	// computation (e.g. ".git" excludes ".git" and everything under ".git/").
	DiffExclude []string
}

// SnapshotPostProcessor runs a transformation on a workspace snapshot
// after it has been created but before the VM is started.
//
// The originalPath parameter points to the real workspace — needed when
// a post-processor must read files excluded from the snapshot (e.g.,
// .git/config is a security pattern excluded from the snapshot, but the
// sanitizer needs to read it from the original workspace).
//
// Returns a PostProcessResult with any side-effects (extra mounts, diff
// excludes). Nil result means no side-effects.
type SnapshotPostProcessor interface {
	Process(ctx context.Context, originalPath, snapshotPath string) (*PostProcessResult, error)
}
