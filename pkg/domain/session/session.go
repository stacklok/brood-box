// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package session defines domain interfaces for interactive terminal sessions.
package session

import (
	"context"
)

// SessionOpts configures an interactive terminal session.
type SessionOpts struct {
	// Host is the SSH server host (e.g., "127.0.0.1").
	Host string

	// Port is the SSH server port.
	Port uint16

	// User is the SSH username.
	User string

	// KeyPath is the path to the SSH private key.
	KeyPath string

	// Command is the command to execute in the VM.
	// It will be wrapped: . /etc/sandbox-env && cd /workspace && exec <cmd>
	Command []string

	// Terminal provides I/O streams and PTY control for the session.
	Terminal Terminal

	// SSHAgentForward enables SSH agent forwarding for this session.
	SSHAgentForward bool
}

// TerminalSession manages interactive PTY sessions over SSH.
type TerminalSession interface {
	// Run starts an interactive terminal session with the given options.
	// It blocks until the remote command exits and returns its exit code as an error.
	Run(ctx context.Context, opts SessionOpts) error
}
