// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package review

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/sandbox-agent/internal/domain/snapshot"
	"github.com/stacklok/sandbox-agent/internal/infra/diff"
)

func TestFSFlusher_AddedFile(t *testing.T) {
	t.Parallel()

	origDir := t.TempDir()
	snapDir := t.TempDir()

	// New file in snapshot.
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "new.go"), []byte("added"), 0o644))

	hash, err := diff.HashFile(filepath.Join(snapDir, "new.go"))
	require.NoError(t, err)

	flusher := NewFSFlusher()
	err = flusher.Flush(origDir, snapDir, []snapshot.FileChange{
		{RelPath: "new.go", Kind: snapshot.Added, Hash: hash},
	})
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(origDir, "new.go"))
	require.NoError(t, err)
	assert.Equal(t, "added", string(data))
}

func TestFSFlusher_ModifiedFile(t *testing.T) {
	t.Parallel()

	origDir := t.TempDir()
	snapDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(origDir, "file.go"), []byte("original"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "file.go"), []byte("modified"), 0o644))

	hash, err := diff.HashFile(filepath.Join(snapDir, "file.go"))
	require.NoError(t, err)

	flusher := NewFSFlusher()
	err = flusher.Flush(origDir, snapDir, []snapshot.FileChange{
		{RelPath: "file.go", Kind: snapshot.Modified, Hash: hash},
	})
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(origDir, "file.go"))
	require.NoError(t, err)
	assert.Equal(t, "modified", string(data))
}

func TestFSFlusher_DeletedFile(t *testing.T) {
	t.Parallel()

	origDir := t.TempDir()
	snapDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(origDir, "delete-me.go"), []byte("bye"), 0o644))

	flusher := NewFSFlusher()
	err := flusher.Flush(origDir, snapDir, []snapshot.FileChange{
		{RelPath: "delete-me.go", Kind: snapshot.Deleted},
	})
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(origDir, "delete-me.go"))
	assert.True(t, os.IsNotExist(err))
}

func TestFSFlusher_PathTraversalRejected(t *testing.T) {
	t.Parallel()

	origDir := t.TempDir()
	snapDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "evil.go"), []byte("pwned"), 0o644))
	hash, err := diff.HashFile(filepath.Join(snapDir, "evil.go"))
	require.NoError(t, err)

	flusher := NewFSFlusher()
	err = flusher.Flush(origDir, snapDir, []snapshot.FileChange{
		{RelPath: "../../../etc/passwd", Kind: snapshot.Added, Hash: hash},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "path traversal rejected")
}

func TestFSFlusher_HashMismatchRejected(t *testing.T) {
	t.Parallel()

	origDir := t.TempDir()
	snapDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "file.go"), []byte("content"), 0o644))

	flusher := NewFSFlusher()
	err := flusher.Flush(origDir, snapDir, []snapshot.FileChange{
		{RelPath: "file.go", Kind: snapshot.Modified, Hash: "wrong-hash"},
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "hash mismatch")
}

func TestFSFlusher_CreatesParentDirs(t *testing.T) {
	t.Parallel()

	origDir := t.TempDir()
	snapDir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(snapDir, "a", "b"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "a", "b", "deep.go"), []byte("deep"), 0o644))

	hash, err := diff.HashFile(filepath.Join(snapDir, "a", "b", "deep.go"))
	require.NoError(t, err)

	flusher := NewFSFlusher()
	err = flusher.Flush(origDir, snapDir, []snapshot.FileChange{
		{RelPath: filepath.Join("a", "b", "deep.go"), Kind: snapshot.Added, Hash: hash},
	})
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(origDir, "a", "b", "deep.go"))
	require.NoError(t, err)
	assert.Equal(t, "deep", string(data))
}

func TestFSFlusher_PreservesPermissions(t *testing.T) {
	t.Parallel()

	origDir := t.TempDir()
	snapDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "script.sh"), []byte("#!/bin/sh"), 0o755))

	hash, err := diff.HashFile(filepath.Join(snapDir, "script.sh"))
	require.NoError(t, err)

	flusher := NewFSFlusher()
	err = flusher.Flush(origDir, snapDir, []snapshot.FileChange{
		{RelPath: "script.sh", Kind: snapshot.Added, Hash: hash},
	})
	require.NoError(t, err)

	info, err := os.Stat(filepath.Join(origDir, "script.sh"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}
