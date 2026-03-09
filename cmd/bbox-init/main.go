// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

// bbox-init is the PID 1 init process for Brood Box guest VMs.
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

	"github.com/stacklok/brood-box/internal/guest/homefs"
	"github.com/stacklok/brood-box/internal/guest/seccomp"
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

	shutdown, err := boot.Run(logger, boot.WithSSHAgentForwarding(true))
	if err != nil {
		logger.Error("boot failed", "error", err)
		halt()
		return
	}

	// Make /home/sandbox writable. On certain host kernels (e.g. openSUSE
	// MicroOS / Tumbleweed), the root virtiofs rejects writes even though
	// the mount is nominally rw. An overlayfs (or tmpfs fallback) on the
	// home directory works around this so agents can create config files.
	if err := homefs.MakeWritable(logger, homefs.SandboxHome, homefs.SandboxUID, homefs.SandboxGID); err != nil {
		logger.Warn("failed to make home writable, agents may not start",
			"error", err)
	}

	if err := seccomp.Apply(); err != nil {
		logger.Error("seccomp filter failed", "error", err)
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
		halt()
		os.Exit(1)
	}

	halt()
}

func halt() {
	// As PID 1 inside a VM, Reboot with POWER_OFF is the clean way to stop.
	_ = syscall.Reboot(syscall.LINUX_REBOOT_CMD_POWER_OFF)
}
