// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"syscall"
	"time"
)

// guestMountEntry matches the JSON written by InjectMountConfig on the host.
type guestMountEntry struct {
	Tag       string `json:"tag"`
	GuestPath string `json:"guest_path"`
	ReadOnly  bool   `json:"read_only"`
}

// mountConfigPath is the guest path where the host writes extra mount config.
const mountConfigPath = "/etc/broodbox-mounts.json"

// mountExtras reads /etc/broodbox-mounts.json and mounts each virtiofs share.
// If the config file does not exist, it returns nil (no extra mounts needed).
func mountExtras(logger *slog.Logger) error {
	data, err := os.ReadFile(mountConfigPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reading mount config: %w", err)
	}

	var entries []guestMountEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("parsing mount config: %w", err)
	}

	for _, entry := range entries {
		if err := mountOne(logger, entry); err != nil {
			return err
		}
	}

	return nil
}

// mountRetries is the number of attempts for each virtiofs mount.
const mountRetries = 5

// mountRetrySleep is the delay between mount retries.
const mountRetrySleep = 500 * time.Millisecond

// sandboxUID and sandboxGID are the UID/GID of the sandbox user in the guest.
const (
	sandboxUID = 1000
	sandboxGID = 1000
)

func mountOne(logger *slog.Logger, entry guestMountEntry) error {
	if err := os.MkdirAll(entry.GuestPath, 0o755); err != nil {
		return fmt.Errorf("creating mount point %s: %w", entry.GuestPath, err)
	}

	flags := uintptr(syscall.MS_NOSUID | syscall.MS_NODEV)
	if entry.ReadOnly {
		flags |= syscall.MS_RDONLY
	}

	var mountErr error
	for attempt := 1; attempt <= mountRetries; attempt++ {
		mountErr = syscall.Mount(entry.Tag, entry.GuestPath, "virtiofs", flags, "")
		if mountErr == nil {
			break
		}
		logger.Debug("virtiofs mount attempt failed, retrying",
			"tag", entry.Tag,
			"guest_path", entry.GuestPath,
			"attempt", attempt,
			"error", mountErr,
		)
		time.Sleep(mountRetrySleep)
	}
	if mountErr != nil {
		return fmt.Errorf("mounting virtiofs tag %q at %s after %d attempts: %w",
			entry.Tag, entry.GuestPath, mountRetries, mountErr)
	}

	if err := os.Chown(entry.GuestPath, sandboxUID, sandboxGID); err != nil {
		logger.Warn("failed to chown mount point", "path", entry.GuestPath, "error", err)
	}

	logger.Info("mounted extra virtiofs share", "tag", entry.Tag, "guest_path", entry.GuestPath, "read_only", entry.ReadOnly)
	return nil
}
