// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestHumanSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		bytes    int64
		expected string
	}{
		{0, "0 B"},
		{100, "100 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 500, "500.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
		{int64(1024) * 1024 * 1024 * 3, "3.0 GB"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, humanSize(tt.bytes), "humanSize(%d)", tt.bytes)
	}
}

func TestTimeAgo(t *testing.T) {
	t.Parallel()

	tests := []struct {
		offset   time.Duration
		expected string
	}{
		{5 * time.Second, "just now"},
		{1 * time.Minute, "1 minute ago"},
		{30 * time.Minute, "30 minutes ago"},
		{1 * time.Hour, "1 hour ago"},
		{5 * time.Hour, "5 hours ago"},
		{24 * time.Hour, "1 day ago"},
		{72 * time.Hour, "3 days ago"},
	}

	for _, tt := range tests {
		result := timeAgo(time.Now().Add(-tt.offset))
		assert.Equal(t, tt.expected, result, "timeAgo(-%v)", tt.offset)
	}
}

func TestShortDigest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"sha256:abcdef123456abcdef", "sha256:abcdef123456"},
		{"sha256:short", "sha256:short"},
		{"sha256:exactlytwelv", "sha256:exactlytwelv"},
		{"no-colon", "no-colon"},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, shortDigest(tt.input), "shortDigest(%q)", tt.input)
	}
}

func TestCacheCmd_SubcommandsExist(t *testing.T) {
	t.Parallel()

	cmd := cacheCmd()
	assert.NotNil(t, cmd)

	// Verify subcommands are registered.
	subCmds := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		subCmds[sub.Name()] = true
	}

	assert.True(t, subCmds["list"], "missing 'list' subcommand")
	assert.True(t, subCmds["gc"], "missing 'gc' subcommand")
	assert.True(t, subCmds["purge"], "missing 'purge' subcommand")
}

func TestCacheGCCmd_DryRunFlagRegistered(t *testing.T) {
	t.Parallel()

	cmd := cacheGCCmd()
	flag := cmd.Flags().Lookup("dry-run")
	assert.NotNil(t, flag, "missing --dry-run flag")
	assert.Equal(t, "false", flag.DefValue)
}

func TestCachePurgeCmd_ForceFlagRegistered(t *testing.T) {
	t.Parallel()

	cmd := cachePurgeCmd()
	flag := cmd.Flags().Lookup("force")
	assert.NotNil(t, flag, "missing --force flag")
	assert.Equal(t, "false", flag.DefValue)
}
