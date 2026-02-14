// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package workspace

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testMatcher is a simple matcher for testing.
type testMatcher struct {
	excluded map[string]bool
}

func (m *testMatcher) Match(relPath string) bool {
	return m.excluded[relPath]
}

// fallbackCloner always uses regular copy (for tests on non-COW filesystems).
type fallbackCloner struct{}

func (c *fallbackCloner) CloneFile(src, dst string) error {
	return copyFile(src, dst)
}

func TestCreateSnapshot_BasicFiles(t *testing.T) {
	t.Parallel()

	srcDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package main"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(srcDir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "sub", "lib.go"), []byte("package sub"), 0o644))

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cloner := NewFSWorkspaceCloner(&fallbackCloner{}, logger)
	matcher := &testMatcher{excluded: map[string]bool{}}

	snap, err := cloner.CreateSnapshot(context.Background(), srcDir, matcher)
	require.NoError(t, err)
	defer func() { _ = snap.Cleanup() }()

	// Verify files exist in snapshot.
	data, err := os.ReadFile(filepath.Join(snap.SnapshotPath, "main.go"))
	require.NoError(t, err)
	assert.Equal(t, "package main", string(data))

	data, err = os.ReadFile(filepath.Join(snap.SnapshotPath, "sub", "lib.go"))
	require.NoError(t, err)
	assert.Equal(t, "package sub", string(data))
}

func TestCreateSnapshot_ExcludedFiles(t *testing.T) {
	t.Parallel()

	srcDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("keep"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, ".env"), []byte("SECRET=x"), 0o644))

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cloner := NewFSWorkspaceCloner(&fallbackCloner{}, logger)
	matcher := &testMatcher{excluded: map[string]bool{".env": true}}

	snap, err := cloner.CreateSnapshot(context.Background(), srcDir, matcher)
	require.NoError(t, err)
	defer func() { _ = snap.Cleanup() }()

	// main.go should exist.
	_, err = os.Stat(filepath.Join(snap.SnapshotPath, "main.go"))
	assert.NoError(t, err)

	// .env should NOT exist.
	_, err = os.Stat(filepath.Join(snap.SnapshotPath, ".env"))
	assert.True(t, os.IsNotExist(err))
}

func TestCreateSnapshot_ExcludedDirectory(t *testing.T) {
	t.Parallel()

	srcDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(srcDir, "node_modules", "pkg"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "node_modules", "pkg", "index.js"), []byte("module"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "app.js"), []byte("app"), 0o644))

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cloner := NewFSWorkspaceCloner(&fallbackCloner{}, logger)
	matcher := &testMatcher{excluded: map[string]bool{"node_modules": true, "node_modules/": true}}

	snap, err := cloner.CreateSnapshot(context.Background(), srcDir, matcher)
	require.NoError(t, err)
	defer func() { _ = snap.Cleanup() }()

	// app.js should exist.
	_, err = os.Stat(filepath.Join(snap.SnapshotPath, "app.js"))
	assert.NoError(t, err)

	// node_modules should NOT exist.
	_, err = os.Stat(filepath.Join(snap.SnapshotPath, "node_modules"))
	assert.True(t, os.IsNotExist(err))
}

func TestCreateSnapshot_SymlinkOutsideBoundary(t *testing.T) {
	t.Parallel()

	// Create a target file outside the workspace.
	outsideDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("secret"), 0o644))

	srcDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("keep"), 0o644))
	// Create symlink pointing outside workspace.
	require.NoError(t, os.Symlink(filepath.Join(outsideDir, "secret.txt"), filepath.Join(srcDir, "link.txt")))

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cloner := NewFSWorkspaceCloner(&fallbackCloner{}, logger)
	matcher := &testMatcher{excluded: map[string]bool{}}

	snap, err := cloner.CreateSnapshot(context.Background(), srcDir, matcher)
	require.NoError(t, err)
	defer func() { _ = snap.Cleanup() }()

	// main.go should exist.
	_, err = os.Stat(filepath.Join(snap.SnapshotPath, "main.go"))
	assert.NoError(t, err)

	// Symlink pointing outside should be skipped.
	_, err = os.Lstat(filepath.Join(snap.SnapshotPath, "link.txt"))
	assert.True(t, os.IsNotExist(err))
}

