// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"log/slog"
	"os"

	"github.com/stacklok/go-microvm/image"

	"github.com/stacklok/brood-box/pkg/domain/settings"
)

// InjectSettings returns a RootFS hook that copies filtered agent settings
// from the host into the guest rootfs before VM boot.
func InjectSettings(injector settings.Injector, manifest *settings.Manifest) func(string, *image.OCIConfig) error {
	return func(rootfsPath string, _ *image.OCIConfig) error {
		if injector == nil || manifest == nil || len(manifest.Entries) == 0 {
			return nil
		}

		hostHome, err := os.UserHomeDir()
		if err != nil {
			slog.Warn("cannot resolve host home for settings injection", "error", err)
			return nil
		}

		slog.Debug("injecting agent settings into rootfs",
			"entries", len(manifest.Entries),
		)

		return injector.Inject(rootfsPath, hostHome, *manifest)
	}
}
