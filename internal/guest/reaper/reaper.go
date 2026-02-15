// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package reaper

import (
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// Start launches a background goroutine that reaps zombie child processes
// by handling SIGCHLD signals. This is intended to run as PID 1 in a guest VM.
// The returned stop function unregisters the signal handler and stops the
// goroutine.
func Start(logger *slog.Logger) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGCHLD)

	go func() {
		for range ch {
			for {
				pid, err := syscall.Wait4(-1, nil, syscall.WNOHANG, nil)
				if errors.Is(err, syscall.ECHILD) {
					break
				}
				if pid <= 0 {
					break
				}
				logger.Debug("reaped child process", "pid", pid)
			}
		}
	}()

	return func() {
		signal.Stop(ch)
		close(ch)
	}
}
