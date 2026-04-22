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
		initPath := filepath.Join(rootfsPath, "bbox-init")
		if err := os.WriteFile(initPath, initbin.Binary, 0o755); err != nil {
			return fmt.Errorf("writing init binary: %w", err)
		}
		return nil
	}
}

// InjectMCPConfig returns a RootFS hook that writes agent-specific MCP
// configuration files so the agent discovers the vmcp endpoint on boot.
func InjectMCPConfig(format domainagent.MCPConfigFormat, gatewayIP string, port uint16, chown ChownFunc) func(string, *image.OCIConfig) error {
	return func(rootfsPath string, _ *image.OCIConfig) error {
		switch format {
		case domainagent.MCPConfigFormatClaudeCode:
			return injectClaudeCodeMCP(rootfsPath, gatewayIP, port, chown)
		case domainagent.MCPConfigFormatCodex:
			return injectCodexMCP(rootfsPath, gatewayIP, port, chown)
		case domainagent.MCPConfigFormatOpenCode:
			return injectOpenCodeMCP(rootfsPath, gatewayIP, port, chown)
		case domainagent.MCPConfigFormatHermes:
			return injectHermesMCP(rootfsPath, gatewayIP, port, chown)
		default:
			return nil
		}
	}
}
