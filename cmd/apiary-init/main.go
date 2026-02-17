// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

// apiary-init is the PID 1 init process for apiary guest VMs.
// It mounts filesystems, configures networking, and starts an embedded
// SSH server — replacing the shell init script and external dependencies
// (dropbear, iproute2, mount).
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/stacklok/propolis/guest/boot"
	"github.com/stacklok/propolis/guest/reaper"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	// PID 1 must reap orphaned children.
	stopReaper := reaper.Start(logger)
	defer stopReaper()

	shutdown, err := boot.Run(logger)
	if err != nil {
		logger.Error("boot failed", "error", err)
		halt()
		return
	}

	// Block until SIGTERM or SIGINT.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	received := <-sig
	logger.Info("received signal, shutting down", "signal", received)

	// Give the SSH server time to close gracefully.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		shutdown()
		close(done)
	}()

	select {
	case <-done:
		logger.Info("shutdown complete")
	case <-ctx.Done():
		logger.Warn("shutdown timed out")
	}

	halt()
}

func halt() {
	// As PID 1 inside a VM, Reboot with POWER_OFF is the clean way to stop.
	_ = syscall.Reboot(syscall.LINUX_REBOOT_CMD_POWER_OFF)
}
