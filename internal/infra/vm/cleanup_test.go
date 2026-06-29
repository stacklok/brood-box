// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stacklok/go-microvm/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deadPID is a PID that is never alive in the test environment (max int32).
const deadPID = 2147483647

// writeVMState writes a go-microvm state file into <vmDir>/data using the
// canonical schema and filename, then creates a rootfs-work/ clone stand-in so
// removal can be observed.
func writeVMState(t *testing.T, vmDir string, active bool, pid int) (dataDir string) {
	t.Helper()
	dataDir = filepath.Join(vmDir, vmDataSubdir)
	require.NoError(t, os.MkdirAll(filepath.Join(dataDir, "rootfs-work"), 0o755))

	ls, err := state.NewManager(dataDir).LoadAndLock(context.Background())
	require.NoError(t, err)
	ls.State.Active = active
	ls.State.PID = pid
	require.NoError(t, ls.Save())
	ls.Release()
	return dataDir
}

// testLogger returns a logger that discards output unless tests are run with -v.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// writeSentinel writes a sentinel file (PID\nexe) into vmDir.
func writeSentinel(t *testing.T, vmDir string, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(vmDir, LogSentinel), []byte(content), 0o600))
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

// cleanupCase builds a single VM dir scenario and records the expectation.
type cleanupCase struct {
	name     string
	setup    func(t *testing.T, vmsDir string) (vmDir string, dataDir string)
	skipName string // name to pass as skipName; "" => none
	want     func(t *testing.T, vmDir, dataDir string)
}

