// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/sandbox-agent/internal/infra/vm/initbin"
)

func TestInjectInitBinaryWritesCorrectContent(t *testing.T) {
	t.Parallel()

	rootfs := t.TempDir()
	hook := InjectInitBinary()
	err := hook(rootfs, nil)
	require.NoError(t, err)

	initPath := filepath.Join(rootfs, "sandbox-init")
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

	initPath := filepath.Join(rootfs, "sandbox-init")
	info, err := os.Stat(initPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

func TestInjectEnvFileRestrictedPermissions(t *testing.T) {
	t.Parallel()

	rootfs := t.TempDir()
	envVars := map[string]string{"SECRET_KEY": "s3cret"}
	hook := InjectEnvFile(envVars)
	err := hook(rootfs, nil)
	require.NoError(t, err)

	envPath := filepath.Join(rootfs, "etc", "sandbox-env")
	info, err := os.Stat(envPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
