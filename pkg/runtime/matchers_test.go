// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildSnapshotMatchers_RespectsGitignore(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()

	gitignore := filepath.Join(workspace, ".gitignore")
	require.NoError(t, os.WriteFile(gitignore, []byte("coverage/\n"), 0o600))

	snapshotMatcher, diffMatcher, err := BuildSnapshotMatchers(workspace, nil, nil)
	require.NoError(t, err)

	require.False(t, snapshotMatcher.Match("coverage/output.txt"))
	require.True(t, diffMatcher.Match("coverage/output.txt"))
}

func TestBuildSnapshotMatchers_AlwaysExcludesSecurityPatterns(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()

	snapshotMatcher, diffMatcher, err := BuildSnapshotMatchers(workspace, nil, nil)
	require.NoError(t, err)

	require.True(t, snapshotMatcher.Match(".env"))
	require.True(t, diffMatcher.Match(".env"))
}