func TestCleanupStaleVMDirs(t *testing.T) {
	t.Parallel()

	cases := []cleanupCase{
		{
			// --log-file case: active state, dead runner PID, no sentinel.
			name: "orphaned_active_state_dead_runner_no_sentinel",
			setup: func(t *testing.T, vmsDir string) (string, string) {
				vmDir := filepath.Join(vmsDir, "orphan-active")
				dataDir := writeVMState(t, vmDir, true, deadPID)
				return vmDir, dataDir
			},
			want: func(t *testing.T, vmDir, dataDir string) {
				_, err := os.Stat(vmDir)
				assert.True(t, os.IsNotExist(err), "orphaned vmDir should be removed")
			},
		},
		{
			// dead bbox sentinel + no state file → vmDir removed.
			name: "dead_sentinel_no_state",
			setup: func(t *testing.T, vmsDir string) (string, string) {
				vmDir := filepath.Join(vmsDir, "dead-sentinel")
				require.NoError(t, os.MkdirAll(vmDir, 0o755))
				writeSentinel(t, vmDir, fmt.Sprintf("%d", deadPID))
				require.NoError(t, os.WriteFile(filepath.Join(vmDir, "broodbox.log"), []byte("log"), 0o600))
				return vmDir, ""
			},
			want: func(t *testing.T, vmDir, dataDir string) {
				_, err := os.Stat(vmDir)
				assert.True(t, os.IsNotExist(err), "vmDir with dead sentinel should be removed")
			},
		},
		{
			// dead bbox sentinel (PID-only legacy) + inactive state (dead PID) → removed.
			name: "dead_sentinel_inactive_state",
			setup: func(t *testing.T, vmsDir string) (string, string) {
				vmDir := filepath.Join(vmsDir, "dead-sentinel-inactive")
				dataDir := writeVMState(t, vmDir, false, deadPID)
				writeSentinel(t, vmDir, fmt.Sprintf("%d", deadPID))
				return vmDir, dataDir
			},
			want: func(t *testing.T, vmDir, dataDir string) {
				_, err := os.Stat(vmDir)
				assert.True(t, os.IsNotExist(err), "vmDir with dead sentinel + inactive state should be removed")
			},
		},
		{
			// live bbox sentinel (current PID + real exe) → preserved.
			name: "live_bbox_sentinel_preserved",
			setup: func(t *testing.T, vmsDir string) (string, string) {
				vmDir := filepath.Join(vmsDir, "live-bbox")
				require.NoError(t, os.MkdirAll(vmDir, 0o755))
				exe, err := os.Executable()
				require.NoError(t, err)
				writeSentinel(t, vmDir, fmt.Sprintf("%d\n%s", os.Getpid(), exe))
				return vmDir, ""
			},
			want: func(t *testing.T, vmDir, dataDir string) {
				_, err := os.Stat(vmDir)
				assert.NoError(t, err, "vmDir with live bbox sentinel should be preserved")
			},
		},
		{
			// inactive state (Active=false, dead PID) + no sentinel → removed.
			// A state file is a signal of past bbox ownership even with no
			// sentinel, and the runner is not alive, so reclaim is correct.
			name: "inactive_state_no_sentinel_removed",
			setup: func(t *testing.T, vmsDir string) (string, string) {
				vmDir := filepath.Join(vmsDir, "inactive-no-sentinel")
				dataDir := writeVMState(t, vmDir, false, deadPID)
				return vmDir, dataDir
			},
			want: func(t *testing.T, vmDir, dataDir string) {
				_, err := os.Stat(vmDir)
				assert.True(t, os.IsNotExist(err), "vmDir with inactive state + no sentinel should be removed")
			},
		},
		{
			// no sentinel, no state file, just an empty dir → preserved.
			name: "empty_unrelated_dir_preserved",
			setup: func(t *testing.T, vmsDir string) (string, string) {
				vmDir := filepath.Join(vmsDir, "empty-unrelated")
				require.NoError(t, os.MkdirAll(vmDir, 0o755))
				return vmDir, ""
			},
			want: func(t *testing.T, vmDir, dataDir string) {
				_, err := os.Stat(vmDir)
				assert.NoError(t, err, "unrelated empty dir should be preserved")
			},
		},
		{
			// no sentinel, no state file, dir absent entirely → no panic.
			name: "nonexistent_vms_dir_no_panic",
			setup: func(t *testing.T, vmsDir string) (string, string) {
				return filepath.Join(vmsDir, "does-not-exist"), ""
			},
			want: func(t *testing.T, vmDir, dataDir string) {
				// Nothing to assert beyond not panicking.
			},
		},
		{
			// oversized state file → dir preserved (hardening).
			name: "oversized_state_file_preserved",
			setup: func(t *testing.T, vmsDir string) (string, string) {
				vmDir := filepath.Join(vmsDir, "oversized-state")
				dataDir := filepath.Join(vmDir, vmDataSubdir)
				require.NoError(t, os.MkdirAll(dataDir, 0o755))
				huge := make([]byte, maxStateSize+1)
				for i := range huge {
					huge[i] = '1'
				}
				require.NoError(t, os.WriteFile(filepath.Join(dataDir, "go-microvm-state.json"), huge, 0o600))
				return vmDir, dataDir
			},
			want: func(t *testing.T, vmDir, dataDir string) {
				_, err := os.Stat(vmDir)
				assert.NoError(t, err, "vmDir with oversized state file should be preserved")
			},
		},
		{
			// skipName equals the vmDir → preserved even if it would otherwise look orphaned.
			name: "skip_name_preserved",
			setup: func(t *testing.T, vmsDir string) (string, string) {
				vmDir := filepath.Join(vmsDir, "current-run")
				dataDir := writeVMState(t, vmDir, true, deadPID)
				return vmDir, dataDir
			},
			skipName: "current-run",
			want: func(t *testing.T, vmDir, dataDir string) {
				_, err := os.Stat(vmDir)
				assert.NoError(t, err, "current run's vmDir (skipName) should be preserved")
			},
		},
		{
			// state Active=true with a dead runner PID → reclaimed on all platforms.
			name: "active_state_dead_pid_removed_all_platforms",
			setup: func(t *testing.T, vmsDir string) (string, string) {
				vmDir := filepath.Join(vmsDir, "active-dead-pid")
				dataDir := writeVMState(t, vmDir, true, deadPID)
				return vmDir, dataDir
			},
			want: func(t *testing.T, vmDir, dataDir string) {
				_, err := os.Stat(vmDir)
				assert.True(t, os.IsNotExist(err), "vmDir with active state + dead runner PID should be removed")
			},
		},
	}

	for _, tt := range cases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			vmsDir := filepath.Join(t.TempDir(), "vms")
			require.NoError(t, os.MkdirAll(vmsDir, 0o755))

			vmDir, dataDir := tt.setup(t, vmsDir)
			CleanupStaleVMDirs(vmsDir, tt.skipName, testLogger())
			tt.want(t, vmDir, dataDir)
		})
	}
}

