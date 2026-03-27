// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/stacklok/go-microvm/image"

	"github.com/stacklok/brood-box/pkg/domain/workspace"
)

// mountConfigPath is the guest path where extra mount configuration is written.
const mountConfigPath = "/etc/broodbox-mounts.json"

// mountEntry is the JSON-serializable form of a mount request for the guest.
type mountEntry struct {
	Tag       string `json:"tag"`
	GuestPath string `json:"guest_path"`
	ReadOnly  bool   `json:"read_only"`
}

// InjectMountConfig returns a rootfs hook that writes extra mount configuration
// to /etc/broodbox-mounts.json. The guest init (bbox-init) reads this file to
// mount additional virtiofs shares.
func InjectMountConfig(mounts []workspace.MountRequest) func(string, *image.OCIConfig) error {
	return func(rootfsDir string, _ *image.OCIConfig) error {
		entries := make([]mountEntry, len(mounts))
		for i, m := range mounts {
			entries[i] = mountEntry{
				Tag:       m.Tag,
				GuestPath: m.GuestPath,
				ReadOnly:  m.ReadOnly,
			}
		}

		data, err := json.Marshal(entries)
		if err != nil {
			return fmt.Errorf("marshaling mount config: %w", err)
		}

		path := filepath.Join(rootfsDir, mountConfigPath)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("creating mount config directory: %w", err)
		}

		if err := os.WriteFile(path, data, 0o644); err != nil {
			return fmt.Errorf("writing mount config: %w", err)
		}

		return nil
	}
}
