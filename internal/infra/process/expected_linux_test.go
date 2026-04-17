// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package process

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsExpectedProcess_SelfAbsolutePath(t *testing.T) {
	t.Parallel()

	exe, err := os.Executable()
	require.NoError(t, err)

	assert.True(t, IsExpectedProcess(os.Getpid(), exe),
		"IsExpectedProcess(self, self-exe) must return true")
}

func TestIsExpectedProcess_SelfBaseNameFallback(t *testing.T) {
	t.Parallel()

	exe, err := os.Executable()
	require.NoError(t, err)

	// Base-name-only comparison path: if the stored binary is not
	// absolute, we accept any process whose base name matches.
	assert.True(t, IsExpectedProcess(os.Getpid(), filepath.Base(exe)),
		"IsExpectedProcess with bare base name should match self")
}

func TestIsExpectedProcess_WrongBinary(t *testing.T) {
	t.Parallel()

	assert.False(t, IsExpectedProcess(os.Getpid(), "/nonexistent/not-bbox"),
		"IsExpectedProcess with wrong absolute path must return false")
}

func TestIsExpectedProcess_NonExistentPID(t *testing.T) {
	t.Parallel()

	// PID 2147483647 is MAX_INT32 — extremely unlikely to be alive.
	assert.False(t, IsExpectedProcess(2147483647, "/any/path"),
		"IsExpectedProcess must return false for a non-existent PID")
}

func TestIsExpectedProcess_InvalidInputs(t *testing.T) {
	t.Parallel()

	assert.False(t, IsExpectedProcess(0, "/any/path"))
	assert.False(t, IsExpectedProcess(-1, "/any/path"))
	assert.False(t, IsExpectedProcess(os.Getpid(), ""))
}
