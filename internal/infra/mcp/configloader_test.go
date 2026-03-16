// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadMCPFileConfig_MissingFile(t *testing.T) {
	t.Parallel()
	cfg, err := LoadMCPFileConfig("/nonexistent/path.yaml")
	assert.NoError(t, err)
	assert.Nil(t, cfg)
}

func TestLoadMCPFileConfig_ValidAuthz(t *testing.T) {
	t.Parallel()

	content := `
authz:
  policies:
    - 'permit(principal, action == Action::"list_tools", resource);'
    - 'permit(principal, action == Action::"call_tool", resource == Tool::"search_code");'
`
	path := writeTestFile(t, content)
	cfg, err := LoadMCPFileConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.NotNil(t, cfg.Authz)
	assert.Len(t, cfg.Authz.Policies, 2)
}

func TestLoadMCPFileConfig_ValidAggregation(t *testing.T) {
	t.Parallel()

	content := `
aggregation:
  conflict_resolution: prefix
  prefix_format: "{workload}_"
  tools:
    - workload: github
      filter:
        - search_code
`
	path := writeTestFile(t, content)
	cfg, err := LoadMCPFileConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.NotNil(t, cfg.Aggregation)
	assert.Equal(t, "prefix", cfg.Aggregation.ConflictResolution)
	assert.Equal(t, "{workload}_", cfg.Aggregation.PrefixFormat)
	require.Len(t, cfg.Aggregation.Tools, 1)
	assert.Equal(t, "github", cfg.Aggregation.Tools[0].Workload)
}

func TestLoadMCPFileConfig_FullConfig(t *testing.T) {
	t.Parallel()

	content := `
authz:
  policies:
    - 'permit(principal, action == Action::"list_tools", resource);'
aggregation:
  conflict_resolution: priority
  priority_order:
    - github
    - context7
  tools:
    - workload: fetch
      exclude_all: true
`
	path := writeTestFile(t, content)
	cfg, err := LoadMCPFileConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.NotNil(t, cfg.Authz)
	assert.Len(t, cfg.Authz.Policies, 1)
	require.NotNil(t, cfg.Aggregation)
	assert.Equal(t, "priority", cfg.Aggregation.ConflictResolution)
	assert.Equal(t, []string{"github", "context7"}, cfg.Aggregation.PriorityOrder)
}

func TestLoadMCPFileConfig_InvalidYAML(t *testing.T) {
	t.Parallel()

	path := writeTestFile(t, ":\n  :\n  - :\n  - invalid: [")
	_, err := LoadMCPFileConfig(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "parsing MCP config")
}

func TestLoadMCPFileConfig_ValidationError_EmptyPolicies(t *testing.T) {
	t.Parallel()

	content := `
authz:
  policies: []
`
	path := writeTestFile(t, content)
	_, err := LoadMCPFileConfig(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "authz.policies must be non-empty")
}

func TestLoadMCPFileConfig_ValidationError_BadConflictResolution(t *testing.T) {
	t.Parallel()

	content := `
aggregation:
  conflict_resolution: invalid
`
	path := writeTestFile(t, content)
	_, err := LoadMCPFileConfig(path)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "conflict_resolution must be one of")
}

func TestLoadMCPFileConfig_EmptyFile(t *testing.T) {
	t.Parallel()

	path := writeTestFile(t, "")
	cfg, err := LoadMCPFileConfig(path)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Nil(t, cfg.Authz)
	assert.Nil(t, cfg.Aggregation)
}

func writeTestFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp-config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}
