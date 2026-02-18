// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package workspace

import "context"

// SnapshotPostProcessor runs a transformation on a workspace snapshot
// after it has been created but before the VM is started.
//
// The originalPath parameter points to the real workspace — needed when
// a post-processor must read files excluded from the snapshot (e.g.,
// .git/config is a security pattern excluded from the snapshot, but the
// sanitizer needs to read it from the original workspace).
type SnapshotPostProcessor interface {
	Process(ctx context.Context, originalPath, snapshotPath string) error
}
