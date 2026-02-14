// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package network

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfigureRequiresRoot(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test must run as non-root")
	}
	err := Configure()
	assert.Error(t, err)
}
