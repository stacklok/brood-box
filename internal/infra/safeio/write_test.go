// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package safeio

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteFileNoFollow_CreatesNewFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")

	require.NoError(t, WriteFileNoFollow(path, []byte("hello"), 0o600))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), got)

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestWriteFileNoFollow_TruncatesExisting(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "existing.txt")
	require.NoError(t, os.WriteFile(path, []byte("original-long-content"), 0o600))

	require.NoError(t, WriteFileNoFollow(path, []byte("short"), 0o600))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, []byte("short"), got)
}

func TestWriteFileNoFollow_RejectsSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	require.NoError(t, os.WriteFile(target, []byte("target-content"), 0o600))

	link := filepath.Join(dir, "link.txt")
	require.NoError(t, os.Symlink(target, link))

	err := WriteFileNoFollow(link, []byte("overwrite"), 0o600)
	require.Error(t, err)
	// Error names the symlink target so users can tell what bbox
	// would have overwritten.
	assert.Contains(t, err.Error(), "symlink")
	assert.Contains(t, err.Error(), target)
	// Error mentions .broodboxignore as a recovery path.
	assert.Contains(t, err.Error(), ".broodboxignore")

	// Target must NOT have been overwritten.
	got, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, []byte("target-content"), got)
}

func TestWriteFileNoFollow_RejectsSymlinkToNonexistent(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Symlink pointing at a path that does not exist.
	link := filepath.Join(dir, "dangling.txt")
	require.NoError(t, os.Symlink("/nonexistent/target", link))

	err := WriteFileNoFollow(link, []byte("overwrite"), 0o600)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlink")
	// Target name is still surfaced even when it does not exist.
	assert.Contains(t, err.Error(), "/nonexistent/target")
}

func TestOpenForWrite_RegularFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "regular.txt")

	f, err := OpenForWrite(path, 0o600)
	require.NoError(t, err)
	_, writeErr := f.Write([]byte("via OpenForWrite"))
	closeErr := f.Close()
	require.NoError(t, writeErr)
	require.NoError(t, closeErr)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, []byte("via OpenForWrite"), got)
}