// TestCleanupStaleVMDirs_FingerprintMismatchRemoved exercises the cross-confirmed
// ordering path: a dead bbox sentinel + state Active=true with a LIVE runner
// PID (os.Getpid) whose exe does NOT match "go-microvm-runner". On Linux this
// is the realistic recycled-PID case (live PID running the wrong binary →
// treated as NOT the runner → reclaimed). On darwin IsExpectedProcess falls
// back to IsAlive, so the live PID IS treated as alive → preserved.
func TestCleanupStaleVMDirs_FingerprintMismatchRemoved(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("fingerprint check is Linux-only; on darwin IsExpectedProcess falls back to IsAlive")
	}

	vmsDir := filepath.Join(t.TempDir(), "vms")
	require.NoError(t, os.MkdirAll(vmsDir, 0o755))

	vmDir := filepath.Join(vmsDir, "recycled-runner-pid")
	dataDir := writeVMState(t, vmDir, true, os.Getpid())
	// Dead bbox sentinel so the bbox owner is gone; the only thing standing
	// between this dir and removal is the runner fingerprint check.
	writeSentinel(t, vmDir, fmt.Sprintf("%d", deadPID))

	CleanupStaleVMDirs(vmsDir, "", testLogger())

	_, err := os.Stat(vmDir)
	assert.True(t, os.IsNotExist(err),
		"vmDir whose runner state names a live PID running the wrong binary should be removed (Linux)")
	_ = dataDir
}

// TestCleanupStaleVMDirs_PreservesUnrelatedStateFile verifies that a state
// file with Active=false and a live runner PID is NOT preserved just because
// the PID is live — Active=false means clean shutdown, so the dir is reclaimed.
// This documents the decision logic for the inactive-state + no-sentinel case.
func TestCleanupStaleVMDirs_InactiveStateLivePID(t *testing.T) {
	t.Parallel()

	vmsDir := filepath.Join(t.TempDir(), "vms")
	require.NoError(t, os.MkdirAll(vmsDir, 0o755))

	vmDir := filepath.Join(vmsDir, "inactive-live-pid")
	dataDir := writeVMState(t, vmDir, false, os.Getpid())

	CleanupStaleVMDirs(vmsDir, "", testLogger())

	_, err := os.Stat(vmDir)
	assert.True(t, os.IsNotExist(err),
		"vmDir with inactive state is not protected by a live PID (clean shutdown) and should be removed")
	_ = dataDir
}

// TestCleanupStaleVMDirs_HeldLockSkipped verifies Fix 3 (TOCTOU/locking):
// when another goroutine holds the state lock, CleanupStaleVMDirs treats the
// dir as live and skips it rather than removing it out from under the lock
// holder.
func TestCleanupStaleVMDirs_HeldLockSkipped(t *testing.T) {
	t.Parallel()

	vmsDir := filepath.Join(t.TempDir(), "vms")
	require.NoError(t, os.MkdirAll(vmsDir, 0o755))

	vmDir := filepath.Join(vmsDir, "locked-vm")
	dataDir := writeVMState(t, vmDir, true, deadPID)

	// Hold the state lock from another goroutine for the duration of the sweep.
	ls, err := state.NewManager(dataDir).LoadAndLock(context.Background())
	require.NoError(t, err)
	defer ls.Release()

	// Give the sweep a short retry window so the lock contention is real but
	// the test stays fast. CleanupStaleVMDirs uses stateLockTimeout internally.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		CleanupStaleVMDirs(vmsDir, "", testLogger())
	}()
	wg.Wait()

	_, err = os.Stat(vmDir)
	assert.NoError(t, err,
		"vmDir with a held state lock must be skipped (live owner) and preserved")
}

