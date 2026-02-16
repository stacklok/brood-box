// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package session

import (
	"context"
	"io"
)

// TermSize represents terminal dimensions.
type TermSize struct {
	Width  int
	Height int
}

// IOStreams abstracts the three standard I/O streams.
// This is the narrow interface that the application layer depends on.
type IOStreams interface {
	Stdin() io.Reader
	Stdout() io.Writer
	Stderr() io.Writer
}

// Terminal extends IOStreams with PTY control for interactive sessions.
// Only the SSH terminal session infrastructure should depend on this.
type Terminal interface {
	IOStreams

	// IsInteractive reports whether the terminal supports PTY operations.
	IsInteractive() bool

	// Size returns current terminal dimensions.
	// Returns zero values and error if non-interactive.
	Size() (TermSize, error)

	// MakeRaw enters raw mode and returns an idempotent restore function.
	// Always returns a non-nil restore func (no-op for non-interactive
	// terminals or on error). Safe to call restore multiple times.
	MakeRaw() (restore func(), err error)

	// NotifyResize delivers size-change events until ctx is cancelled.
	// Returns a closed (immediately-draining) channel for non-interactive
	// terminals. Never returns nil.
	NotifyResize(ctx context.Context) <-chan TermSize
}
