// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"os"

	"github.com/stacklok/brood-box/pkg/domain/agent"
)

// sandboxHome is the home directory of the sandbox user inside the guest.
// Config files are written here because /workspace is mounted via virtiofs
// and would shadow anything written into the rootfs at that path.
const sandboxHome = "home/sandbox"

// Sandbox uid/gid used to chown injected files. Kept in sync with the guest
// init code in go-microvm's guest/ tree.
const (
	sandboxUID = 1000
	sandboxGID = 1000
)

// ChownFunc abstracts file ownership changes for testability. It is an
// alias for agent.ChownFunc so vm-internal hooks (gitconfig, knownhosts,
// credentials) and the cross-layer MCP injectors agree on the type.
type ChownFunc = agent.ChownFunc

// mkdirAndChown creates a directory tree and chowns it to the sandbox user.
func mkdirAndChown(dir string, chown ChownFunc) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return chown(dir, sandboxUID, sandboxGID)
}
