// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package reaper

import (
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

// Start launches a background goroutine that reaps zombie child processes
// by handling SIGCHLD signals. This is intended to run as PID 1 in a guest VM.
func Start() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGCHLD)

	go func() {
		for range ch {
			for {
				pid, err := syscall.Wait4(-1, nil, syscall.WNOHANG, nil)
				if err == syscall.ECHILD {
					break
				}
				if pid <= 0 {
					break
				}
				slog.Debug("reaped child process", "pid", pid)
			}
		}
	}()
}
