// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package git defines domain types and interfaces for git identity
// and environment variable forwarding.
package git

// Identity holds the git user identity (name and email).
type Identity struct {
	Name  string
	Email string
}

// IsComplete returns true if both Name and Email are set.
func (id Identity) IsComplete() bool {
	return id.Name != "" && id.Email != ""
}

// IdentityProvider resolves the host git user identity.
type IdentityProvider interface {
	// GetIdentity returns the git user identity from the host.
	// Returns a zero Identity (not an error) if no identity is configured.
	GetIdentity() (Identity, error)
}

// CommonEnvPatterns returns environment variable patterns that should
// always be forwarded for git operations, regardless of agent config.
func CommonEnvPatterns() []string {
	return []string{
		"GIT_AUTHOR_NAME",
		"GIT_AUTHOR_EMAIL",
		"GIT_COMMITTER_NAME",
		"GIT_COMMITTER_EMAIL",
		"GITHUB_TOKEN",
		"GH_TOKEN",
	}
}
