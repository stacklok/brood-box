// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"log/slog"

	"github.com/stacklok/go-microvm/image"

	"github.com/stacklok/brood-box/pkg/domain/credential"
)

// InjectCredentials returns a RootFS hook that restores saved agent
// credentials into the guest rootfs before VM boot.
func InjectCredentials(store credential.Store, agentName string, credPaths []string) func(string, *image.OCIConfig) error {
	return func(rootfsPath string, _ *image.OCIConfig) error {
		if len(credPaths) == 0 {
			return nil
		}

		slog.Debug("injecting credentials into rootfs",
			"agent", agentName,
			"paths", credPaths,
		)

		return store.Inject(rootfsPath, agentName, credPaths)
	}
}
