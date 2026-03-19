// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package git

import "context"

// CommitReplayer replays commits from a snapshot repository onto the original.
type CommitReplayer interface {
	// ResolveHEAD returns the current HEAD commit hash for the repo at
	// the given path. Returns an empty string (not an error) if the
	// repo has no commits or is not a git repository.
	ResolveHEAD(ctx context.Context, repoPath string) (string, error)

	// Replay extracts new commits (after baseRef) from the snapshot repo
	// and recreates them in the original repo using the same metadata.
	// Only files in the accepted list are included in replayed commits.
	// Returns the count of replayed and skipped commits.
	//
	// baseRef is the HEAD commit hash recorded before the VM started.
	// An empty baseRef means no initial HEAD existed (e.g. worktree case)
	// and replay is skipped.
	Replay(ctx context.Context, originalPath, snapshotPath, baseRef string, accepted []string) (*ReplayResult, error)
}

// ReplayResult holds the outcome of a commit replay operation.
type ReplayResult struct {
	// Replayed is the number of commits successfully recreated.
	Replayed int
	// Skipped is the number of commits that were skipped (e.g. no accepted files, merge commits).
	Skipped int
}
