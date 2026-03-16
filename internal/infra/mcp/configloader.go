// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"

	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
)

// LoadMCPFileConfig reads and validates an MCP config from a YAML file.
// Returns (nil, nil) if the file does not exist.
func LoadMCPFileConfig(path string) (*domainconfig.MCPFileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading MCP config %s: %w", path, err)
	}

	var cfg domainconfig.MCPFileConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing MCP config %s: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validating MCP config %s: %w", path, err)
	}

	return &cfg, nil
}
