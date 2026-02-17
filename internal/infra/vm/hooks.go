// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/stacklok/propolis/image"

	domainagent "github.com/stacklok/apiary/internal/domain/agent"
	"github.com/stacklok/apiary/internal/infra/vm/initbin"
)

// InjectInitBinary returns a RootFS hook that writes the embedded apiary-init
// binary to /apiary-init in the guest rootfs. This replaces the former shell
// init script and its dependencies (dropbear, iproute2, mount).
func InjectInitBinary() func(string, *image.OCIConfig) error {
	return func(rootfsPath string, _ *image.OCIConfig) error {
		initPath := filepath.Join(rootfsPath, "apiary-init")
		if err := os.WriteFile(initPath, initbin.Binary, 0o755); err != nil {
			return fmt.Errorf("writing init binary: %w", err)
		}
		return nil
	}
}

// InjectMCPConfig returns a RootFS hook that writes agent-specific MCP
// configuration files so the agent discovers the vmcp endpoint on boot.
func InjectMCPConfig(format domainagent.MCPConfigFormat, gatewayIP string, port uint16) func(string, *image.OCIConfig) error {
	return func(rootfsPath string, _ *image.OCIConfig) error {
		switch format {
		case domainagent.MCPConfigFormatClaudeCode:
			return injectClaudeCodeMCP(rootfsPath, gatewayIP, port)
		case domainagent.MCPConfigFormatCodex:
			return injectCodexMCP(rootfsPath, gatewayIP, port)
		case domainagent.MCPConfigFormatOpenCode:
			return injectOpenCodeMCP(rootfsPath, gatewayIP, port)
		default:
			return nil
		}
	}
}
