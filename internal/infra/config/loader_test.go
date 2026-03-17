// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
)

func TestLoader_Load_FileNotExist(t *testing.T) {
	t.Parallel()
	loader := NewLoader("/nonexistent/path/config.yaml")
	cfg, err := loader.Load()
	require.NoError(t, err)
	assert.NotNil(t, cfg)
	assert.Zero(t, cfg.Defaults.CPUs)
	assert.Zero(t, cfg.Defaults.Memory)
	assert.Nil(t, cfg.Agents)
}

func TestLoader_Load_ValidConfig(t *testing.T) {
	t.Parallel()

	content := `
defaults:
  cpus: 4
  memory: 4096

agents:
  claude-code:
    env_forward:
      - ANTHROPIC_API_KEY
      - "CLAUDE_*"
      - GITHUB_TOKEN
  my-custom-agent:
    image: ghcr.io/me/my-agent:latest
    command: ["my-agent", "--interactive"]
    env_forward:
      - MY_API_KEY
    cpus: 2
    memory: 1024
`

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	loader := NewLoader(path)
	cfg, err := loader.Load()
	require.NoError(t, err)

	assert.Equal(t, uint32(4), cfg.Defaults.CPUs)
	assert.Equal(t, domainconfig.ByteSize(4096), cfg.Defaults.Memory)

	require.Contains(t, cfg.Agents, "claude-code")
	cc := cfg.Agents["claude-code"]
	assert.Equal(t, []string{"ANTHROPIC_API_KEY", "CLAUDE_*", "GITHUB_TOKEN"}, cc.EnvForward)

	require.Contains(t, cfg.Agents, "my-custom-agent")
	custom := cfg.Agents["my-custom-agent"]
	assert.Equal(t, "ghcr.io/me/my-agent:latest", custom.Image)
	assert.Equal(t, []string{"my-agent", "--interactive"}, custom.Command)
	assert.Equal(t, uint32(2), custom.CPUs)
	assert.Equal(t, domainconfig.ByteSize(1024), custom.Memory)
}

func TestLoader_Load_InvalidYAML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("{{invalid yaml"), 0o644))

	loader := NewLoader(path)
	_, err := loader.Load()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parsing config file")
}

func TestLoadFromPath_MissingFile(t *testing.T) {
	t.Parallel()
	cfg, err := LoadFromPath("/nonexistent/path/config.yaml")
	require.NoError(t, err)
	assert.Nil(t, cfg)
}

func TestLoadFromPath_ValidYAML(t *testing.T) {
	t.Parallel()

	content := `
defaults:
  cpus: 8
  memory: 8192
review:
  exclude_patterns:
    - "*.tmp"
`
	dir := t.TempDir()
	path := filepath.Join(dir, ".broodbox.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

	cfg, err := LoadFromPath(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, uint32(8), cfg.Defaults.CPUs)
	assert.Equal(t, domainconfig.ByteSize(8192), cfg.Defaults.Memory)
	assert.Equal(t, []string{"*.tmp"}, cfg.Review.ExcludePatterns)
}

func TestLoadFromPath_EmptyFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, ".broodbox.yaml")
	require.NoError(t, os.WriteFile(path, []byte(""), 0o644))

	cfg, err := LoadFromPath(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Zero(t, cfg.Defaults.CPUs)
}

func TestLoadFromPath_InvalidYAML(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, ".broodbox.yaml")
	require.NoError(t, os.WriteFile(path, []byte("{{invalid yaml"), 0o644))

	_, err := LoadFromPath(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parsing config file")
}

func TestLoader_DefaultPath(t *testing.T) {
	t.Parallel()
	loader := NewLoader("")
	assert.Contains(t, loader.Path(), "broodbox")
	assert.Contains(t, loader.Path(), "config.yaml")
}
