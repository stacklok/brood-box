// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package ssh provides interactive PTY terminal sessions over SSH.
package ssh

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
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

	// Stdin is the input stream (typically os.Stdin).
	Stdin *os.File

	// Stdout is the output stream (typically os.Stdout).
	Stdout *os.File

	// Stderr is the error stream (typically os.Stderr).
	Stderr *os.File
}

// TerminalSession manages interactive PTY sessions over SSH.
type TerminalSession interface {
	// Run starts an interactive terminal session with the given options.
	// It blocks until the remote command exits and returns its exit code as an error.
	Run(ctx context.Context, opts SessionOpts) error
}

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
func (s *InteractiveSession) Run(ctx context.Context, opts SessionOpts) error {
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

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("creating SSH session: %w", err)
	}
	defer func() { _ = session.Close() }()

	// Set terminal to raw mode if it's a real terminal.
	if term.IsTerminal(int(opts.Stdin.Fd())) {
		oldState, err := term.MakeRaw(int(opts.Stdin.Fd()))
		if err != nil {
			return fmt.Errorf("setting raw terminal: %w", err)
		}
		defer func() {
			_ = term.Restore(int(opts.Stdin.Fd()), oldState)
		}()

		// Get terminal size and request PTY.
		width, height, err := term.GetSize(int(opts.Stdin.Fd()))
		if err != nil {
			width, height = 80, 24
		}

		modes := ssh.TerminalModes{
			ssh.ECHO:          1,
			ssh.TTY_OP_ISPEED: 14400,
			ssh.TTY_OP_OSPEED: 14400,
		}

		if err := session.RequestPty("xterm-256color", height, width, modes); err != nil {
			return fmt.Errorf("requesting PTY: %w", err)
		}

		// Handle terminal resize signals.
		s.handleResize(ctx, opts.Stdin, session)
	}

	// Wire up I/O.
	session.Stdin = opts.Stdin
	session.Stdout = opts.Stdout
	session.Stderr = opts.Stderr

	// Build the command string.
	cmd := buildCommand(opts.Command)
	s.logger.Info("running command in VM", "command", cmd)

	if err := session.Start(cmd); err != nil {
		return fmt.Errorf("starting remote command: %w", err)
	}

	// Wait for the command to finish, respecting context cancellation.
	done := make(chan error, 1)
	go func() {
		done <- session.Wait()
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
		_ = session.Signal(ssh.SIGTERM)
		return ctx.Err()
	}
}

// handleResize watches for SIGWINCH and forwards window size changes.
func (s *InteractiveSession) handleResize(ctx context.Context, stdin *os.File, session *ssh.Session) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)

	var once sync.Once
	go func() {
		defer once.Do(func() { signal.Stop(sigCh) })
		for {
			select {
			case <-sigCh:
				width, height, err := term.GetSize(int(stdin.Fd()))
				if err != nil {
					continue
				}
				_ = session.WindowChange(height, width)
			case <-ctx.Done():
				return
			}
		}
	}()
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

// Ensure InteractiveSession implements TerminalSession.
var _ TerminalSession = (*InteractiveSession)(nil)

// Ensure io interfaces are satisfied (compile-time check).
var (
	_ io.Reader = (*os.File)(nil)
	_ io.Writer = (*os.File)(nil)
)
