// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package agent defines the Agent value object and Registry interface.
package agent

import (
	"fmt"
	"regexp"

	"github.com/stacklok/brood-box/pkg/domain/egress"
)

// MaxNameLength is the maximum allowed length for an agent name.
const MaxNameLength = 64

// agentNameRe matches valid agent names: starts with alphanumeric,
// followed by alphanumerics, hyphens, or underscores.
var agentNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// ValidateName checks that an agent name is safe for use in filesystem
// paths and VM identifiers. It rejects empty strings, names starting
// with non-alphanumeric characters, path traversal sequences, and
// names exceeding MaxNameLength.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("agent name must not be empty")
	}
	if len(name) > MaxNameLength {
		return fmt.Errorf("agent name too long (%d chars, max %d)", len(name), MaxNameLength)
	}
	if !agentNameRe.MatchString(name) {
		return fmt.Errorf("invalid agent name %q: must start with a letter or digit and contain only letters, digits, hyphens, or underscores", name)
	}
	return nil
}

// MCPConfigFormat identifies how an agent consumes MCP server configuration.
type MCPConfigFormat string

const (
	// MCPConfigFormatClaudeCode injects a Claude Code MCP config file.
	MCPConfigFormatClaudeCode MCPConfigFormat = "claude-code"

	// MCPConfigFormatCodex injects a Codex MCP config file.
	MCPConfigFormatCodex MCPConfigFormat = "codex"

	// MCPConfigFormatOpenCode injects an OpenCode MCP config file.
	MCPConfigFormatOpenCode MCPConfigFormat = "opencode"

	// MCPConfigFormatNone means no MCP config injection.
	MCPConfigFormatNone MCPConfigFormat = "none"
)

// Agent describes a coding agent that can run inside a sandbox VM.
type Agent struct {
	// Name is the unique identifier for this agent (e.g., "claude-code").
	Name string

	// Image is the OCI image reference to pull and boot.
	Image string

	// Command is the entrypoint command to run inside the VM.
	Command []string

	// EnvForward lists environment variable patterns to forward into the VM.
	// Supports exact match ("ANTHROPIC_API_KEY") and glob suffix ("CLAUDE_*").
	EnvForward []string

	// DefaultCPUs is the default number of vCPUs for this agent.
	DefaultCPUs uint32

	// DefaultMemory is the default RAM in MiB for this agent.
	DefaultMemory uint32

	// DefaultEgressProfile is the default egress restriction level.
	DefaultEgressProfile egress.ProfileName

	// EgressHosts maps profile names to allowed host lists for this agent.
	EgressHosts map[egress.ProfileName][]egress.Host

	// MCPConfigFormat identifies how MCP server configuration is injected.
	MCPConfigFormat MCPConfigFormat

	// CredentialPaths lists relative paths (from the sandbox user's home)
	// whose contents are persisted between sessions for authentication.
	// Only built-in agents should set this field.
	CredentialPaths []string

	// DefaultTmpSize is the default /tmp tmpfs size in MiB for this agent.
	// Zero means use the go-microvm default (256 MiB). Built-in agents default to 512 MiB.
	DefaultTmpSize uint32
}

// Registry provides access to known agents by name.
type Registry interface {
	// Get returns the agent with the given name, or an error if not found.
	Get(name string) (Agent, error)

	// List returns all registered agents.
	List() []Agent
}

// ErrNotFound is returned when an agent is not found in the registry.
type ErrNotFound struct {
	Name string
}

func (e *ErrNotFound) Error() string {
	return fmt.Sprintf("agent not found: %q", e.Name)
}
