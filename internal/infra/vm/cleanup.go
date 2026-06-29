// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stacklok/go-microvm/state"

	"github.com/stacklok/brood-box/internal/infra/process"
)

// LogSentinel is the marker file placed inside per-VM log directories
// to identify ownership by a running bbox process.
const LogSentinel = ".bbox-sentinel"

// maxSentinelSize is a generous upper bound on the sentinel file
// size. A well-formed sentinel is ~32 bytes (PID + newline + exe
// path); 4 KiB is plenty and prevents a same-UID attacker from
// forcing bbox to load a multi-GiB file into memory at startup by
// planting a giant sentinel.
const maxSentinelSize = 4096

// maxStateSize is a generous upper bound on any single file inside a
// VM data directory. A well-formed go-microvm state file is a few
// hundred bytes; 4 KiB caps a planted giant (CWE-400 hardening) and
// mirrors the sentinel discipline, applied to LoadAndLock's unbounded
// os.ReadFile underpinnings.
const maxStateSize = 4096

// runnerBinaryName is the base name of the go-microvm runner process.
// It must match the constant go-microvm's own terminateStaleRunner
// keys off so that this cleanup and the upstream one agree on the
// runner fingerprint.
const runnerBinaryName = "go-microvm-runner"

// stateLockTimeout bounds how long CleanupStaleVMDirs will wait to
// acquire a VM state lock before treating the dir as live (a live
// process holds the lock → owner is alive → skip).
const stateLockTimeout = 2 * time.Second

// parseSentinel decodes the sentinel file contents. The current format
// is two lines: PID\nEXEPATH. The previous format was a single PID.
// Both are accepted; a missing exe path means "caller should use a
// plain liveness check for backward compatibility".
func parseSentinel(data []byte) (pid int, exePath string, ok bool) {
	content := strings.TrimSpace(string(data))
	if content == "" {
		return 0, "", false
	}
	lines := strings.SplitN(content, "\n", 2)
	p, err := strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil || p <= 0 {
		return 0, "", false
	}
	if len(lines) > 1 {
		exePath = strings.TrimSpace(lines[1])
	}
	return p, exePath, true
}

// CleanupStaleVMDirs removes orphaned per-VM directories (logs + COW rootfs
// clones) left behind when a VM crashed or its bbox process was killed
// (SIGKILL, OOM) before WithCleanDataDir() could run.
//
// A vmDir is reclaimed only when NEITHER owner is alive:
//   - the bbox process (identified by the sentinel PID + exe fingerprint), AND
//   - the go-microvm-runner process (identified by the state file's PID,
//     fingerprinted against "go-microvm-runner").
//
// The runner is spawned with Setsid:true, so "bbox died, detached runner +
// VM still alive" leaves a dead bbox sentinel with a LIVE runner PID. The
// combined check prevents removing data/rootfs-work/ a live VM is actively
// serving — the ordering hole that existed when CleanupStaleLogs ran first
// and recursively removed the whole vmDir before the runner-aware check.
//
// skipName is the current run's VM directory name and is never touched,
// which makes the sweep safe to run concurrently with a freshly booted VM
// (VM names are unique per session).
func CleanupStaleVMDirs(vmsDir string, skipName string, logger *slog.Logger) {
	entries, err := os.ReadDir(vmsDir)
	if err != nil {
		// Directory may not exist yet on first run — not an error.
		if os.IsNotExist(err) {
			return
		}
		logger.Warn("failed to scan for stale VM directories", "error", err)
		return
	}

	for _, entry := range entries {
		name := entry.Name()
		if name == skipName {
			continue
		}
		if !entry.IsDir() {
			continue
		}

		vmDir := filepath.Join(vmsDir, name)
		reclaimVMDir(vmDir, logger)
	}
}

