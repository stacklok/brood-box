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
	"os"

	"golang.org/x/crypto/ssh"

	"github.com/stacklok/sandbox-agent/internal/guest/env"
	"github.com/stacklok/sandbox-agent/internal/guest/mount"
	"github.com/stacklok/sandbox-agent/internal/guest/network"
	"github.com/stacklok/sandbox-agent/internal/guest/sshd"
)

// Run executes the full guest boot sequence and returns a shutdown function
// that stops the SSH server. The caller should block on signals and then
// invoke shutdown before halting.
func Run(logger *slog.Logger) (shutdown func(), err error) {
	// 1. Essential mounts — /proc is needed before netlink can work.
	logger.Info("mounting essential filesystems")
	if err := mount.Essential(); err != nil {
		return nil, fmt.Errorf("essential mounts: %w", err)
	}

	// 2. Network configuration.
	logger.Info("configuring network")
	if err := network.Configure(); err != nil {
		return nil, fmt.Errorf("network setup: %w", err)
	}

	// 3. Workspace mount (non-fatal — VM is still useful without it).
	logger.Info("mounting workspace")
	if err := mount.Workspace("/workspace", "workspace", 5); err != nil {
		logger.Warn("workspace mount failed, continuing without workspace", "error", err)
	}

	// 4. Load environment file.
	envVars, err := env.Load("/etc/sandbox-env")
	if err != nil {
		return nil, fmt.Errorf("loading environment: %w", err)
	}

	// 5. Parse authorized keys.
	authorizedKeys, err := parseAuthorizedKeys("/home/sandbox/.ssh/authorized_keys")
	if err != nil {
		return nil, fmt.Errorf("parsing authorized keys: %w", err)
	}

	// 6. Start SSH server.
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

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			logger.Error("SSH server error", "error", err)
		}
	}()

	logger.Info("sandbox init ready", "ssh_port", 22)

	return func() { srv.Close() }, nil
}

// parseAuthorizedKeys reads an authorized_keys file and returns the parsed
// public keys.
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
		if len(line) == 0 {
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
	return keys, nil
}
