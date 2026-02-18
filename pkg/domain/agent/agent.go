// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package agent defines the Agent value object and Registry interface.
package agent

import (
	"fmt"

	"github.com/stacklok/apiary/pkg/domain/egress"
)

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
	return fmt.Sprintf("agent not found: %s", e.Name)
}
