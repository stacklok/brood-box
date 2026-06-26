// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/stacklok/go-microvm/image"

	"github.com/stacklok/brood-box/internal/infra/vm/initbin"
	domainagent "github.com/stacklok/brood-box/pkg/domain/agent"
)

// InjectInitBinary returns a RootFS hook that writes the embedded bbox-init
// binary to /bbox-init in the guest rootfs. This replaces the former shell
// init script and its dependencies (dropbear, iproute2, mount).
func InjectInitBinary() func(string, *image.OCIConfig) error {
	return func(rootfsPath string, _ *image.OCIConfig) error {
		if len(initbin.Binary) == 0 {
			return fmt.Errorf("bbox-init binary not embedded: rebuild bbox with the 'bbox_full' build tag (e.g. `task build`)")
		}
		initPath := filepath.Join(rootfsPath, "bbox-init")
		if err := os.WriteFile(initPath, initbin.Binary, 0o755); err != nil {
			return fmt.Errorf("writing init binary: %w", err)
		}
		return nil
	}
}

// InjectMCPConfig returns a RootFS hook that delegates MCP config emission
// to the agent's MCPInjector. Returns a no-op hook when injector is nil.
func InjectMCPConfig(injector domainagent.MCPInjector, gatewayIP string, port uint16, chown domainagent.ChownFunc) func(string, *image.OCIConfig) error {
	return func(rootfsPath string, _ *image.OCIConfig) error {
		if injector == nil {
			return nil
		}
		return injector.Inject(rootfsPath, gatewayIP, port, chown)
	}
}
