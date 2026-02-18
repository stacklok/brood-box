// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package diff

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/apiary/pkg/domain/snapshot"
)

// nilMatcher never excludes anything.
type nilMatcher struct{}

func (m *nilMatcher) Match(_ string) bool { return false }

func TestFSDiffer_NoChanges(t *testing.T) {
	t.Parallel()

	origDir := t.TempDir()
	snapDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(origDir, "file.go"), []byte("same"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "file.go"), []byte("same"), 0o644))

	d := NewFSDiffer()
	changes, err := d.Diff(origDir, snapDir, &nilMatcher{})
	require.NoError(t, err)
	assert.Empty(t, changes)
}

func TestFSDiffer_AddedFile(t *testing.T) {
	t.Parallel()

	origDir := t.TempDir()
	snapDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(origDir, "existing.go"), []byte("keep"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "existing.go"), []byte("keep"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "new.go"), []byte("added"), 0o644))

	d := NewFSDiffer()
	changes, err := d.Diff(origDir, snapDir, &nilMatcher{})
	require.NoError(t, err)

	require.Len(t, changes, 1)
	assert.Equal(t, "new.go", changes[0].RelPath)
	assert.Equal(t, snapshot.Added, changes[0].Kind)
	assert.NotEmpty(t, changes[0].Hash)
	assert.Contains(t, changes[0].UnifiedDiff, "+added")
}

func TestFSDiffer_ModifiedFile(t *testing.T) {
	t.Parallel()

	origDir := t.TempDir()
	snapDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(origDir, "file.go"), []byte("original"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "file.go"), []byte("modified"), 0o644))

	d := NewFSDiffer()
	changes, err := d.Diff(origDir, snapDir, &nilMatcher{})
	require.NoError(t, err)

	require.Len(t, changes, 1)
	assert.Equal(t, "file.go", changes[0].RelPath)
	assert.Equal(t, snapshot.Modified, changes[0].Kind)
	assert.NotEmpty(t, changes[0].Hash)
	assert.NotEmpty(t, changes[0].UnifiedDiff)
}

func TestFSDiffer_DeletedFile(t *testing.T) {
	t.Parallel()

	origDir := t.TempDir()
	snapDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(origDir, "deleted.go"), []byte("gone"), 0o644))
	// File absent from snapDir.

	d := NewFSDiffer()
	changes, err := d.Diff(origDir, snapDir, &nilMatcher{})
	require.NoError(t, err)

	require.Len(t, changes, 1)
	assert.Equal(t, "deleted.go", changes[0].RelPath)
	assert.Equal(t, snapshot.Deleted, changes[0].Kind)
}

func TestFSDiffer_BinaryFile(t *testing.T) {
	t.Parallel()

	origDir := t.TempDir()
	snapDir := t.TempDir()

	binaryData := make([]byte, 100)
	binaryData[50] = 0 // null byte
	require.NoError(t, os.WriteFile(filepath.Join(origDir, "image.png"), binaryData, 0o644))

	modifiedBinary := make([]byte, 100)
	modifiedBinary[50] = 0
	modifiedBinary[51] = 1 // different
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "image.png"), modifiedBinary, 0o644))

	d := NewFSDiffer()
	changes, err := d.Diff(origDir, snapDir, &nilMatcher{})
	require.NoError(t, err)

	require.Len(t, changes, 1)
	assert.Equal(t, "Binary file differs", changes[0].UnifiedDiff)
}

func TestFSDiffer_SortedOutput(t *testing.T) {
	t.Parallel()

	origDir := t.TempDir()
	snapDir := t.TempDir()

	// Create files in non-alphabetical order.
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "z.go"), []byte("z"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "a.go"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "m.go"), []byte("m"), 0o644))

	d := NewFSDiffer()
	changes, err := d.Diff(origDir, snapDir, &nilMatcher{})
	require.NoError(t, err)

	require.Len(t, changes, 3)
	assert.Equal(t, "a.go", changes[0].RelPath)
	assert.Equal(t, "m.go", changes[1].RelPath)
	assert.Equal(t, "z.go", changes[2].RelPath)
}

func TestFSDiffer_NestedDirectories(t *testing.T) {
	t.Parallel()

	origDir := t.TempDir()
	snapDir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(origDir, "a", "b"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(origDir, "a", "b", "deep.go"), []byte("orig"), 0o644))

	require.NoError(t, os.MkdirAll(filepath.Join(snapDir, "a", "b"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(snapDir, "a", "b", "deep.go"), []byte("changed"), 0o644))

	d := NewFSDiffer()
	changes, err := d.Diff(origDir, snapDir, &nilMatcher{})
	require.NoError(t, err)

	require.Len(t, changes, 1)
	assert.Equal(t, filepath.Join("a", "b", "deep.go"), changes[0].RelPath)
	assert.Equal(t, snapshot.Modified, changes[0].Kind)
}

func TestHashFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	require.NoError(t, os.WriteFile(path, []byte("hello"), 0o644))

	hash1, err := HashFile(path)
	require.NoError(t, err)
	assert.NotEmpty(t, hash1)

	// Same content should produce same hash.
	hash2, err := HashFile(path)
	require.NoError(t, err)
	assert.Equal(t, hash1, hash2)

	// Different content should produce different hash.
	require.NoError(t, os.WriteFile(path, []byte("world"), 0o644))
	hash3, err := HashFile(path)
	require.NoError(t, err)
	assert.NotEqual(t, hash1, hash3)
}
