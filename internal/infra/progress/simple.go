// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package progress

import (
	"fmt"
	"io"

	"github.com/stacklok/apiary/pkg/domain/progress"
)

// Ensure SimpleObserver implements progress.Observer.
var _ progress.Observer = (*SimpleObserver)(nil)

// SimpleObserver renders progress as plain text without ANSI codes.
// Suitable for non-TTY environments (pipes, CI).
type SimpleObserver struct {
	out io.Writer
}

// NewSimpleObserver creates a SimpleObserver that writes to w.
func NewSimpleObserver(w io.Writer) *SimpleObserver {
	return &SimpleObserver{out: w}
}

// Start prints a new phase message.
func (s *SimpleObserver) Start(_ progress.Phase, msg string) {
	_, _ = fmt.Fprintf(s.out, ":: %s\n", msg)
}

// Complete prints a completion message.
func (s *SimpleObserver) Complete(msg string) {
	_, _ = fmt.Fprintf(s.out, "   done: %s\n", msg)
}

// Warn prints a warning message.
func (s *SimpleObserver) Warn(msg string) {
	_, _ = fmt.Fprintf(s.out, "   warn: %s\n", msg)
}

// Fail prints a failure message.
func (s *SimpleObserver) Fail(msg string) {
	_, _ = fmt.Fprintf(s.out, "   FAILED: %s\n", msg)
}
