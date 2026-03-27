// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package git

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorktreeProcessor_NotAWorktree(t *testing.T) {
	t.Parallel()

	snapshot := t.TempDir()
	// .git is a directory — normal repo, not a worktree.
	require.NoError(t, os.MkdirAll(filepath.Join(snapshot, ".git"), 0o755))

	proc := NewWorktreeProcessor(slog.Default())
	result, err := proc.Process(context.Background(), snapshot, snapshot)

	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestWorktreeProcessor_NoGitEntry(t *testing.T) {
	t.Parallel()

	snapshot := t.TempDir()
	// No .git at all.

	proc := NewWorktreeProcessor(slog.Default())
	result, err := proc.Process(context.Background(), snapshot, snapshot)

	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestWorktreeProcessor_ExternalWorktree(t *testing.T) {
	t.Parallel()

	// Set up a simulated main repo .git directory.
	mainRepo := t.TempDir()
	mainGitDir := filepath.Join(mainRepo, ".git")
	require.NoError(t, os.MkdirAll(mainGitDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "config"), []byte("[core]\n\tbare = false\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(mainGitDir, "objects", "pack"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(mainGitDir, "refs", "heads"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(mainGitDir, "refs", "heads", "main"),
		[]byte("abcdef1234567890abcdef1234567890abcdef12\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "packed-refs"), []byte("# pack-refs with: peeled fully-peeled sorted\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(mainGitDir, "info"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "info", "exclude"), []byte("# git ls-files --others --exclude-from\n"), 0o644))

	// Set up a simulated worktree gitdir (like .git/worktrees/wt1/).
	worktreeGitDir := filepath.Join(mainGitDir, "worktrees", "wt1")
	require.NoError(t, os.MkdirAll(worktreeGitDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(worktreeGitDir, "HEAD"),
		[]byte("ref: refs/heads/feature\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(worktreeGitDir, "commondir"),
		[]byte("../..\n"), // relative: worktrees/wt1/../../ = .git/
		0o644,
	))
	require.NoError(t, os.MkdirAll(filepath.Join(worktreeGitDir, "refs", "heads"), 0o755))

	// Set up the original workspace directory with a .git file.
	workspace := t.TempDir()
	gitFileContent := "gitdir: " + worktreeGitDir + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(workspace, ".git"), []byte(gitFileContent), 0o644))

	// Set up the snapshot directory with a copy of the .git file.
	snapshot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(snapshot, ".git"), []byte(gitFileContent), 0o644))

	proc := NewWorktreeProcessor(slog.Default())
	result, err := proc.Process(context.Background(), workspace, snapshot)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify mount request.
	require.Len(t, result.Mounts, 1)
	assert.Equal(t, "git-objects", result.Mounts[0].Tag)
	assert.Equal(t, filepath.Join(mainGitDir, "objects"), result.Mounts[0].HostPath)
	assert.Equal(t, "/mnt/git-objects", result.Mounts[0].GuestPath)
	assert.True(t, result.Mounts[0].ReadOnly)

	// Verify diff exclude.
	assert.Contains(t, result.DiffExclude, ".git")

	// Verify .git is now a directory.
	dotGitInfo, err := os.Lstat(filepath.Join(snapshot, ".git"))
	require.NoError(t, err)
	assert.True(t, dotGitInfo.IsDir())

	// Verify HEAD is from the worktree (feature branch).
	headData, err := os.ReadFile(filepath.Join(snapshot, ".git", "HEAD"))
	require.NoError(t, err)
	assert.Equal(t, "ref: refs/heads/feature\n", string(headData))

	// Config is NOT written by WorktreeProcessor — ConfigSanitizer owns it.
	_, err = os.Stat(filepath.Join(snapshot, ".git", "config"))
	assert.True(t, os.IsNotExist(err), ".git/config should not be written by WorktreeProcessor")

	// Verify objects/info/alternates.
	alternatesData, err := os.ReadFile(filepath.Join(snapshot, ".git", "objects", "info", "alternates"))
	require.NoError(t, err)
	assert.Equal(t, "/mnt/git-objects\n", string(alternatesData))

	// Verify objects/pack/ exists.
	packInfo, err := os.Stat(filepath.Join(snapshot, ".git", "objects", "pack"))
	require.NoError(t, err)
	assert.True(t, packInfo.IsDir())

	// Verify refs from main repo were copied.
	mainRefData, err := os.ReadFile(filepath.Join(snapshot, ".git", "refs", "heads", "main"))
	require.NoError(t, err)
	assert.Equal(t, "abcdef1234567890abcdef1234567890abcdef12\n", string(mainRefData))

	// Verify packed-refs was copied.
	_, err = os.Stat(filepath.Join(snapshot, ".git", "packed-refs"))
	assert.NoError(t, err)

	// Verify info/exclude was copied.
	_, err = os.Stat(filepath.Join(snapshot, ".git", "info", "exclude"))
	assert.NoError(t, err)
}

func TestWorktreeProcessor_MalformedGitFile(t *testing.T) {
	t.Parallel()

	snapshot := t.TempDir()
	// .git file with garbage content (no "gitdir: " prefix).
	require.NoError(t, os.WriteFile(filepath.Join(snapshot, ".git"), []byte("this is garbage\n"), 0o644))

	proc := NewWorktreeProcessor(slog.Default())
	result, err := proc.Process(context.Background(), snapshot, snapshot)

	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestWorktreeProcessor_MissingObjects(t *testing.T) {
	t.Parallel()

	// Set up main repo WITHOUT objects directory.
	mainRepo := t.TempDir()
	mainGitDir := filepath.Join(mainRepo, ".git")
	require.NoError(t, os.MkdirAll(mainGitDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	// Intentionally do NOT create objects/.

	// Set up worktree gitdir pointing to main repo.
	worktreeGitDir := filepath.Join(mainGitDir, "worktrees", "wt1")
	require.NoError(t, os.MkdirAll(worktreeGitDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(worktreeGitDir, "HEAD"),
		[]byte("ref: refs/heads/feature\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(worktreeGitDir, "commondir"),
		[]byte("../..\n"),
		0o644,
	))

	workspace := t.TempDir()
	gitFileContent := "gitdir: " + worktreeGitDir + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(workspace, ".git"), []byte(gitFileContent), 0o644))

	snapshot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(snapshot, ".git"), []byte(gitFileContent), 0o644))

	proc := NewWorktreeProcessor(slog.Default())
	result, err := proc.Process(context.Background(), workspace, snapshot)

	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestWorktreeProcessor_CraftedGitdir(t *testing.T) {
	t.Parallel()

	// Simulate a crafted repo whose .git file points to a gitdir that is
	// NOT under <repo>/.git/worktrees/<name>/. This could be used to trick
	// the processor into reading arbitrary host directories.
	craftedDir := t.TempDir()
	// Provide everything the processor would need if the path were accepted.
	require.NoError(t, os.WriteFile(filepath.Join(craftedDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(craftedDir, "commondir"), []byte(".\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(craftedDir, "objects", "pack"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(craftedDir, "refs", "heads"), 0o755))

	workspace := t.TempDir()
	gitFileContent := "gitdir: " + craftedDir + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(workspace, ".git"), []byte(gitFileContent), 0o644))

	snapshot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(snapshot, ".git"), []byte(gitFileContent), 0o644))

	proc := NewWorktreeProcessor(slog.Default())
	result, err := proc.Process(context.Background(), workspace, snapshot)

	require.NoError(t, err)
	assert.Nil(t, result, "crafted gitdir path should be rejected (nil result)")

	// .git should remain a file (not converted to a directory).
	dotGitInfo, err := os.Lstat(filepath.Join(snapshot, ".git"))
	require.NoError(t, err)
	assert.True(t, dotGitInfo.Mode().IsRegular(), ".git should still be a file")
}

func TestWorktreeProcessor_SymlinkInGitdir(t *testing.T) {
	t.Parallel()

	// Set up a legitimate-looking worktree structure, but with a symlinked
	// packed-refs pointing to an external file. The copyFile function should
	// skip the symlink rather than following it.
	mainRepo := t.TempDir()
	mainGitDir := filepath.Join(mainRepo, ".git")
	require.NoError(t, os.MkdirAll(mainGitDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "config"), []byte("[core]\n\tbare = false\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(mainGitDir, "objects", "pack"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(mainGitDir, "refs", "heads"), 0o755))

	// Create a secret file that should NOT be exposed.
	secretFile := filepath.Join(t.TempDir(), "secret.txt")
	secretContent := "TOP SECRET DATA"
	require.NoError(t, os.WriteFile(secretFile, []byte(secretContent), 0o644))

	// Replace packed-refs with a symlink to the secret file.
	packedRefsPath := filepath.Join(mainGitDir, "packed-refs")
	require.NoError(t, os.Symlink(secretFile, packedRefsPath))

	// Set up worktree gitdir.
	worktreeGitDir := filepath.Join(mainGitDir, "worktrees", "wt1")
	require.NoError(t, os.MkdirAll(worktreeGitDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(worktreeGitDir, "HEAD"),
		[]byte("ref: refs/heads/feature\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(worktreeGitDir, "commondir"),
		[]byte("../..\n"),
		0o644,
	))

	workspace := t.TempDir()
	gitFileContent := "gitdir: " + worktreeGitDir + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(workspace, ".git"), []byte(gitFileContent), 0o644))

	snapshot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(snapshot, ".git"), []byte(gitFileContent), 0o644))

	proc := NewWorktreeProcessor(slog.Default())
	result, err := proc.Process(context.Background(), workspace, snapshot)

	require.NoError(t, err)
	require.NotNil(t, result, "worktree processing should succeed")

	// The symlinked packed-refs should NOT have been copied.
	snapshotPackedRefs := filepath.Join(snapshot, ".git", "packed-refs")
	_, statErr := os.Stat(snapshotPackedRefs)
	assert.True(t, os.IsNotExist(statErr),
		"symlinked packed-refs should not be copied into the snapshot")
}

func TestWorktreeProcessor_DoesNotWriteConfig(t *testing.T) {
	t.Parallel()

	// WorktreeProcessor should NOT write .git/config — that responsibility
	// belongs to ConfigSanitizer (separate post-processor).
	mainRepo := t.TempDir()
	mainGitDir := filepath.Join(mainRepo, ".git")
	require.NoError(t, os.MkdirAll(mainGitDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "config"), []byte("[core]\n\tbare = false\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(mainGitDir, "objects", "pack"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(mainGitDir, "refs", "heads"), 0o755))

	worktreeGitDir := filepath.Join(mainGitDir, "worktrees", "wt1")
	require.NoError(t, os.MkdirAll(worktreeGitDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(worktreeGitDir, "HEAD"),
		[]byte("ref: refs/heads/feature\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(worktreeGitDir, "commondir"),
		[]byte("../..\n"),
		0o644,
	))

	workspace := t.TempDir()
	gitFileContent := "gitdir: " + worktreeGitDir + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(workspace, ".git"), []byte(gitFileContent), 0o644))

	snapshot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(snapshot, ".git"), []byte(gitFileContent), 0o644))

	proc := NewWorktreeProcessor(slog.Default())
	result, err := proc.Process(context.Background(), workspace, snapshot)

	require.NoError(t, err)
	require.NotNil(t, result)

	// .git/config should NOT exist — ConfigSanitizer handles it.
	_, err = os.Stat(filepath.Join(snapshot, ".git", "config"))
	assert.True(t, os.IsNotExist(err), ".git/config should not be written by WorktreeProcessor")
}

func TestWorktreeProcessor_DetachedHEAD(t *testing.T) {
	t.Parallel()

	detachedSHA := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"

	// Set up a simulated main repo .git directory.
	mainRepo := t.TempDir()
	mainGitDir := filepath.Join(mainRepo, ".git")
	require.NoError(t, os.MkdirAll(mainGitDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "config"), []byte("[core]\n\tbare = false\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(mainGitDir, "objects", "pack"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(mainGitDir, "refs", "heads"), 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(mainGitDir, "refs", "heads", "main"),
		[]byte("abcdef1234567890abcdef1234567890abcdef12\n"),
		0o644,
	))

	// Set up a simulated worktree gitdir with a detached HEAD (raw SHA, not a ref).
	worktreeGitDir := filepath.Join(mainGitDir, "worktrees", "wt1")
	require.NoError(t, os.MkdirAll(worktreeGitDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(worktreeGitDir, "HEAD"),
		[]byte(detachedSHA+"\n"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(worktreeGitDir, "commondir"),
		[]byte("../..\n"),
		0o644,
	))
	require.NoError(t, os.MkdirAll(filepath.Join(worktreeGitDir, "refs", "heads"), 0o755))

	// Set up the original workspace directory with a .git file.
	workspace := t.TempDir()
	gitFileContent := "gitdir: " + worktreeGitDir + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(workspace, ".git"), []byte(gitFileContent), 0o644))

	// Set up the snapshot directory with a copy of the .git file.
	snapshot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(snapshot, ".git"), []byte(gitFileContent), 0o644))

	proc := NewWorktreeProcessor(slog.Default())
	result, err := proc.Process(context.Background(), workspace, snapshot)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify the snapshot .git/HEAD contains the raw SHA (detached HEAD preserved),
	// NOT "ref: refs/heads/main" from the main repo.
	headData, err := os.ReadFile(filepath.Join(snapshot, ".git", "HEAD"))
	require.NoError(t, err)
	assert.Equal(t, detachedSHA+"\n", string(headData))
}

