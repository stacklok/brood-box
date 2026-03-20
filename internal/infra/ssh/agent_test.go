// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package ssh

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestSession(t *testing.T) *InteractiveSession {
	t.Helper()
	return NewInteractiveSession(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
}

// shortTempDir creates a temp directory with a short path suitable for Unix
// sockets. macOS limits Unix socket paths to 104 bytes; t.TempDir() paths
// often exceed that when combined with long test names.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ssh")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// startUnixListener creates a Unix socket listener in a temp directory and
// returns the socket path. The listener accepts and immediately closes
// connections in a goroutine so that dialAgentWithRetry can succeed.
func startUnixListener(t *testing.T) (string, net.Listener) {
	t.Helper()
	sockPath := filepath.Join(shortTempDir(t), "a.sock")
	ln, err := net.Listen("unix", sockPath)
	require.NoError(t, err)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			_ = conn.Close()
		}
	}()

	return sockPath, ln
}

func TestDialAgentWithRetry_SuccessFirstAttempt(t *testing.T) {
	t.Parallel()
	s := newTestSession(t)
	sockPath, ln := startUnixListener(t)
	defer func() { _ = ln.Close() }()

	ctx := context.Background()
	conn, err := s.dialAgentWithRetry(ctx, sockPath)
	require.NoError(t, err)
	require.NotNil(t, conn)
	_ = conn.Close()
}

func TestDialAgentWithRetry_AllRetriesFail(t *testing.T) {
	t.Parallel()
	s := newTestSession(t)
	// Use a path that doesn't exist — all attempts should fail.
	badPath := filepath.Join(shortTempDir(t), "nonexistent.sock")

	ctx := context.Background()
	start := time.Now()
	conn, err := s.dialAgentWithRetry(ctx, badPath)

	require.Error(t, err)
	assert.Nil(t, conn)
	assert.Contains(t, err.Error(), "after 3 attempts")
	// Verify retries actually waited (at least 200ms + 400ms = 600ms of backoff).
	assert.GreaterOrEqual(t, time.Since(start), 500*time.Millisecond)
}

func TestDialAgentWithRetry_SuccessAfterRetry(t *testing.T) {
	t.Parallel()
	s := newTestSession(t)
	sockPath := filepath.Join(shortTempDir(t), "a.sock")

	// Start the listener after a short delay so the first attempt fails
	// but a retry succeeds.
	go func() {
		time.Sleep(250 * time.Millisecond)
		ln, err := net.Listen("unix", sockPath)
		if err != nil {
			return
		}
		t.Cleanup(func() { _ = ln.Close() })
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()

	ctx := context.Background()
	conn, err := s.dialAgentWithRetry(ctx, sockPath)
	require.NoError(t, err)
	require.NotNil(t, conn)
	_ = conn.Close()
}

func TestDialAgentWithRetry_ContextCancelled(t *testing.T) {
	t.Parallel()
	s := newTestSession(t)
	badPath := filepath.Join(shortTempDir(t), "nonexistent.sock")

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately so the first retry's backoff select picks up ctx.Done().
	cancel()

	conn, err := s.dialAgentWithRetry(ctx, badPath)
	require.Error(t, err)
	assert.Nil(t, conn)
	// Either the context error or the dial error is acceptable here,
	// depending on scheduling. The important thing is it doesn't hang.
}

func TestSetupAgentForwarding_EmptyAuthSock(t *testing.T) {
	t.Parallel()
	s := newTestSession(t)

	ctx := context.Background()
	err := s.setupAgentForwarding(ctx, nil, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SSH_AUTH_SOCK not set")
}

func TestSetupAgentForwarding_UnreachableSocket(t *testing.T) {
	t.Parallel()
	s := newTestSession(t)

	badPath := filepath.Join(shortTempDir(t), "nonexistent.sock")

	ctx := context.Background()
	err := s.setupAgentForwarding(ctx, nil, badPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connecting to SSH agent")
}
