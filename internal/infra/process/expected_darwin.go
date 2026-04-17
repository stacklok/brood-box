// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build darwin

package process

// IsExpectedProcess on Darwin falls back to a plain liveness check.
// macOS does not expose /proc, and reading another process's exe path
// requires libproc/proc_pidpath via cgo — outside the scope of this
// defensive cleanup helper. The tradeoff matches go-microvm's own
// procutil.IsExpectedProcess on darwin: stale-cleanup on macOS is not
// fully guarded against PID reuse.
func IsExpectedProcess(pid int, _ string) bool {
	if pid <= 0 {
		return false
	}
	return IsAlive(pid)
}
