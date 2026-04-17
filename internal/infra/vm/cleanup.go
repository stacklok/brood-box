// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

// CleanupStaleLogs removes orphaned per-VM log directories from previous
// crashes. It scans vmsDir for subdirectories with a sentinel file whose
// owning process has died or whose PID has been recycled onto a different
// binary since the sentinel was written.
func CleanupStaleLogs(vmsDir string, logger *slog.Logger) {
	entries, err := os.ReadDir(vmsDir)
	if err != nil {
		// Directory may not exist yet on first run — not an error.
		if os.IsNotExist(err) {
			return
		}
		logger.Warn("failed to scan for stale VM log directories", "error", err)
		return
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dirPath := filepath.Join(vmsDir, entry.Name())

		// Only remove directories that have our sentinel file to avoid
		// deleting unrelated directories.
		sentinelPath := filepath.Join(dirPath, LogSentinel)
		data, err := readSentinelFile(sentinelPath)
		if err != nil {
			if os.IsNotExist(err) {
				logger.Debug("skipping VM directory without sentinel", "path", dirPath)
			} else {
				// Permission denied, oversize, or other unexpected I/O
				// errors deserve visibility distinct from the "no
				// sentinel at all" case — helps triage on shared hosts.
				logger.Debug("skipping VM directory with unreadable sentinel",
					"path", dirPath, "error", err)
			}
			continue
		}

		pid, exePath, ok := parseSentinel(data)
		if !ok {
			logger.Debug("skipping VM directory with invalid sentinel", "path", dirPath)
			continue
		}

		// Prefer the fingerprint-aware check when the sentinel carries
		// an exe path: it correctly distinguishes a recycled PID (now
		// hosting an unrelated process) from a still-running bbox. Fall
		// back to plain liveness for legacy single-line sentinels.
		var ownerLive bool
		var reason string
		if exePath != "" {
			ownerLive = process.IsExpectedProcess(pid, exePath)
			reason = "fingerprint-mismatch-or-dead"
		} else {
			ownerLive = process.IsAlive(pid)
			reason = "pid-dead"
		}

		if ownerLive {
			logger.Debug("skipping VM log directory owned by running process",
				"path", dirPath, "pid", pid)
			continue
		}

		logger.Warn("removing stale VM log directory",
			"path", dirPath, "pid", pid, "reason", reason)
		if err := os.RemoveAll(dirPath); err != nil {
			logger.Error("failed to remove stale VM log directory", "path", dirPath, "error", err)
		}
	}
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
// lets future CleanupStaleLogs invocations distinguish "bbox still
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
