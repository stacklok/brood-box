// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package ssh provides interactive PTY terminal sessions over SSH.
package ssh

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/stacklok/sandbox-agent/internal/domain/session"
)

// Ensure InteractiveSession implements session.TerminalSession at compile time.
var _ session.TerminalSession = (*InteractiveSession)(nil)

// InteractiveSession implements TerminalSession with PTY forwarding.
type InteractiveSession struct {
	logger *slog.Logger
}

// NewInteractiveSession creates a new terminal session handler.
func NewInteractiveSession(logger *slog.Logger) *InteractiveSession {
	return &InteractiveSession{logger: logger}
}

// ExitError represents a non-zero exit code from the remote command.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("remote command exited with code %d", e.Code)
}

// Run establishes an SSH connection, requests a PTY, and runs the command
// interactively with full terminal forwarding.
func (s *InteractiveSession) Run(ctx context.Context, opts session.SessionOpts) error {
	keyData, err := os.ReadFile(opts.KeyPath)
	if err != nil {
		return fmt.Errorf("reading ssh key: %w", err)
	}

	signer, err := ssh.ParsePrivateKey(keyData)
	if err != nil {
		return fmt.Errorf("parsing ssh key: %w", err)
	}

	config := &ssh.ClientConfig{
		User: opts.User,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		//nolint:gosec // We trust VMs we just created.
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	addr := net.JoinHostPort(opts.Host, fmt.Sprintf("%d", opts.Port))
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return fmt.Errorf("connecting to SSH: %w", err)
	}
	defer func() { _ = client.Close() }()

	sshSession, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("creating SSH session: %w", err)
	}
	defer func() { _ = sshSession.Close() }()

	// Set terminal to raw mode if it's a real terminal.
	if opts.Terminal.IsInteractive() {
		restore, err := opts.Terminal.MakeRaw()
		if err != nil {
			return fmt.Errorf("setting raw terminal: %w", err)
		}
		defer restore() // safety net for panics

		// Get terminal size and request PTY.
		size, err := opts.Terminal.Size()
		if err != nil {
			size = session.TermSize{Width: 80, Height: 24}
		}

		modes := ssh.TerminalModes{
			ssh.ECHO:          1,
			ssh.TTY_OP_ISPEED: 14400,
			ssh.TTY_OP_OSPEED: 14400,
		}

		if err := sshSession.RequestPty("xterm-256color", size.Height, size.Width, modes); err != nil {
			return fmt.Errorf("requesting PTY: %w", err)
		}

		// Handle terminal resize signals.
		resizeCh := opts.Terminal.NotifyResize(ctx)
		go func() {
			for newSize := range resizeCh {
				_ = sshSession.WindowChange(newSize.Height, newSize.Width)
			}
		}()
	}

	// Wire up I/O.
	sshSession.Stdin = opts.Terminal.Stdin()
	sshSession.Stdout = opts.Terminal.Stdout()
	sshSession.Stderr = opts.Terminal.Stderr()

	// Build the command string.
	cmd := buildCommand(opts.Command)
	s.logger.Info("running command in VM", "command", cmd)

	if err := sshSession.Start(cmd); err != nil {
		return fmt.Errorf("starting remote command: %w", err)
	}

	// Wait for the command to finish, respecting context cancellation.
	done := make(chan error, 1)
	go func() {
		done <- sshSession.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			if exitErr, ok := err.(*ssh.ExitError); ok {
				return &ExitError{Code: exitErr.ExitStatus()}
			}
			return fmt.Errorf("remote command failed: %w", err)
		}
		return nil
	case <-ctx.Done():
		_ = sshSession.Signal(ssh.SIGTERM)
		return ctx.Err()
	}
}

// buildCommand constructs the shell command to run in the VM.
// Sources the system profile (for PATH), the env file, changes to the
// workspace, and executes the command.
func buildCommand(command []string) string {
	var parts []string
	parts = append(parts, ". /etc/profile 2>/dev/null || true")
	parts = append(parts, ". /etc/sandbox-env 2>/dev/null || true")
	parts = append(parts, "cd /workspace")
	parts = append(parts, "exec "+strings.Join(command, " "))
	return strings.Join(parts, " && ")
}