func TestCreateSnapshot_SymlinkInsideBoundary(t *testing.T) {
	t.Parallel()

	srcDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "target.txt"), []byte("hello"), 0o644))
	require.NoError(t, os.Symlink("target.txt", filepath.Join(srcDir, "link.txt")))

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cloner := NewFSWorkspaceCloner(&fallbackCloner{}, logger)
	matcher := &testMatcher{excluded: map[string]bool{}}

	snap, err := cloner.CreateSnapshot(context.Background(), srcDir, matcher)
	require.NoError(t, err)
	defer func() { _ = snap.Cleanup() }()

	// Symlink should be preserved.
	linkDest, err := os.Readlink(filepath.Join(snap.SnapshotPath, "link.txt"))
	require.NoError(t, err)
	assert.Equal(t, "target.txt", linkDest)
}

func TestCreateSnapshot_PreservesPermissions(t *testing.T) {
	t.Parallel()

	srcDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "script.sh"), []byte("#!/bin/sh"), 0o755))

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cloner := NewFSWorkspaceCloner(&fallbackCloner{}, logger)
	matcher := &testMatcher{excluded: map[string]bool{}}

	snap, err := cloner.CreateSnapshot(context.Background(), srcDir, matcher)
	require.NoError(t, err)
	defer func() { _ = snap.Cleanup() }()

	info, err := os.Stat(filepath.Join(snap.SnapshotPath, "script.sh"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func TestCreateSnapshot_ContextCancellation(t *testing.T) {
	t.Parallel()

	srcDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(srcDir, "file.go"), []byte("x"), 0o644))

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	cloner := NewFSWorkspaceCloner(&fallbackCloner{}, logger)
	matcher := &testMatcher{excluded: map[string]bool{}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	_, err := cloner.CreateSnapshot(ctx, srcDir, matcher)
	assert.Error(t, err)
}

func TestSnapshot_Cleanup(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	snapDir := filepath.Join(tmpDir, "snap")
	require.NoError(t, os.MkdirAll(snapDir, 0o755))

	snap := &Snapshot{SnapshotPath: snapDir}
	require.NoError(t, snap.Cleanup())

	_, err := os.Stat(snapDir)
	assert.True(t, os.IsNotExist(err))
}

func TestValidateInBounds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		base    string
		target  string
		wantErr bool
	}{
		{"within bounds", "/workspace", "/workspace/file.go", false},
		{"at root", "/workspace", "/workspace", false},
		{"nested within bounds", "/workspace", "/workspace/a/b/c.go", false},
		{"escapes via dotdot", "/workspace", "/workspace/../etc/passwd", true},
		{"completely outside", "/workspace", "/tmp/other", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateInBounds(tt.base, tt.target)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestCleanupStaleSnapshots(t *testing.T) {
	t.Parallel()

	parentDir := t.TempDir()
	workspaceDir := filepath.Join(parentDir, "my-project")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	// Create a stale snapshot dir.
	staleDir := filepath.Join(parentDir, ".sandbox-snapshot-abc123")
	require.NoError(t, os.MkdirAll(staleDir, 0o755))

	// Create a non-snapshot dir (should not be removed).
	otherDir := filepath.Join(parentDir, "other-project")
	require.NoError(t, os.MkdirAll(otherDir, 0o755))

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	CleanupStaleSnapshots(workspaceDir, logger)

	_, err := os.Stat(staleDir)
	assert.True(t, os.IsNotExist(err), "stale snapshot should be removed")

	_, err = os.Stat(otherDir)
	assert.NoError(t, err, "non-snapshot dir should remain")
}

func TestPlatformCloner(t *testing.T) {
	t.Parallel()

	// Test that the platform cloner works (falls back to copy on non-COW FS).
	srcDir := t.TempDir()
	dstDir := t.TempDir()

	srcFile := filepath.Join(srcDir, "test.txt")
	dstFile := filepath.Join(dstDir, "test.txt")
	require.NoError(t, os.WriteFile(srcFile, []byte("hello COW"), 0o644))

	cloner := NewPlatformCloner()
	require.NoError(t, cloner.CloneFile(srcFile, dstFile))

	data, err := os.ReadFile(dstFile)
	require.NoError(t, err)
	assert.Equal(t, "hello COW", string(data))
}
