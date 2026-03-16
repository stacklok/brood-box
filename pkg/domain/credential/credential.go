// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package credential defines interfaces for persisting and seeding agent
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

// FileStore provides CRUD access to individual credential files in the store.
// Used by Seeder implementations to read and write credential files
// independently of the VM lifecycle.
type FileStore interface {
	// SeedFile writes a file into the credential store for an agent.
	// relPath is relative to the agent's home (e.g. ".claude/.credentials.json").
	// No-op if the file already exists in the store.
	SeedFile(agentName, relPath string, content []byte) error

	// ReadFile reads a file from the credential store for an agent.
	// relPath is relative to the agent's home (e.g. ".claude/.credentials.json").
	// Returns os.ErrNotExist if the file does not exist.
	ReadFile(agentName, relPath string) ([]byte, error)

	// OverwriteFile writes a file into the credential store for an agent,
	// replacing any existing content. relPath is relative to the agent's
	// home (e.g. ".claude/.credentials.json"). Unlike SeedFile, this
	// always writes regardless of whether the file already exists.
	OverwriteFile(agentName, relPath string, content []byte) error
}

// Seeder seeds fresh credentials from the host into the file store
// before VM boot. Each agent has a different implementation.
type Seeder interface {
	Seed(store FileStore) error
}
