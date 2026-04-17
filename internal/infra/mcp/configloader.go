// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/stacklok/brood-box/internal/infra/configfile"
	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
)

// LoadMCPFileConfig reads and validates an MCP config from a YAML file
// supplied via the operator's --mcp-config flag. Operator-owned paths
// skip the workspace-local symlink rejection but still get the size
// cap and strict-unknown-fields checking.
// Returns (nil, nil) if the file does not exist.
func LoadMCPFileConfig(path string) (*domainconfig.MCPFileConfig, error) {
	data, err := configfile.ReadFile(path, configfile.ReadOptions{})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading MCP config %s: %w", path, err)
	}

	var cfg domainconfig.MCPFileConfig
	if err := configfile.DecodeStrict(data, &cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return &domainconfig.MCPFileConfig{}, nil
		}
		return nil, fmt.Errorf("parsing MCP config %s: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validating MCP config %s: %w", path, err)
	}

	return &cfg, nil
}
