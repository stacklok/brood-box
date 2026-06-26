// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build bbox_full

package vm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/brood-box/internal/infra/vm/initbin"
)

func TestInjectInitBinaryWritesCorrectContent(t *testing.T) {
	t.Parallel()

	rootfs := t.TempDir()
	hook := InjectInitBinary()
	err := hook(rootfs, nil)
	require.NoError(t, err)

	initPath := filepath.Join(rootfs, "bbox-init")
	data, err := os.ReadFile(initPath)
	require.NoError(t, err)
	assert.Equal(t, initbin.Binary, data)
}

func TestInjectInitBinaryPermissions(t *testing.T) {
	t.Parallel()

	rootfs := t.TempDir()
	hook := InjectInitBinary()
	err := hook(rootfs, nil)
	require.NoError(t, err)

	initPath := filepath.Join(rootfs, "bbox-init")
	info, err := os.Stat(initPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}
