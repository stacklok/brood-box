// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"log/slog"
	"os"
)

// bestEffortLchown attempts os.Lchown and silently ignores permission errors.
// On macOS non-root users cannot chown to a different UID; the guest init
// will fix ownership at boot time. Lchown is used instead of Chown to avoid
// following symlinks in the rootfs.
//
// Mirrors configio.BestEffortLchown — kept here because internal/infra/vm/
// cannot import pkg/clients/internal/configio/.
func bestEffortLchown(path string, uid, gid int) error {
	if err := os.Lchown(path, uid, gid); err != nil {
		if !os.IsPermission(err) {
			slog.Warn("lchown failed", "path", path, "uid", uid, "gid", gid, "err", err)
		}
	}
	return nil
}
