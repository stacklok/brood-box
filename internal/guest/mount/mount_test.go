// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package mount

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEssentialRequiresRoot(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test must run as non-root")
	}
	err := Essential()
	assert.Error(t, err)
}

func TestWorkspaceReturnsErrorForInvalidMount(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test must run as non-root")
	}
	err := Workspace(t.TempDir()+"/ws", "nonexistent-tag", 1)
	assert.Error(t, err)
}
