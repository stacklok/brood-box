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

	"github.com/stacklok/brood-box/pkg/domain/snapshot"
)

func TestLoadExcludeConfig_NoFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	cfg, err := LoadExcludeConfig(dir, nil, logger)
	require.NoError(t, err)

	assert.NotEmpty(t, cfg.SecurityPatterns)
	assert.NotEmpty(t, cfg.DiffSecurityPatterns)
	assert.NotEmpty(t, cfg.PerformancePatterns)
	assert.Empty(t, cfg.FilePatterns)
	assert.Empty(t, cfg.CLIPatterns)
}

func TestLoadExcludeConfig_WithSandboxIgnore(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	content := "# Comment line\n*.tmp\nlogs/\n\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".broodboxignore"), []byte(content), 0o644))

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
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".broodboxignore"), []byte(content), 0o644))

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

func TestNewDiffMatcher_MergesDiffSecurityPatterns(t *testing.T) {
	t.Parallel()

	cfg := snapshot.ExcludeConfig{
		SecurityPatterns:     snapshot.DefaultSecurityPatterns(),
		DiffSecurityPatterns: snapshot.DefaultDiffSecurityPatterns(),
		PerformancePatterns:  snapshot.DefaultPerformancePatterns(),
	}

	m := NewDiffMatcher(cfg, nil)

	// Shared security pattern should match.
	assert.True(t, m.Match(".env"), "shared security pattern .env should match")
	// Diff-only security pattern should match .git file (worktree pointer).
	assert.True(t, m.Match(".git"), "diff security pattern .git should match")
	// Diff-only security pattern should match .git/ directory contents.
	assert.True(t, m.Match(".git/hooks/pre-commit"), ".git/ contents should match via diff security")
	// Regular file should not match.
	assert.False(t, m.Match("main.go"), "regular file should not match")
}

func TestNewDiffMatcher_GitignorePatternsIncluded(t *testing.T) {
	t.Parallel()

	cfg := snapshot.ExcludeConfig{
		SecurityPatterns:     snapshot.DefaultSecurityPatterns(),
		DiffSecurityPatterns: snapshot.DefaultDiffSecurityPatterns(),
	}

	m := NewDiffMatcher(cfg, []string{"*.o", "build/"})

	// Gitignore patterns should match.
	assert.True(t, m.Match("foo.o"), "gitignore pattern *.o should match")
	assert.True(t, m.Match("build/output"), "gitignore pattern build/ should match")
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
	// .git must NOT be excluded in snapshot matcher — agent needs it for git operations.
	// Only the diff matcher (NewDiffMatcher) should exclude .git.
	assert.False(t, m.Match(".git"), ".git should not be excluded in snapshot matcher")
}
