// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testLogger returns a logger that discards output unless tests are run with -v.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestCleanupStaleLogs(t *testing.T) {
	t.Parallel()

	vmsDir := filepath.Join(t.TempDir(), "vms")
	require.NoError(t, os.MkdirAll(vmsDir, 0o755))

	// Stale directory with dead PID sentinel — should be removed.
	staleDir := filepath.Join(vmsDir, "stale-vm")
	require.NoError(t, os.MkdirAll(staleDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(staleDir, LogSentinel), []byte("2147483647"), 0o600))
	// Also create a log file to verify entire directory is removed.
	require.NoError(t, os.WriteFile(filepath.Join(staleDir, "broodbox.log"), []byte("old log"), 0o600))

	// Directory with live PID sentinel (our process) — should be preserved.
	liveDir := filepath.Join(vmsDir, "live-vm")
	require.NoError(t, os.MkdirAll(liveDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(liveDir, LogSentinel), []byte(fmt.Sprintf("%d", os.Getpid())), 0o600))

	// Directory without sentinel — should be preserved.
	noSentinelDir := filepath.Join(vmsDir, "no-sentinel-vm")
	require.NoError(t, os.MkdirAll(noSentinelDir, 0o755))

	// Regular file in vms/ (not a directory) — should be skipped.
	require.NoError(t, os.WriteFile(filepath.Join(vmsDir, "stray-file"), []byte("x"), 0o600))

	CleanupStaleLogs(vmsDir, testLogger())

	_, err := os.Stat(staleDir)
	assert.True(t, os.IsNotExist(err), "stale directory with dead PID should be removed")

	_, err = os.Stat(liveDir)
	assert.NoError(t, err, "directory with live PID should remain")

	_, err = os.Stat(noSentinelDir)
	assert.NoError(t, err, "directory without sentinel should remain")

	_, err = os.Stat(filepath.Join(vmsDir, "stray-file"))
	assert.NoError(t, err, "non-directory entry should remain")
}

func TestCleanupStaleLogs_InvalidSentinelContent(t *testing.T) {
	t.Parallel()

	vmsDir := filepath.Join(t.TempDir(), "vms")
	require.NoError(t, os.MkdirAll(vmsDir, 0o755))

	tests := []struct {
		name    string
		content string
	}{
		{"empty sentinel", ""},
		{"non-numeric text", "not-a-pid"},
		{"negative PID", "-1"},
		{"zero PID", "0"},
		{"floating point", "123.456"},
		{"PID with trailing garbage", "123abc"},
	}

	for _, tt := range tests {
		dir := filepath.Join(vmsDir, tt.name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, LogSentinel), []byte(tt.content), 0o600))
	}

	CleanupStaleLogs(vmsDir, testLogger())

	// All directories with invalid sentinels should be preserved (not cleaned).
	for _, tt := range tests {
		dir := filepath.Join(vmsDir, tt.name)
		_, err := os.Stat(dir)
		assert.NoError(t, err, "directory with invalid sentinel %q should remain", tt.name)
	}
}

func TestCleanupStaleLogs_WhitespacePaddedSentinel(t *testing.T) {
	t.Parallel()

	vmsDir := filepath.Join(t.TempDir(), "vms")
	require.NoError(t, os.MkdirAll(vmsDir, 0o755))

	dir := filepath.Join(vmsDir, "whitespace-vm")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	// PID 2147483647 with leading/trailing whitespace and newline.
	require.NoError(t, os.WriteFile(filepath.Join(dir, LogSentinel), []byte("  2147483647\n"), 0o600))

	CleanupStaleLogs(vmsDir, testLogger())

	_, err := os.Stat(dir)
	assert.True(t, os.IsNotExist(err), "stale directory with whitespace-padded dead PID should be removed")
}

