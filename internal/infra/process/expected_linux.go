// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package process

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// IsExpectedProcess reports whether the process at pid is running the
// expected binary. On Linux this reads /proc/<pid>/exe (a symlink to
// the running binary) and compares against expectedBinary.
//
// When expectedBinary is an absolute path, the comparison is against
// the full resolved exe path — the strong guarantee. When it is a
// bare name, the comparison falls back to base-name equality; two
// unrelated binaries with the same base name on the same host would
// collide under the fallback, so callers should pass the absolute
// path they got from os.Executable() when possible.
//
// The "(deleted)" suffix that the kernel appends when the underlying
// binary has been unlinked post-exec is stripped, so a bbox still
// running from an upgraded-away binary is correctly identified.
//
// Returns false when the process does not exist, is owned by another
// user (permission denied on the symlink read), or runs a different
// binary. A `false` result means "this PID is not the bbox we
// remembered" — the caller should treat its sentinel as stale.
func IsExpectedProcess(pid int, expectedBinary string) bool {
	if pid <= 0 || expectedBinary == "" {
		return false
	}
	exePath, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return false
	}
	exePath = strings.TrimSuffix(exePath, " (deleted)")
	if filepath.IsAbs(expectedBinary) {
		return filepath.Clean(exePath) == filepath.Clean(expectedBinary)
	}
	return filepath.Base(exePath) == filepath.Base(expectedBinary)
}
