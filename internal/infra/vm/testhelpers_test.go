// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// chownCall records a single chown invocation made by recordingChown.
type chownCall struct {
	Path string
	UID  int
	GID  int
}

// recordingChown returns a ChownFunc that records calls and a function
// to retrieve the recorded calls. Safe for concurrent use.
func recordingChown() (ChownFunc, func() []chownCall) {
	var mu sync.Mutex
	var calls []chownCall
	fn := func(path string, uid, gid int) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, chownCall{Path: path, UID: uid, GID: gid})
		return nil
	}
	get := func() []chownCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]chownCall{}, calls...)
	}
	return fn, get
}

// setupRootfs creates a minimal rootfs with /home/sandbox/ pre-created,
// mimicking what the OCI image extraction provides.
func setupRootfs(t *testing.T) string {
	t.Helper()
	rootfs := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(rootfs, sandboxHome), 0o755))
	return rootfs
}