// TestCleanupStaleVMDirs_OversizedSentinelSkipped mirrors the original
// sentinel-cap hardening under the combined sweep: a planted giant sentinel
// must not be read into memory and must leave the dir alone.
func TestCleanupStaleVMDirs_OversizedSentinelSkipped(t *testing.T) {
	t.Parallel()

	vmsDir := filepath.Join(t.TempDir(), "vms")
	require.NoError(t, os.MkdirAll(vmsDir, 0o755))

	vmDir := filepath.Join(vmsDir, "giant-sentinel")
	require.NoError(t, os.MkdirAll(vmDir, 0o755))
	huge := make([]byte, maxSentinelSize*2)
	for i := range huge {
		huge[i] = '1'
	}
	require.NoError(t, os.WriteFile(filepath.Join(vmDir, LogSentinel), huge, 0o600))

	CleanupStaleVMDirs(vmsDir, "", testLogger())

	_, err := os.Stat(vmDir)
	assert.NoError(t, err, "vmDir with oversized sentinel should be left alone")
}

// TestCleanupStaleVMDirs_InvalidSentinelContent verifies that invalid sentinel
// contents do not cause the dir to be removed (no live owner signal from the
// sentinel → falls through to runner check; with no state, preserved as
// unrelated — except the sentinel IS present, so the bbox-artifacts guard
// passes; but bboxAlive=false and no runner, so it WOULD be reclaimed).
//
// Wait: per the decision logic, an unparseable sentinel counts as "present"
// (sentinelPresent=true) and bboxAlive=false. With no state file and
// sentinelPresent=true, the bbox-artifacts guard passes and the dir IS
// reclaimed. This test documents that behavior: invalid sentinel content is
// treated as a stale/abandoned marker and the dir is removed.
func TestCleanupStaleVMDirs_InvalidSentinelContent(t *testing.T) {
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
		vmDir := filepath.Join(vmsDir, tt.name)
		require.NoError(t, os.MkdirAll(vmDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(vmDir, LogSentinel), []byte(tt.content), 0o600))
	}

	CleanupStaleVMDirs(vmsDir, "", testLogger())

	for _, tt := range tests {
		vmDir := filepath.Join(vmsDir, tt.name)
		_, err := os.Stat(vmDir)
		// An invalid sentinel is treated as a present-but-no-live-owner
		// marker: bboxAlive=false, sentinelPresent=true. With no runner
		// state, the bbox-artifacts guard passes and the dir is reclaimed.
		assert.True(t, os.IsNotExist(err),
			"vmDir with invalid sentinel %q should be removed (stale marker)", tt.name)
	}
}

// TestCleanupStaleVMDirs_EmptyVmsDir ensures no panic on an empty vms dir.
func TestCleanupStaleVMDirs_EmptyVmsDir(t *testing.T) {
	t.Parallel()

	vmsDir := filepath.Join(t.TempDir(), "vms")
	require.NoError(t, os.MkdirAll(vmsDir, 0o755))

	// Should not panic or error on empty directory.
	CleanupStaleVMDirs(vmsDir, "", testLogger())
}

// TestCleanupStaleVMDirs_NonexistentVmsDir ensures no panic when vms/ doesn't
// exist at all.
func TestCleanupStaleVMDirs_NonexistentVmsDir(t *testing.T) {
	t.Parallel()

	CleanupStaleVMDirs(filepath.Join(t.TempDir(), "nonexistent"), "", testLogger())
}

// TestCleanupStaleVMDirs_WhitespacePaddedSentinel verifies that whitespace-
// padded dead-PID sentinels are trimmed and the dir is removed.
func TestCleanupStaleVMDirs_WhitespacePaddedSentinel(t *testing.T) {
	t.Parallel()

	vmsDir := filepath.Join(t.TempDir(), "vms")
	require.NoError(t, os.MkdirAll(vmsDir, 0o755))

	vmDir := filepath.Join(vmsDir, "whitespace-vm")
	require.NoError(t, os.MkdirAll(vmDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(vmDir, LogSentinel), []byte("  2147483647\n"), 0o600))

	CleanupStaleVMDirs(vmsDir, "", testLogger())

	_, err := os.Stat(vmDir)
	assert.True(t, os.IsNotExist(err), "vmDir with whitespace-padded dead PID sentinel should be removed")
}

