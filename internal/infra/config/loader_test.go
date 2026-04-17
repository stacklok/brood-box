// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/brood-box/pkg/domain/bytesize"
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
	assert.Equal(t, bytesize.ByteSize(4096), cfg.Defaults.Memory)

	require.Contains(t, cfg.Agents, "claude-code")
	cc := cfg.Agents["claude-code"]
	assert.Equal(t, []string{"ANTHROPIC_API_KEY", "CLAUDE_*", "GITHUB_TOKEN"}, cc.EnvForward)

	require.Contains(t, cfg.Agents, "my-custom-agent")
	custom := cfg.Agents["my-custom-agent"]
	assert.Equal(t, "ghcr.io/me/my-agent:latest", custom.Image)
	assert.Equal(t, []string{"my-agent", "--interactive"}, custom.Command)
	assert.Equal(t, uint32(2), custom.CPUs)
	assert.Equal(t, bytesize.ByteSize(1024), custom.Memory)
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
	assert.Equal(t, bytesize.ByteSize(8192), cfg.Defaults.Memory)
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

func TestLoader_Load_RejectsWildcardEnvForward(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
agents:
  claude-code:
    env_forward:
      - "*"
`), 0o644))

	loader := NewLoader(path)
	_, err := loader.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validating config file")
	assert.Contains(t, err.Error(), "agents.claude-code")
	assert.Contains(t, err.Error(), `bare "*"`)
}

func TestLoadFromPath_RejectsWildcardEnvForward(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, ".broodbox.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
agents:
  claude-code:
    env_forward:
      - "*"
`), 0o644))

	_, err := LoadFromPath(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "validating config file")
	assert.Contains(t, err.Error(), `bare "*"`)
}

func TestLoadFromPath_RejectsEmptyEnvForward(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, ".broodbox.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
agents:
  claude-code:
    env_forward:
      - ""
`), 0o644))

	_, err := LoadFromPath(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty pattern")
}

func TestLoadFromPath_RejectsUnknownFields(t *testing.T) {
	t.Parallel()

	// `mcp.athz.profile` (typo for `mcp.authz.profile`) must not be
	// silently dropped — that would let a repo author *think* they've
	// tightened to `observe` while the operator falls back to
	// `full-access`.
	dir := t.TempDir()
	path := filepath.Join(dir, ".broodbox.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
mcp:
  athz:
    profile: observe
`), 0o644))

	_, err := LoadFromPath(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing config file")
}

func TestLoader_Load_RejectsUnknownFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
nonexistent_top_level: true
`), 0o644))

	loader := NewLoader(path)
	_, err := loader.Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing config file")
}

func TestLoadFromPath_RejectsOversizedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, ".broodbox.yaml")
	// 2 MiB of padded YAML comments — exceeds the 1 MiB cap.
	big := make([]byte, 2*1024*1024)
	for i := range big {
		big[i] = '#'
	}
	require.NoError(t, os.WriteFile(path, big, 0o644))

	_, err := LoadFromPath(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "too large")
}

func TestLoadFromPath_RejectsSymlink(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "target.yaml")
	require.NoError(t, os.WriteFile(target, []byte("defaults:\n  cpus: 2\n"), 0o644))

	linkPath := filepath.Join(dir, ".broodbox.yaml")
	require.NoError(t, os.Symlink(target, linkPath))

	_, err := LoadFromPath(linkPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlinks are not allowed")
}

func TestLoadFromPath_RejectsNonRegularFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, ".broodbox.yaml")
	// A FIFO (named pipe) is the realistic non-regular case: `os.Open`
	// without a writer blocks forever. Must be rejected up-front.
	require.NoError(t, syscall.Mkfifo(path, 0o600))

	done := make(chan error, 1)
	go func() {
		_, err := LoadFromPath(path)
		done <- err
	}()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a regular file")
	case <-time.After(5 * time.Second):
		t.Fatal("LoadFromPath blocked on FIFO — should have rejected it immediately")
	}
}

func TestLoader_Load_FollowsSymlink(t *testing.T) {
	t.Parallel()

	// The global-config path is operator-supplied — symlinks are a
	// legitimate operator choice (e.g. pointing at a team-shared file),
	// so they must NOT be rejected on the Load path.
	dir := t.TempDir()
	target := filepath.Join(dir, "target.yaml")
	require.NoError(t, os.WriteFile(target, []byte("defaults:\n  cpus: 3\n"), 0o644))

	linkPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.Symlink(target, linkPath))

	loader := NewLoader(linkPath)
	cfg, err := loader.Load()
	require.NoError(t, err)
	assert.Equal(t, uint32(3), cfg.Defaults.CPUs)
}