func TestCleanupStaleLogs_MultipleStaleDirectories(t *testing.T) {
	t.Parallel()

	vmsDir := filepath.Join(t.TempDir(), "vms")
	require.NoError(t, os.MkdirAll(vmsDir, 0o755))

	staleDirs := make([]string, 5)
	for i := range staleDirs {
		dir := filepath.Join(vmsDir, fmt.Sprintf("stale-%d", i))
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, LogSentinel), []byte("2147483647"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "broodbox.log"), []byte("log data"), 0o600))
		staleDirs[i] = dir
	}

	CleanupStaleLogs(vmsDir, testLogger())

	for _, dir := range staleDirs {
		_, err := os.Stat(dir)
		assert.True(t, os.IsNotExist(err), "stale directory %s should be removed", filepath.Base(dir))
	}
}

func TestCleanupStaleLogs_NestedDataSubdirectory(t *testing.T) {
	t.Parallel()

	vmsDir := filepath.Join(t.TempDir(), "vms")
	dir := filepath.Join(vmsDir, "nested-vm")
	dataDir := filepath.Join(dir, "data")
	require.NoError(t, os.MkdirAll(dataDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, LogSentinel), []byte("2147483647"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "broodbox.log"), []byte("log"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "state.json"), []byte("{}"), 0o600))

	CleanupStaleLogs(vmsDir, testLogger())

	_, err := os.Stat(dir)
	assert.True(t, os.IsNotExist(err), "entire directory tree should be removed")
}

func TestCleanupStaleLogs_EmptyVmsDir(t *testing.T) {
	t.Parallel()

	vmsDir := filepath.Join(t.TempDir(), "vms")
	require.NoError(t, os.MkdirAll(vmsDir, 0o755))

	// Should not panic or error on empty directory.
	CleanupStaleLogs(vmsDir, testLogger())
}

func TestCleanupStaleLogs_NonexistentVmsDir(t *testing.T) {
	t.Parallel()

	// Should not panic when vms/ doesn't exist at all.
	CleanupStaleLogs(filepath.Join(t.TempDir(), "nonexistent"), testLogger())
}

func TestWriteSentinel(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, WriteSentinel(dir))

	data, err := os.ReadFile(filepath.Join(dir, LogSentinel))
	require.NoError(t, err)

	// Sentinel format: `PID\nEXEPATH`. PID is on line 1.
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Equal(t, fmt.Sprintf("%d", os.Getpid()), lines[0])

	// os.Executable is expected to succeed in the test environment, so
	// the sentinel should carry an exe path fingerprint too.
	require.Len(t, lines, 2, "sentinel should carry PID + exe path")
	exe, err := os.Executable()
	require.NoError(t, err)
	assert.Equal(t, exe, lines[1])
}

func TestWriteSentinel_FilePermissions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, WriteSentinel(dir))

	info, err := os.Stat(filepath.Join(dir, LogSentinel))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"sentinel should be owner-only read/write")
}

func TestWriteSentinel_Overwrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// Write a sentinel with stale content.
	require.NoError(t, os.WriteFile(filepath.Join(dir, LogSentinel), []byte("99999"), 0o600))

	// Overwrite with current PID.
	require.NoError(t, WriteSentinel(dir))

	data, err := os.ReadFile(filepath.Join(dir, LogSentinel))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	assert.Equal(t, fmt.Sprintf("%d", os.Getpid()), lines[0],
		"sentinel should contain current PID after overwrite")
}

func TestWriteSentinel_NonexistentDirectory(t *testing.T) {
	t.Parallel()

	err := WriteSentinel(filepath.Join(t.TempDir(), "nonexistent"))
	assert.Error(t, err, "writing sentinel to nonexistent directory should fail")
}

