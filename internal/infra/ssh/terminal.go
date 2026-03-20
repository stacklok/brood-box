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
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	domainagent "github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/session"
)

const (
	// agentDialRetries is the number of retry attempts for connecting to
	// the host SSH agent. Covers transient unavailability when the agent
	// restarts (e.g. gcr-ssh-agent with Restart=on-failure).
	agentDialRetries = 3
	// agentDialBackoff is the base delay between retry attempts.
	agentDialBackoff = 200 * time.Millisecond
	// sshKeepaliveInterval is the interval between SSH keepalive requests.
	sshKeepaliveInterval = 30 * time.Second
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
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	keyData, err := os.ReadFile(opts.KeyPath)
	if err != nil {
		return fmt.Errorf("reading ssh key: %w", err)
	}

	signer, err := ssh.ParsePrivateKey(keyData)
	if err != nil {
		return fmt.Errorf("parsing ssh key: %w", err)
	}

	var hostKeyCallback ssh.HostKeyCallback
	if opts.HostPublicKey != nil {
		hostKeyCallback = ssh.FixedHostKey(opts.HostPublicKey)
	} else {
		//nolint:gosec // Backward compat when host key not available.
		hostKeyCallback = ssh.InsecureIgnoreHostKey()
	}

	config := &ssh.ClientConfig{
		User: opts.User,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(signer),
		},
		HostKeyCallback: hostKeyCallback,
	}

	addr := net.JoinHostPort(opts.Host, fmt.Sprintf("%d", opts.Port))
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return fmt.Errorf("connecting to SSH: %w", err)
	}
	defer func() { _ = client.Close() }()

	// Start SSH keepalive to detect dead connections and keep the mux alive.
	go s.runKeepalive(sessionCtx, client)

	// Set up SSH agent forwarding if requested and an agent is available.
	if opts.SSHAgentForward {
		if err := s.setupAgentForwarding(sessionCtx, client, opts.SSHAuthSock); err != nil {
			s.logger.Debug("SSH agent forwarding not available", "error", err)
		}
	}

	sshSession, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("creating SSH session: %w", err)
	}
	defer func() { _ = sshSession.Close() }()

	// Request agent forwarding on this session if we set it up.
	if opts.SSHAgentForward {
		if fwdErr := agent.RequestAgentForwarding(sshSession); fwdErr != nil {
			s.logger.Debug("failed to request agent forwarding", "error", fwdErr)
		}
	}

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
		resizeCh := opts.Terminal.NotifyResize(sessionCtx)
		go forwardResize(resizeCh, sshSession)
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
	case <-sessionCtx.Done():
		_ = sshSession.Signal(ssh.SIGTERM)
		_ = sshSession.Close()
		_ = client.Close()
		return sessionCtx.Err()
	}
}

func forwardResize(resizeCh <-chan session.TermSize, sshSession *ssh.Session) {
	for newSize := range resizeCh {
		_ = sshSession.WindowChange(newSize.Height, newSize.Width)
	}
}

// setupAgentForwarding registers a handler for incoming auth-agent@openssh.com
// channel opens from the server. Each channel gets its own connection to the
// local SSH agent to avoid concurrency issues on the agent protocol stream.
// If SSH_AUTH_SOCK is not set or unreachable, returns an error (non-fatal).
func (s *InteractiveSession) setupAgentForwarding(ctx context.Context, client *ssh.Client, authSock string) error {
	if authSock == "" {
		return fmt.Errorf("SSH_AUTH_SOCK not set")
	}

	// Verify the agent is reachable before committing to forwarding.
	testConn, err := net.Dial("unix", authSock)
	if err != nil {
		return fmt.Errorf("connecting to SSH agent: %w", err)
	}
	_ = testConn.Close()

	// Handle incoming auth-agent@openssh.com channel opens from the server.
	// When a process inside the VM connects to the agent socket, the server
	// opens this channel type back to us. We serve the agent protocol on it.
	// Each channel gets a dedicated connection to the local agent because the
	// agent protocol stream is not safe for concurrent multiplexed use.
	go func() {
		chans := client.HandleChannelOpen("auth-agent@openssh.com")
		for {
			select {
			case <-ctx.Done():
				return
			case ch, ok := <-chans:
				if !ok {
					s.logger.Warn("SSH agent forwarding channel closed unexpectedly")
					return
				}
				channel, reqs, err := ch.Accept()
				if err != nil {
					s.logger.Warn("failed to accept agent channel", "error", err)
					continue
				}
				go ssh.DiscardRequests(reqs)
				go s.serveAgentChannel(ctx, authSock, channel)
			}
		}
	}()

	s.logger.Debug("SSH agent forwarding configured")
	return nil
}

func (s *InteractiveSession) serveAgentChannel(ctx context.Context, authSock string, channel ssh.Channel) {
	defer func() { _ = channel.Close() }()
	stopCh := make(chan struct{})
	defer close(stopCh)
	go func() {
		select {
		case <-ctx.Done():
			_ = channel.Close()
		case <-stopCh:
		}
	}()

	agentConn, err := s.dialAgentWithRetry(ctx, authSock)
	if err != nil {
		s.logger.Warn("SSH agent unreachable, agent forwarding will fail for this request",
			"error", err, "socket", authSock)
		return
	}
	defer func() { _ = agentConn.Close() }()

	agentClient := agent.NewClient(agentConn)
	if err := agent.ServeAgent(agentClient, channel); err != nil {
		s.logger.Debug("agent forwarding session ended", "error", err)
	}
}

// dialAgentWithRetry attempts to connect to the host SSH agent socket with
// retries and exponential backoff. This handles transient unavailability when
// the agent restarts (e.g. gcr-ssh-agent socket-activated restart).
func (s *InteractiveSession) dialAgentWithRetry(ctx context.Context, authSock string) (net.Conn, error) {
	var lastErr error
	for attempt := range agentDialRetries {
		if attempt > 0 {
			delay := agentDialBackoff * time.Duration(1<<(attempt-1))
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
			s.logger.Debug("retrying SSH agent connection", "attempt", attempt+1, "socket", authSock)
		}
		conn, err := net.Dial("unix", authSock)
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("after %d attempts: %w", agentDialRetries, lastErr)
}

// runKeepalive sends periodic keepalive requests over the SSH connection to
// detect dead connections and prevent idle timeouts from dropping the mux.
func (s *InteractiveSession) runKeepalive(ctx context.Context, client *ssh.Client) {
	ticker := time.NewTicker(sshKeepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// SendRequest with wantReply=true acts as a keepalive ping.
			_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				s.logger.Warn("SSH keepalive failed, connection may be degraded", "error", err)
				return
			}
		}
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
	escaped := make([]string, len(command))
	for i, arg := range command {
		escaped[i] = domainagent.ShellEscape(arg)
	}
	parts = append(parts, "exec "+strings.Join(escaped, " "))
	return strings.Join(parts, " && ")
}
