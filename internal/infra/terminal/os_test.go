// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package terminal

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/sandbox-agent/internal/domain/session"
)

func TestOSTerminal_Streams(t *testing.T) {
	t.Parallel()

	term := NewOSTerminal(os.Stdin, os.Stdout, os.Stderr)
	assert.NotNil(t, term.Stdin())
	assert.NotNil(t, term.Stdout())
	assert.NotNil(t, term.Stderr())
}

func TestOSTerminal_MakeRaw_NonInteractive(t *testing.T) {
	t.Parallel()

	// Use a pipe (not a terminal) to get non-interactive behavior.
	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()

	term := NewOSTerminal(r, w, w)
	assert.False(t, term.IsInteractive())

	restore, err := term.MakeRaw()
	require.NoError(t, err)
	require.NotNil(t, restore)

	// Should be safe to call multiple times.
	restore()
	restore()
}

func TestOSTerminal_NotifyResize_NonInteractive(t *testing.T) {
	t.Parallel()

	r, w, err := os.Pipe()
	require.NoError(t, err)
	defer func() { _ = r.Close() }()
	defer func() { _ = w.Close() }()

	term := NewOSTerminal(r, w, w)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := term.NotifyResize(ctx)
	require.NotNil(t, ch)

	// Channel should be closed immediately for non-interactive terminals.
	select {
	case _, ok := <-ch:
		assert.False(t, ok, "channel should be closed for non-interactive terminal")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout waiting for closed channel")
	}
}

func TestOSTerminal_ImplementsTerminal(t *testing.T) {
	t.Parallel()

	var _ session.Terminal = (*OSTerminal)(nil)
}