// TestCleanupStaleVMDirs_MultipleStaleDirectories verifies that multiple
// orphaned dirs are all removed in one sweep.
func TestCleanupStaleVMDirs_MultipleStaleDirectories(t *testing.T) {
	t.Parallel()

	vmsDir := filepath.Join(t.TempDir(), "vms")
	require.NoError(t, os.MkdirAll(vmsDir, 0o755))

	staleDirs := make([]string, 5)
	for i := range staleDirs {
		vmDir := filepath.Join(vmsDir, fmt.Sprintf("stale-%d", i))
		require.NoError(t, os.MkdirAll(vmDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(vmDir, LogSentinel), []byte("2147483647"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(vmDir, "broodbox.log"), []byte("log data"), 0o600))
		staleDirs[i] = vmDir
	}

	CleanupStaleVMDirs(vmsDir, "", testLogger())

	for _, vmDir := range staleDirs {
		_, err := os.Stat(vmDir)
		assert.True(t, os.IsNotExist(err), "stale vmDir %s should be removed", filepath.Base(vmDir))
	}
}

// TestCleanupStaleVMDirs_NestedDataSubdirectory verifies the whole vmDir tree
// (data subdir included) is removed when the bbox sentinel is stale.
func TestCleanupStaleVMDirs_NestedDataSubdirectory(t *testing.T) {
	t.Parallel()

	vmsDir := filepath.Join(t.TempDir(), "vms")
	vmDir := filepath.Join(vmsDir, "nested-vm")
	dataDir := filepath.Join(vmDir, "data")
	require.NoError(t, os.MkdirAll(dataDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(vmDir, LogSentinel), []byte("2147483647"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(vmDir, "broodbox.log"), []byte("log"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dataDir, "state.json"), []byte("{}"), 0o600))

	CleanupStaleVMDirs(vmsDir, "", testLogger())

	_, err := os.Stat(vmDir)
	assert.True(t, os.IsNotExist(err), "entire vmDir tree should be removed")
}

// TestCleanupStaleVMDirs_FingerprintMatchPreserved verifies that a live bbox
// sentinel with a matching exe fingerprint preserves the dir even when there
// is also state pointing at a dead runner.
func TestCleanupStaleVMDirs_FingerprintMatchPreserved(t *testing.T) {
	t.Parallel()

	vmsDir := filepath.Join(t.TempDir(), "vms")
	require.NoError(t, os.MkdirAll(vmsDir, 0o755))

	vmDir := filepath.Join(vmsDir, "live-bbox-fingerprint")
	dataDir := writeVMState(t, vmDir, true, deadPID)
	exe, err := os.Executable()
	require.NoError(t, err)
	writeSentinel(t, vmDir, fmt.Sprintf("%d\n%s", os.Getpid(), exe))

	CleanupStaleVMDirs(vmsDir, "", testLogger())

	_, err = os.Stat(vmDir)
	assert.NoError(t, err, "vmDir with matching bbox fingerprint should remain")
	_ = dataDir
}

// TestCleanupStaleVMDirs_LegacyPIDOnlySentinelStillWorks verifies the legacy
// single-line sentinel format falls back to plain liveness.
func TestCleanupStaleVMDirs_LegacyPIDOnlySentinelStillWorks(t *testing.T) {
	t.Parallel()

	vmsDir := filepath.Join(t.TempDir(), "vms")
	require.NoError(t, os.MkdirAll(vmsDir, 0o755))

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

	CleanupStaleVMDirs(vmsDir, "", testLogger())

	_, err := os.Stat(liveDir)
	assert.NoError(t, err, "legacy sentinel with live PID should preserve directory")

	_, err = os.Stat(staleDir)
	assert.True(t, os.IsNotExist(err), "legacy sentinel with dead PID should remove directory")
}

// TestStateFileOversize_Direct exercises the helper directly.
func TestStateFileOversize_Direct(t *testing.T) {
	t.Parallel()

	t.Run("under_limit", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "go-microvm-state.json"),
			make([]byte, maxStateSize-1), 0o600))
		assert.False(t, stateFileOversize(dir, testLogger()))
	})

	t.Run("over_limit", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "go-microvm-state.json"),
			make([]byte, maxStateSize+1), 0o600))
		assert.True(t, stateFileOversize(dir, testLogger()))
	})

	t.Run("exactly_limit", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, "go-microvm-state.json"),
			make([]byte, maxStateSize), 0o600))
		assert.False(t, stateFileOversize(dir, testLogger()))
	})

	t.Run("nonexistent_dir", func(t *testing.T) {
		t.Parallel()
		assert.False(t, stateFileOversize(filepath.Join(t.TempDir(), "nope"), testLogger()))
	})
}
