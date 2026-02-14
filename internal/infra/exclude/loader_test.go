// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package exclude

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadExcludeConfig_NoFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfg, err := LoadExcludeConfig(dir, nil, logger)
	require.NoError(t, err)

	assert.NotEmpty(t, cfg.SecurityPatterns)
	assert.NotEmpty(t, cfg.PerformancePatterns)
	assert.Empty(t, cfg.FilePatterns)
	assert.Empty(t, cfg.CLIPatterns)
}

func TestLoadExcludeConfig_WithSandboxIgnore(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	content := "# Comment line\n*.tmp\nlogs/\n\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".sandboxignore"), []byte(content), 0o644))

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfg, err := LoadExcludeConfig(dir, []string{"extra/"}, logger)
	require.NoError(t, err)

	assert.Equal(t, []string{"*.tmp", "logs/"}, cfg.FilePatterns)
	assert.Equal(t, []string{"extra/"}, cfg.CLIPatterns)
}

func TestLoadExcludeConfig_SecurityNegationStripped(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Attempt to negate security patterns — should be stripped.
	content := "!.env*\n!*.pem\nkeep-this-pattern\n!node_modules/\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".sandboxignore"), []byte(content), 0o644))

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfg, err := LoadExcludeConfig(dir, nil, logger)
	require.NoError(t, err)

	// Security negations stripped, performance negation kept, regular pattern kept.
	assert.Equal(t, []string{"keep-this-pattern", "!node_modules/"}, cfg.FilePatterns)
}

func TestLoadExcludeConfig_CLIPatterns(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfg, err := LoadExcludeConfig(dir, []string{"*.bak", "temp/"}, logger)
	require.NoError(t, err)

	assert.Equal(t, []string{"*.bak", "temp/"}, cfg.CLIPatterns)
}

func TestNewMatcherFromConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfg, err := LoadExcludeConfig(dir, nil, logger)
	require.NoError(t, err)

	m := NewMatcherFromConfig(cfg)

	// Security pattern should match.
	assert.True(t, m.Match(".env"))
	// Performance pattern should match.
	assert.True(t, m.Match("node_modules/foo.js"))
	// Regular file should not match.
	assert.False(t, m.Match("main.go"))
}