func TestCleanupStaleLogs_FingerprintMismatchRemoved(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		// /proc/<pid>/exe is Linux-only; on darwin IsExpectedProcess
		// falls back to plain liveness and cannot detect PID reuse.
		t.Skip("fingerprint check is Linux-only")
	}

	vmsDir := filepath.Join(t.TempDir(), "vms")
	require.NoError(t, os.MkdirAll(vmsDir, 0o755))

	// Simulate PID reuse: the sentinel claims the current PID was bbox
	// with a fake exe path that /proc/self/exe will NOT match.
	// IsExpectedProcess returns false → directory is treated as stale.
	dir := filepath.Join(vmsDir, "recycled-pid")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	content := fmt.Sprintf("%d\n/nonexistent/fake-bbox-binary", os.Getpid())
	require.NoError(t, os.WriteFile(filepath.Join(dir, LogSentinel), []byte(content), 0o600))

	CleanupStaleLogs(vmsDir, testLogger())

	_, err := os.Stat(dir)
	assert.True(t, os.IsNotExist(err),
		"directory whose sentinel names a different binary should be removed")
}

func TestCleanupStaleLogs_FingerprintMatchPreserved(t *testing.T) {
	t.Parallel()

	vmsDir := filepath.Join(t.TempDir(), "vms")
	require.NoError(t, os.MkdirAll(vmsDir, 0o755))

	// Sentinel claims the current PID running the real test binary —
	// fingerprint matches (Linux) or legacy-liveness matches (darwin) —
	// directory must be preserved.
	exe, err := os.Executable()
	require.NoError(t, err)
	dir := filepath.Join(vmsDir, "live-bbox")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	content := fmt.Sprintf("%d\n%s", os.Getpid(), exe)
	require.NoError(t, os.WriteFile(filepath.Join(dir, LogSentinel), []byte(content), 0o600))

	CleanupStaleLogs(vmsDir, testLogger())

	_, err = os.Stat(dir)
	assert.NoError(t, err, "directory with matching fingerprint should remain")
}

func TestCleanupStaleLogs_OversizedSentinelSkipped(t *testing.T) {
	t.Parallel()

	vmsDir := filepath.Join(t.TempDir(), "vms")
	require.NoError(t, os.MkdirAll(vmsDir, 0o755))

	// Planted giant sentinel (8 KiB > 4 KiB cap). Cleanup must not
	// read it into memory and must leave the dir alone — the file is
	// suspicious and silently nuking would be worse than skipping.
	dir := filepath.Join(vmsDir, "giant-sentinel")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	huge := make([]byte, 8192)
	for i := range huge {
		huge[i] = '1'
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, LogSentinel), huge, 0o600))

	CleanupStaleLogs(vmsDir, testLogger())

	_, err := os.Stat(dir)
	assert.NoError(t, err, "directory with oversized sentinel should be left alone")
}

func TestCleanupStaleLogs_LegacyPIDOnlySentinelStillWorks(t *testing.T) {
	t.Parallel()

	vmsDir := filepath.Join(t.TempDir(), "vms")
	require.NoError(t, os.MkdirAll(vmsDir, 0o755))

	// Legacy v1 sentinel (just a PID, no exe path) written by an older
	// bbox build. Cleanup must fall back to a plain liveness check.

	// Live PID, legacy format — preserved.
	liveDir := filepath.Join(vmsDir, "live-legacy")
	require.NoError(t, os.MkdirAll(liveDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(liveDir, LogSentinel),
		[]byte(fmt.Sprintf("%d", os.Getpid())), 0o600))

	// Dead PID, legacy format — removed.
	staleDir := filepath.Join(vmsDir, "stale-legacy")
	require.NoError(t, os.MkdirAll(staleDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(staleDir, LogSentinel),
		[]byte("2147483647"), 0o600))

	CleanupStaleLogs(vmsDir, testLogger())

	_, err := os.Stat(liveDir)
	assert.NoError(t, err, "legacy sentinel with live PID should preserve directory")

	_, err = os.Stat(staleDir)
	assert.True(t, os.IsNotExist(err), "legacy sentinel with dead PID should remove directory")
}
