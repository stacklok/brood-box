// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package terminal provides OS-level terminal implementations backed by real
// file descriptors and PTY control via golang.org/x/term.
package terminal

import (
	"context"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/term"

	"github.com/stacklok/sandbox-agent/internal/domain/session"
)

// Ensure OSTerminal implements session.Terminal at compile time.
var _ session.Terminal = (*OSTerminal)(nil)

// OSTerminal implements session.Terminal backed by real *os.File descriptors
// and golang.org/x/term for PTY control.
type OSTerminal struct {
	stdin  *os.File
	stdout *os.File
	stderr *os.File
}

// NewOSTerminal creates a Terminal backed by the given OS file descriptors.
func NewOSTerminal(stdin, stdout, stderr *os.File) *OSTerminal {
	return &OSTerminal{
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
	}
}

// Stdin returns the input stream.
func (t *OSTerminal) Stdin() io.Reader { return t.stdin }

// Stdout returns the output stream.
func (t *OSTerminal) Stdout() io.Writer { return t.stdout }

// Stderr returns the error stream.
func (t *OSTerminal) Stderr() io.Writer { return t.stderr }

// IsInteractive reports whether stdin is a real terminal.
func (t *OSTerminal) IsInteractive() bool {
	return term.IsTerminal(int(t.stdin.Fd()))
}

// Size returns the current terminal dimensions.
func (t *OSTerminal) Size() (session.TermSize, error) {
	width, height, err := term.GetSize(int(t.stdin.Fd()))
	if err != nil {
		return session.TermSize{}, err
	}
	return session.TermSize{Width: width, Height: height}, nil
}

// MakeRaw enters raw mode and returns an idempotent restore function.
// Always returns a non-nil restore func, even on error or for non-interactive
// terminals.
func (t *OSTerminal) MakeRaw() (func(), error) {
	noop := func() {}

	if !t.IsInteractive() {
		return noop, nil
	}

	fd := int(t.stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return noop, err
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			_ = term.Restore(fd, oldState)
		})
	}, nil
}

// NotifyResize delivers terminal size-change events (SIGWINCH) until ctx is
// cancelled. Returns a closed channel for non-interactive terminals.
// Never returns nil.
func (t *OSTerminal) NotifyResize(ctx context.Context) <-chan session.TermSize {
	ch := make(chan session.TermSize, 1)

	if !t.IsInteractive() {
		close(ch)
		return ch
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGWINCH)

	go func() {
		defer signal.Stop(sigCh)
		defer close(ch)
		for {
			select {
			case <-sigCh:
				size, err := t.Size()
				if err != nil {
					continue
				}
				select {
				case ch <- size:
				default:
					// Drop if consumer is slow.
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	return ch
}
