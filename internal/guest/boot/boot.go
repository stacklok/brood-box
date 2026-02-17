// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

// Package boot orchestrates the guest VM boot sequence: essential mounts,
// networking, workspace mount, environment loading, and SSH server start.
package boot

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"os"

	"golang.org/x/crypto/ssh"

	"github.com/stacklok/apiary/internal/guest/env"
	"github.com/stacklok/apiary/internal/guest/mount"
	"github.com/stacklok/apiary/internal/guest/network"
	"github.com/stacklok/apiary/internal/guest/sshd"
	"github.com/stacklok/propolis/guest/harden"
)

// Run executes the full guest boot sequence and returns a shutdown function
// that stops the SSH server. The caller should block on signals and then
// invoke shutdown before halting.
func Run(logger *slog.Logger) (shutdown func(), err error) {
	// 1. Essential mounts — /proc is needed before netlink can work.
	logger.Info("mounting essential filesystems")
	if err := mount.Essential(logger); err != nil {
		return nil, fmt.Errorf("essential mounts: %w", err)
	}

	// 2. Network configuration.
	logger.Info("configuring network")
	if err := network.Configure(); err != nil {
		return nil, fmt.Errorf("network setup: %w", err)
	}

	// 3. Workspace mount (non-fatal — VM is still useful without it).
	logger.Info("mounting workspace")
	if err := mount.Workspace(logger, "/workspace", "workspace", 1000, 1000, 5); err != nil {
		logger.Warn("workspace mount failed, continuing without workspace", "error", err)
	}

	// 4. Apply kernel sysctl hardening (needs /proc mounted).
	harden.KernelDefaults(logger)

	// 5. Lock down /root/ so the sandbox user cannot read it.
	lockdownRoot(logger)

	// 6. Load environment file.
	envVars, err := env.Load("/etc/sandbox-env")
	if err != nil {
		return nil, fmt.Errorf("loading environment: %w", err)
	}

	// 7. Parse authorized keys.
	authorizedKeys, err := parseAuthorizedKeys("/home/sandbox/.ssh/authorized_keys")
	if err != nil {
		return nil, fmt.Errorf("parsing authorized keys: %w", err)
	}

	// 8. Drop unneeded capabilities from the bounding set. Keep only
	// what sshd needs: SETUID/SETGID for credential switching,
	// NET_BIND_SERVICE for port 22. This must be the last privileged
	// operation before starting the SSH server.
	//
	// This is fatal because PR_CAPBSET_DROP has been available since
	// Linux 2.6.25 — failure indicates a serious problem (not root,
	// kernel bug) and continuing with a full bounding set defeats the
	// hardening entirely.
	logger.Info("dropping unnecessary capabilities")
	if err := harden.DropBoundingCaps(
		harden.CapSetUID,
		harden.CapSetGID,
		harden.CapNetBindService,
	); err != nil {
		return nil, fmt.Errorf("dropping capabilities: %w", err)
	}

	// 9. Start SSH server — bind synchronously so listen errors surface
	// immediately rather than being swallowed in a goroutine.
	cfg := sshd.Config{
		Port:           22,
		AuthorizedKeys: authorizedKeys,
		Env:            envVars,
		DefaultUID:     1000,
		DefaultGID:     1000,
		DefaultUser:    "sandbox",
		DefaultHome:    "/home/sandbox",
		DefaultShell:   "/bin/bash",
		Logger:         logger,
	}
	srv, err := sshd.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("creating SSH server: %w", err)
	}

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", cfg.Port))
	if err != nil {
		return nil, fmt.Errorf("listening on port %d: %w", cfg.Port, err)
	}

	go func() {
		if err := srv.Serve(ln); err != nil {
			logger.Error("SSH server error", "error", err)
		}
	}()

	logger.Info("sandbox init ready", "ssh_port", cfg.Port)

	return func() { srv.Close() }, nil
}

// lockdownRoot sets /root/ to mode 0700 so the sandbox user cannot read
// its contents (MCP bootstrap config, debug logs, etc.).
func lockdownRoot(logger *slog.Logger) {
	logger.Info("locking down /root permissions")
	if err := os.Chmod("/root", 0o700); err != nil {
		logger.Warn("failed to chmod /root", "error", err)
	}
}

// parseAuthorizedKeys reads an authorized_keys file and returns the parsed
// public keys. Returns an error if no valid keys are found.
func parseAuthorizedKeys(path string) ([]ssh.PublicKey, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var keys []ssh.PublicKey
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		key, _, _, _, err := ssh.ParseAuthorizedKey(line)
		if err != nil {
			continue // skip unparseable lines
		}
		keys = append(keys, key)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no valid keys found in %s", path)
	}
	return keys, nil
}
