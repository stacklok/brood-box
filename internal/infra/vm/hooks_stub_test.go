// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build !bbox_full

package vm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Without the bbox_full build tag the bbox-init binary is not embedded, so the
// hook must fail fast with an actionable error rather than writing a zero-byte
// init that would break VM boot.
func TestInjectInitBinaryStubReturnsError(t *testing.T) {
	t.Parallel()

	rootfs := t.TempDir()
	hook := InjectInitBinary()
	err := hook(rootfs, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bbox_full")

	_, statErr := os.Stat(filepath.Join(rootfs, "bbox-init"))
	assert.True(t, os.IsNotExist(statErr), "no init file should be written in stub builds")
}