// reclaimVMDir decides whether a single vmDir is orphaned and removes it
// if so. It is the combined bbox-sentinel + runner-state decision described
// on CleanupStaleVMDirs.
func reclaimVMDir(vmDir string, logger *slog.Logger) {
	sentinelPath := filepath.Join(vmDir, LogSentinel)

	// --- bbox owner -------------------------------------------------------
	bboxAlive := false
	sentinelPresent := false
	data, sentinelErr := readSentinelFile(sentinelPath)
	if sentinelErr == nil {
		sentinelPresent = true
		pid, exePath, ok := parseSentinel(data)
		if ok {
			if exePath != "" {
				bboxAlive = process.IsExpectedProcess(pid, exePath)
			} else {
				bboxAlive = process.IsAlive(pid)
			}
		}
		// An unparseable sentinel counts as "present but no live owner".
	} else if !os.IsNotExist(sentinelErr) {
		// Unreadable for unexpected reasons (permission denied, oversize).
		// Be conservative: don't reclaim something we can't inspect.
		logger.Debug("skipping VM dir with unreadable sentinel",
			"path", vmDir, "error", sentinelErr)
		return
	}

	// --- runner owner -----------------------------------------------------
	dataDir := filepath.Join(vmDir, vmDataSubdir)
	runnerAlive := false

	// Avoid the MkdirAll side effect of LoadAndLock when there is nothing
	// to load: stat the dataDir first. If it doesn't exist, there is no
	// runner state and no runner owner.
	if info, err := os.Stat(dataDir); err == nil && info.IsDir() {
		if stateFileOversize(dataDir, logger) {
			// Suspicious oversized file — same discipline as the sentinel
			// path: don't read it into memory, leave the dir alone.
			logger.Debug("skipping VM dir with oversized state file", "path", vmDir)
			return
		}

		ls, lockErr := state.NewManager(dataDir).LoadAndLockWithRetry(context.Background(), stateLockTimeout)
		if lockErr != nil {
			// A live process holds the lock → that process is the owner.
			// Treat as live and skip rather than racing it.
			logger.Debug("skipping VM dir with held state lock (runner alive)",
				"path", vmDir, "error", lockErr)
			return
		}
		// Hold the lock across the decision + removal so no runner can
		// start serving the dir between our check and our RemoveAll.
		defer ls.Release()

		st := ls.State
		if st.Active && st.PID > 0 {
			runnerAlive = process.IsExpectedProcess(st.PID, runnerBinaryName)
		}
		// If !st.Active or PID <= 0: clean shutdown — runner not an owner.

		if bboxAlive || runnerAlive {
			logger.Debug("skipping VM dir with live owner",
				"path", vmDir, "bbox_alive", bboxAlive, "runner_alive", runnerAlive)
			return
		}

		// A loadable state file is itself a signal of past bbox ownership,
		// so the bbox-artifacts guard below is satisfied; reclaim while
		// holding the lock. The flock is on an open fd; unlinking the lock
		// file from under a held flock is fine — the lock persists until
		// Release closes the fd.
		reclaim(vmDir, logger, "orphaned VM directory")
		return
	}

	// No dataDir: only the sentinel path had a say.
	if bboxAlive {
		logger.Debug("skipping VM dir with live bbox sentinel",
			"path", vmDir)
		return
	}
	if !sentinelPresent {
		// No bbox artifacts at all — preserve unrelated directories.
		logger.Debug("skipping VM dir without bbox artifacts", "path", vmDir)
		return
	}
	reclaim(vmDir, logger, "orphaned VM directory (no runner state)")
}

// reclaim removes vmDir and logs the outcome.
func reclaim(vmDir string, logger *slog.Logger, reason string) {
	logger.Warn("removing stale VM directory", "path", vmDir, "reason", reason)
	if err := os.RemoveAll(vmDir); err != nil {
		logger.Error("failed to remove stale VM directory", "path", vmDir, "error", err)
	}
}

// stateFileOversize reports whether any regular file in dataDir exceeds
// maxStateSize. It guards the unbounded os.ReadFile that LoadAndLock
// performs against a planted giant state file (CWE-400).
func stateFileOversize(dataDir string, logger *slog.Logger) bool {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		// Let LoadAndLock surface the real error; treat as not-oversize.
		return false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.Size() > maxStateSize {
			logger.Debug("oversized file in VM data dir",
				"path", filepath.Join(dataDir, e.Name()),
				"size", info.Size(), "limit", maxStateSize)
			return true
		}
	}
	return false
}

// readSentinelFile reads a sentinel with a size cap. Returns an error
// wrapping the underlying read error for permission-denied / I/O
// failures, and a specific "too large" error when the file exceeds
// maxSentinelSize (defensive cap against a planted giant sentinel).
func readSentinelFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > maxSentinelSize {
		return nil, fmt.Errorf("sentinel %s too large: %d bytes (limit %d)",
			path, info.Size(), maxSentinelSize)
	}
	return io.ReadAll(io.LimitReader(f, maxSentinelSize))
}

// WriteSentinel writes a PID+exe-path sentinel file into the given
// directory to mark ownership by the current process. The exe path
// lets future CleanupStaleVMDirs invocations distinguish "bbox still
// alive at pid X" from "pid X has been recycled onto some unrelated
// process" — a kill -0 liveness check alone cannot tell those apart.
func WriteSentinel(dir string) error {
	sentinelPath := filepath.Join(dir, LogSentinel)
	// os.Executable returns the path to the currently running bbox.
	// Failure is tolerated: we simply fall back to a PID-only sentinel
	// and the cleanup path will use plain liveness. This matches the
	// behavior of installations where /proc is unavailable.
	exe, _ := os.Executable()
	var content string
	if exe != "" {
		content = fmt.Sprintf("%d\n%s", os.Getpid(), exe)
	} else {
		content = fmt.Sprintf("%d", os.Getpid())
	}
	if err := os.WriteFile(sentinelPath, []byte(content), 0o600); err != nil {
		return fmt.Errorf("writing log sentinel: %w", err)
	}
	return nil
}