func TestWorktreeProcessor_CraftedCommondir(t *testing.T) {
	t.Parallel()

	// Set up a legitimate-looking worktree structure where the gitdir
	// passes isWorktreeGitdir, but the commondir file points to an
	// external directory that is NOT an ancestor of the gitdir.
	mainRepo := t.TempDir()
	mainGitDir := filepath.Join(mainRepo, ".git")
	require.NoError(t, os.MkdirAll(mainGitDir, 0o755))

	// Create a worktree gitdir that passes isWorktreeGitdir.
	worktreeGitDir := filepath.Join(mainGitDir, "worktrees", "wt1")
	require.NoError(t, os.MkdirAll(worktreeGitDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(worktreeGitDir, "HEAD"),
		[]byte("ref: refs/heads/feature\n"),
		0o644,
	))

	// Create an external directory with a HEAD and objects/ to satisfy
	// the downstream checks — simulating a crafted commondir attack.
	externalDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(externalDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(externalDir, "objects", "pack"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(externalDir, "refs", "heads"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(externalDir, "config"), []byte("[core]\n\tbare = false\n"), 0o644))

	// Point commondir to the external directory (the attack vector).
	require.NoError(t, os.WriteFile(
		filepath.Join(worktreeGitDir, "commondir"),
		[]byte(externalDir+"\n"),
		0o644,
	))

	workspace := t.TempDir()
	gitFileContent := "gitdir: " + worktreeGitDir + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(workspace, ".git"), []byte(gitFileContent), 0o644))

	snapshot := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(snapshot, ".git"), []byte(gitFileContent), 0o644))

	proc := NewWorktreeProcessor(slog.Default())
	result, err := proc.Process(context.Background(), workspace, snapshot)

	require.NoError(t, err)
	assert.Nil(t, result, "crafted commondir pointing outside git dir should be rejected")

	// .git should remain a file (not converted to a directory).
	dotGitInfo, err := os.Lstat(filepath.Join(snapshot, ".git"))
	require.NoError(t, err)
	assert.True(t, dotGitInfo.Mode().IsRegular(), ".git should still be a file")
}
