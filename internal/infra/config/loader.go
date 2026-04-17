// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package config provides YAML configuration loading from disk.
package config

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/stacklok/brood-box/internal/infra/configfile"
	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
)

// Loader implements ConfigLoader by reading a YAML file from disk.
type Loader struct {
	path string
}

// NewLoader creates a Loader that reads from the given path.
// If path is empty, it defaults to ~/.config/broodbox/config.yaml
// (respecting XDG_CONFIG_HOME).
func NewLoader(path string) *Loader {
	if path == "" {
		path = defaultConfigPath()
	}
	return &Loader{path: path}
}

// Load reads and parses the config file. If the file does not exist,
// it returns a zero-value Config (no error).
func (l *Loader) Load() (*domainconfig.Config, error) {
	data, err := configfile.ReadFile(l.path, configfile.ReadOptions{})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &domainconfig.Config{}, nil
		}
		return nil, fmt.Errorf("reading config file %s: %w", l.path, err)
	}

	var cfg domainconfig.Config
	if err := configfile.DecodeStrict(data, &cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return &domainconfig.Config{}, nil
		}
		return nil, fmt.Errorf("parsing config file %s: %w", l.path, err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validating config file %s: %w", l.path, err)
	}

	return &cfg, nil
}

// Path returns the resolved config file path.
func (l *Loader) Path() string {
	return l.path
}

// LoadFromPath reads and parses a workspace-local config file at the
// given path. Workspace-local hardening applies: symlinks are rejected
// because the path is attacker-controllable (a malicious repo's
// `.broodbox.yaml` could otherwise be a symlink to another readable
// file on the host).
//
// Returns (nil, nil) when the file does not exist.
// Returns a parsed Config for any existing file (including empty files).
func LoadFromPath(path string) (*domainconfig.Config, error) {
	data, err := configfile.ReadFile(path, configfile.ReadOptions{RejectSymlinks: true})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading config file %s: %w", path, err)
	}

	var cfg domainconfig.Config
	if err := configfile.DecodeStrict(data, &cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return &domainconfig.Config{}, nil
		}
		return nil, fmt.Errorf("parsing config file %s: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validating config file %s: %w", path, err)
	}

	return &cfg, nil
}

func defaultConfigPath() string {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "broodbox", "config.yaml")
}
