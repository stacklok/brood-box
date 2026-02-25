// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package progress

import (
	"log/slog"

	"github.com/stacklok/apiary/pkg/domain/progress"
)

// Ensure LogObserver implements progress.Observer.
var _ progress.Observer = (*LogObserver)(nil)

// LogObserver bridges progress.Observer to slog, emitting each event as a
// structured log line. Used in --debug mode.
type LogObserver struct {
	logger *slog.Logger
}

// NewLogObserver creates a LogObserver that writes to the given logger.
func NewLogObserver(logger *slog.Logger) *LogObserver {
	return &LogObserver{logger: logger}
}

// Start logs the beginning of a phase.
func (l *LogObserver) Start(phase progress.Phase, msg string) {
	l.logger.Info(msg, "phase", int(phase))
}

// Complete logs phase completion.
func (l *LogObserver) Complete(msg string) {
	l.logger.Info(msg)
}

// Info logs an informational message.
func (l *LogObserver) Info(msg string) {
	l.logger.Info(msg)
}

// Warn logs a non-fatal warning.
func (l *LogObserver) Warn(msg string) {
	l.logger.Warn(msg)
}

// Fail logs a phase failure.
func (l *LogObserver) Fail(msg string) {
	l.logger.Error(msg)
}
