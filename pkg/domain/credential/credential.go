// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package credential defines the Store interface for persisting agent
// authentication credentials across sandbox VM sessions.
package credential

// MaxFileSize is the maximum allowed size per credential file (64 KiB).
const MaxFileSize int64 = 64 * 1024

// MaxFileCount is the maximum number of files persisted per agent.
const MaxFileCount = 100

// MaxDepth is the maximum directory nesting depth within a credential path.
const MaxDepth = 3

// Store persists and restores agent credential files between VM sessions.
type Store interface {
	// Inject copies previously saved credentials into the guest rootfs
	// before VM boot. No-op when no saved state exists for the agent.
	Inject(rootfsPath, agentName string, credentialPaths []string) error

	// Extract copies credential files from the guest rootfs after the
	// session ends, saving them for the next boot.
	Extract(rootfsPath, agentName string, credentialPaths []string) error
}
