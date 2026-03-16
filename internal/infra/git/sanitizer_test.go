// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package git

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSanitizeConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "allowed sections pass through unchanged",
			input: strings.Join([]string{
				"[core]",
				"\trepositoryformatversion = 0",
				"\tfilemode = true",
				"\tbare = false",
				`[remote "origin"]`,
				"\turl = https://github.com/org/repo.git",
				"\tfetch = +refs/heads/*:refs/remotes/origin/*",
				`[branch "main"]`,
				"\tremote = origin",
				"\tmerge = refs/heads/main",
			}, "\n"),
			expected: strings.Join([]string{
				"[core]",
				"\trepositoryformatversion = 0",
				"\tfilemode = true",
				"\tbare = false",
				`[remote "origin"]`,
				"\turl = https://github.com/org/repo.git",
				"\tfetch = +refs/heads/*:refs/remotes/origin/*",
				`[branch "main"]`,
				"\tremote = origin",
				"\tmerge = refs/heads/main",
			}, "\n"),
		},
		{
			name: "blocked sections stripped entirely including alias with shell commands",
			input: strings.Join([]string{
				"[core]",
				"\tbare = false",
				"[alias]",
				"\tco = !git checkout",
				"\tst = status",
				"[credential]",
				"\thelper = store",
				"[color]",
				"\tui = auto",
			}, "\n"),
			expected: strings.Join([]string{
				"[core]",
				"\tbare = false",
				"[color]",
				"\tui = auto",
			}, "\n"),
		},
		{
			name: "case-insensitive blocking",
			input: strings.Join([]string{
				"[CREDENTIAL]",
				"\thelper = store",
				"[Http]",
				"\tsslVerify = false",
				"[core]",
				"\tbare = false",
			}, "\n"),
			expected: strings.Join([]string{
				"[core]",
				"\tbare = false",
			}, "\n"),
		},
		{
			name: "subsection does not affect section name blocking",
			input: strings.Join([]string{
				`[credential "https://github.com"]`,
				"\thelper = osxkeychain",
				"[core]",
				"\tbare = false",
			}, "\n"),
			expected: strings.Join([]string{
				"[core]",
				"\tbare = false",
			}, "\n"),
		},
		{
			name: "includeIf correctly blocked",
			input: strings.Join([]string{
				`[includeIf "gitdir:/home/user/work/"]`,
				"\tpath = /home/user/.gitconfig-work",
				"[core]",
				"\tbare = false",
			}, "\n"),
			expected: strings.Join([]string{
				"[core]",
				"\tbare = false",
			}, "\n"),
		},
		{
			name: "URL credentials removed from remote",
			input: strings.Join([]string{
				`[remote "origin"]`,
				"\turl = https://user:token@github.com/org/repo.git",
				"\tfetch = +refs/heads/*:refs/remotes/origin/*",
			}, "\n"),
			expected: strings.Join([]string{
				`[remote "origin"]`,
				"\turl = https://github.com/org/repo.git",
				"\tfetch = +refs/heads/*:refs/remotes/origin/*",
			}, "\n"),
		},
		{
			name: "SCP-like URLs pass through",
			input: strings.Join([]string{
				`[remote "origin"]`,
				"\turl = git@github.com:org/repo.git",
				"\tfetch = +refs/heads/*:refs/remotes/origin/*",
			}, "\n"),
			expected: strings.Join([]string{
				`[remote "origin"]`,
				"\turl = git@github.com:org/repo.git",
				"\tfetch = +refs/heads/*:refs/remotes/origin/*",
			}, "\n"),
		},
		{
			name: "malformed HTTPS URL with credentials fail closed",
			input: strings.Join([]string{
				`[remote "origin"]`,
				"\turl = http{s://user:pass@github.com/org/repo.git",
				"\tfetch = +refs/heads/*:refs/remotes/origin/*",
			}, "\n"),
			expected: strings.Join([]string{
				`[remote "origin"]`,
				"\tfetch = +refs/heads/*:refs/remotes/origin/*",
			}, "\n"),
		},
		{
			name: "user section name and email pass through signingkey stripped",
			input: strings.Join([]string{
				"[user]",
				"\tname = John Doe",
				"\temail = john@example.com",
				"\tsigningkey = ABCDEF1234567890",
			}, "\n"),
			expected: strings.Join([]string{
				"[user]",
				"\tname = John Doe",
				"\temail = john@example.com",
			}, "\n"),
		},
		{
			name: "backslash continuation stays in same section context",
			input: strings.Join([]string{
				"[core]",
				"\tpager = less \\",
				"\t-FRSX",
				"\tbare = false",
			}, "\n"),
			expected: strings.Join([]string{
				"[core]",
				"\tpager = less \\",
				"\t-FRSX",
				"\tbare = false",
			}, "\n"),
		},
		{
			name: "backslash continuation fake section inside continued value is not parsed as header",
			input: strings.Join([]string{
				"[core]",
				"\tpager = some-value \\",
				"[credential]",
				"\tbare = false",
			}, "\n"),
			expected: strings.Join([]string{
				"[core]",
				"\tpager = some-value \\",
				"[credential]",
				"\tbare = false",
			}, "\n"),
		},
		{
			name:     "empty input produces empty output",
			input:    "",
			expected: "",
		},
		{
			name: "comments and blank lines preserved in allowed sections dropped in blocked",
			input: strings.Join([]string{
				"[core]",
				"\t# This is a comment",
				"\tbare = false",
				"",
				"[credential]",
				"\t# Credential comment",
				"\thelper = store",
				"",
				"[color]",
				"\t; semicolon comment",
				"\tui = auto",
			}, "\n"),
			expected: strings.Join([]string{
				"[core]",
				"\t# This is a comment",
				"\tbare = false",
				"",
				"[color]",
				"\t; semicolon comment",
				"\tui = auto",
			}, "\n"),
		},
		{
			name: "unknown sections not in either list are dropped fail-safe",
			input: strings.Join([]string{
				"[core]",
				"\tbare = false",
				"[somethingnew]",
				"\tkey = value",
				"[color]",
				"\tui = auto",
			}, "\n"),
			expected: strings.Join([]string{
				"[core]",
				"\tbare = false",
				"[color]",
				"\tui = auto",
			}, "\n"),
		},
		{
			name: "submodule URL sanitization works like remote",
			input: strings.Join([]string{
				`[submodule "lib"]`,
				"\tpath = lib",
				"\turl = https://user:pass@github.com/org/lib.git",
			}, "\n"),
			expected: strings.Join([]string{
				`[submodule "lib"]`,
				"\tpath = lib",
				"\turl = https://github.com/org/lib.git",
			}, "\n"),
		},
		{
			name: "remote pushurl is also sanitized",
			input: strings.Join([]string{
				`[remote "origin"]`,
				"\turl = https://github.com/org/repo.git",
				"\tpushurl = https://deploy:token@github.com/org/repo.git",
			}, "\n"),
			expected: strings.Join([]string{
				`[remote "origin"]`,
				"\turl = https://github.com/org/repo.git",
				"\tpushurl = https://github.com/org/repo.git",
			}, "\n"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := SanitizeConfig(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestProcess_ReadsAndWritesSanitizedConfig(t *testing.T) {
	t.Parallel()

	originalDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Set up .git/config in the original workspace.
	gitDir := filepath.Join(originalDir, ".git")
	require.NoError(t, os.MkdirAll(gitDir, 0o755))

	configContent := strings.Join([]string{
		"[core]",
		"\tbare = false",
		"[credential]",
		"\thelper = store",
		`[remote "origin"]`,
		"\turl = https://user:token@github.com/org/repo.git",
	}, "\n")
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "config"), []byte(configContent), 0o644))

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	sanitizer := NewConfigSanitizer(logger)

	err := sanitizer.Process(context.Background(), originalDir, snapshotDir)
	require.NoError(t, err)

	// Read the sanitized config from the snapshot.
	configPath := filepath.Join(snapshotDir, ".git", "config")
	result, err := os.ReadFile(configPath)
	require.NoError(t, err)

	expected := strings.Join([]string{
		"[core]",
		"\tbare = false",
		`[remote "origin"]`,
		"\turl = https://github.com/org/repo.git",
	}, "\n")
	assert.Equal(t, expected, string(result))

	// Verify config is world-readable (0644) since credentials are stripped.
	info, err := os.Stat(configPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o644), info.Mode().Perm(),
		"sanitized config should be world-readable (0644)")
}

func TestProcess_NoGitConfig_NoOp(t *testing.T) {
	t.Parallel()

	originalDir := t.TempDir()
	snapshotDir := t.TempDir()

	// No .git directory at all.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	sanitizer := NewConfigSanitizer(logger)

	err := sanitizer.Process(context.Background(), originalDir, snapshotDir)
	require.NoError(t, err)

	// Snapshot should not have a .git directory.
	_, err = os.Stat(filepath.Join(snapshotDir, ".git", "config"))
	assert.True(t, os.IsNotExist(err))
}

func TestContainsCredentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name: "URL with user:token detects credential",
			input: strings.Join([]string{
				`[remote "origin"]`,
				"\turl = https://user:token@github.com/org/repo.git",
			}, "\n"),
			expected: true,
		},
		{
			name: "credential section header detected",
			input: strings.Join([]string{
				"[credential]",
				"\thelper = store",
			}, "\n"),
			expected: true,
		},
		{
			name: "credential subsection header detected",
			input: strings.Join([]string{
				`[credential "https://github.com"]`,
				"\thelper = osxkeychain",
			}, "\n"),
			expected: true,
		},
		{
			name: "pushurl with deploy:token detects credential",
			input: strings.Join([]string{
				`[remote "origin"]`,
				"\tpushurl = https://deploy:token@github.com/org/repo.git",
			}, "\n"),
			expected: true,
		},
		{
			name: "SCP-style SSH remote is not a credential",
			input: strings.Join([]string{
				`[remote "origin"]`,
				"\turl = git@github.com:org/repo.git",
			}, "\n"),
			expected: false,
		},
		{
			name: "HTTPS without credentials is clean",
			input: strings.Join([]string{
				`[remote "origin"]`,
				"\turl = https://github.com/org/repo.git",
			}, "\n"),
			expected: false,
		},
		{
			name: "ssh scheme URL with username only is not a credential",
			input: strings.Join([]string{
				`[remote "origin"]`,
				"\turl = ssh://git@github.com/org/repo.git",
			}, "\n"),
			expected: false,
		},
		{
			name: "credentials in comment are ignored",
			input: strings.Join([]string{
				"[core]",
				"\t# url = https://user:token@github.com/org/repo.git",
				"\tbare = false",
			}, "\n"),
			expected: false,
		},
		{
			name: "url section with insteadOf credential detected",
			input: strings.Join([]string{
				`[url "https://user:token@github.com/"]`,
				"\tinsteadOf = https://github.com/",
			}, "\n"),
			expected: true,
		},
		{
			name: "url section without credentials is clean",
			input: strings.Join([]string{
				`[url "https://github.com/"]`,
				"\tinsteadOf = git://github.com/",
			}, "\n"),
			expected: false,
		},
		{
			name: "malformed URL with credentials detected via heuristic",
			input: strings.Join([]string{
				`[remote "origin"]`,
				"\turl = http{s://user:pass@github.com/org/repo.git",
			}, "\n"),
			expected: true,
		},
		{
			name:     "credentials detected with Windows line endings",
			input:    "[remote \"origin\"]\r\n\turl = https://user:token@github.com/org/repo.git\r\n",
			expected: true,
		},
		{
			name:     "empty input returns false",
			input:    "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := containsCredentials(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestContainsCredentials_FileHandling(t *testing.T) {
	t.Parallel()

	t.Run("nonexistent file returns false nil", func(t *testing.T) {
		t.Parallel()
		hasCreds, err := ContainsCredentials(filepath.Join(t.TempDir(), "nonexistent"))
		require.NoError(t, err)
		assert.False(t, hasCreds)
	})

	t.Run("file with credentials returns true", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config")
		content := strings.Join([]string{
			`[remote "origin"]`,
			"\turl = https://user:token@github.com/org/repo.git",
		}, "\n")
		require.NoError(t, os.WriteFile(configPath, []byte(content), 0o644))

		hasCreds, err := ContainsCredentials(configPath)
		require.NoError(t, err)
		assert.True(t, hasCreds)
	})

	t.Run("clean file returns false", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		configPath := filepath.Join(dir, "config")
		content := strings.Join([]string{
			"[core]",
			"\tbare = false",
			`[remote "origin"]`,
			"\turl = https://github.com/org/repo.git",
		}, "\n")
		require.NoError(t, os.WriteFile(configPath, []byte(content), 0o644))

		hasCreds, err := ContainsCredentials(configPath)
		require.NoError(t, err)
		assert.False(t, hasCreds)
	})
}

func TestResolveGitConfigPath(t *testing.T) {
	t.Parallel()

	t.Run("normal repo returns .git/config", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		gitDir := filepath.Join(dir, ".git")
		require.NoError(t, os.MkdirAll(gitDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(gitDir, "config"), []byte("[core]\n"), 0o644))

		path, err := resolveGitConfigPath(dir)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(gitDir, "config"), path)
	})

	t.Run("no git returns empty", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()

		path, err := resolveGitConfigPath(dir)
		require.NoError(t, err)
		assert.Empty(t, path)
	})

	t.Run("worktree resolves through commondir", func(t *testing.T) {
		t.Parallel()

		// Set up a main repo .git directory.
		mainRepo := t.TempDir()
		mainGitDir := filepath.Join(mainRepo, ".git")
		require.NoError(t, os.MkdirAll(filepath.Join(mainGitDir, "worktrees", "wt1"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "config"), []byte("[core]\n\tbare = false\n"), 0o644))

		// Set up commondir in the worktree gitdir.
		wtGitDir := filepath.Join(mainGitDir, "worktrees", "wt1")
		require.NoError(t, os.WriteFile(filepath.Join(wtGitDir, "commondir"), []byte("../..\n"), 0o644))

		// Set up worktree workspace with .git file.
		wtWorkspace := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(wtWorkspace, ".git"),
			[]byte("gitdir: "+wtGitDir+"\n"),
			0o644,
		))

		path, err := resolveGitConfigPath(wtWorkspace)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(mainGitDir, "config"), path)
	})

	t.Run("worktree with relative commondir", func(t *testing.T) {
		t.Parallel()

		mainRepo := t.TempDir()
		mainGitDir := filepath.Join(mainRepo, ".git")
		require.NoError(t, os.MkdirAll(filepath.Join(mainGitDir, "worktrees", "wt1"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "config"), []byte("[core]\n"), 0o644))

		wtGitDir := filepath.Join(mainGitDir, "worktrees", "wt1")
		// Relative commondir (standard git worktree layout).
		require.NoError(t, os.WriteFile(filepath.Join(wtGitDir, "commondir"), []byte("../.."), 0o644))

		wtWorkspace := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(wtWorkspace, ".git"),
			[]byte("gitdir: "+wtGitDir+"\n"),
			0o644,
		))

		path, err := resolveGitConfigPath(wtWorkspace)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(mainGitDir, "config"), path)
	})

	t.Run("malformed .git file returns error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".git"), []byte("garbage content\n"), 0o644))

		_, err := resolveGitConfigPath(dir)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "malformed .git file")
	})

	t.Run("worktree without commondir falls back to gitdir/config", func(t *testing.T) {
		t.Parallel()

		// Set up a gitdir without a commondir file (e.g. submodule).
		gitdir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(gitdir, "config"), []byte("[core]\n"), 0o644))

		wtWorkspace := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(wtWorkspace, ".git"),
			[]byte("gitdir: "+gitdir+"\n"),
			0o644,
		))

		path, err := resolveGitConfigPath(wtWorkspace)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(gitdir, "config"), path)
	})

	t.Run("broken gitdir path returns error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(
			filepath.Join(dir, ".git"),
			[]byte("gitdir: /nonexistent/path/that/does/not/exist\n"),
			0o644,
		))

		// commondir won't exist → falls back to gitdir/config, which also
		// won't exist. resolveGitConfigPath still returns a path (it doesn't
		// verify the config file exists — that's Process's job).
		path, err := resolveGitConfigPath(dir)
		require.NoError(t, err)
		assert.Equal(t, "/nonexistent/path/that/does/not/exist/config", path)
	})
}

func TestProcess_Worktree(t *testing.T) {
	t.Parallel()

	// Set up a main repo with .git directory.
	mainRepo := t.TempDir()
	mainGitDir := filepath.Join(mainRepo, ".git")
	require.NoError(t, os.MkdirAll(filepath.Join(mainGitDir, "worktrees", "wt1"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))

	configContent := strings.Join([]string{
		"[core]",
		"\tbare = false",
		"[credential]",
		"\thelper = store",
		`[remote "origin"]`,
		"\turl = https://user:token@github.com/org/repo.git",
	}, "\n")
	require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "config"), []byte(configContent), 0o644))

	// Set up worktree gitdir with commondir and HEAD.
	wtGitDir := filepath.Join(mainGitDir, "worktrees", "wt1")
	require.NoError(t, os.WriteFile(filepath.Join(wtGitDir, "commondir"), []byte("../.."), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(wtGitDir, "HEAD"), []byte("ref: refs/heads/feature-wt1\n"), 0o644))

	// Original workspace: .git is a file.
	originalDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(originalDir, ".git"),
		[]byte("gitdir: "+wtGitDir+"\n"),
		0o644,
	))

	// Snapshot: also has .git as a file (mimicking snapshot walker behavior).
	snapshotDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(snapshotDir, ".git"),
		[]byte("gitdir: "+wtGitDir+"\n"),
		0o644,
	))

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	sanitizer := NewConfigSanitizer(logger)

	err := sanitizer.Process(context.Background(), originalDir, snapshotDir)
	require.NoError(t, err)

	// The .git should now be a directory with a sanitized config.
	info, err := os.Stat(filepath.Join(snapshotDir, ".git"))
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	result, err := os.ReadFile(filepath.Join(snapshotDir, ".git", "config"))
	require.NoError(t, err)

	expected := strings.Join([]string{
		"[core]",
		"\tbare = false",
		`[remote "origin"]`,
		"\turl = https://github.com/org/repo.git",
	}, "\n")
	assert.Equal(t, expected, string(result))

	// Verify worktree git structure: HEAD, objects/, refs/.
	headData, err := os.ReadFile(filepath.Join(snapshotDir, ".git", "HEAD"))
	require.NoError(t, err)
	assert.Equal(t, "ref: refs/heads/feature-wt1\n", string(headData))

	objInfo, err := os.Stat(filepath.Join(snapshotDir, ".git", "objects"))
	require.NoError(t, err)
	assert.True(t, objInfo.IsDir())

	refsInfo, err := os.Stat(filepath.Join(snapshotDir, ".git", "refs"))
	require.NoError(t, err)
	assert.True(t, refsInfo.IsDir())
}

func TestProcess_WorktreeNoCommondir(t *testing.T) {
	t.Parallel()

	// Set up a gitdir without commondir (e.g. submodule).
	gitdir := t.TempDir()
	configContent := strings.Join([]string{
		"[core]",
		"\tbare = false",
		"[credential]",
		"\thelper = store",
	}, "\n")
	require.NoError(t, os.WriteFile(filepath.Join(gitdir, "config"), []byte(configContent), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(gitdir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))

	// Original workspace: .git is a file pointing to gitdir.
	originalDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(originalDir, ".git"),
		[]byte("gitdir: "+gitdir+"\n"),
		0o644,
	))

	snapshotDir := t.TempDir()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	sanitizer := NewConfigSanitizer(logger)

	err := sanitizer.Process(context.Background(), originalDir, snapshotDir)
	require.NoError(t, err)

	result, err := os.ReadFile(filepath.Join(snapshotDir, ".git", "config"))
	require.NoError(t, err)

	expected := strings.Join([]string{
		"[core]",
		"\tbare = false",
	}, "\n")
	assert.Equal(t, expected, string(result))

	// Verify worktree git structure: HEAD, objects/, refs/.
	headData, err := os.ReadFile(filepath.Join(snapshotDir, ".git", "HEAD"))
	require.NoError(t, err)
	assert.Equal(t, "ref: refs/heads/main\n", string(headData))

	_, err = os.Stat(filepath.Join(snapshotDir, ".git", "objects"))
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(snapshotDir, ".git", "refs"))
	require.NoError(t, err)
}

func TestProcess_Worktree_DetachedHEAD(t *testing.T) {
	t.Parallel()

	// Set up a gitdir with a detached HEAD (raw SHA).
	gitdir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(gitdir, "config"), []byte("[core]\n\tbare = false\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(gitdir, "HEAD"), []byte("abc123def456789\n"), 0o644))

	originalDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(originalDir, ".git"),
		[]byte("gitdir: "+gitdir+"\n"),
		0o644,
	))

	snapshotDir := t.TempDir()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	sanitizer := NewConfigSanitizer(logger)

	err := sanitizer.Process(context.Background(), originalDir, snapshotDir)
	require.NoError(t, err)

	// Detached HEAD should fall back to refs/heads/main.
	headData, err := os.ReadFile(filepath.Join(snapshotDir, ".git", "HEAD"))
	require.NoError(t, err)
	assert.Equal(t, "ref: refs/heads/main\n", string(headData))
}

func TestProcess_Worktree_CustomBranch(t *testing.T) {
	t.Parallel()

	mainRepo := t.TempDir()
	mainGitDir := filepath.Join(mainRepo, ".git")
	require.NoError(t, os.MkdirAll(filepath.Join(mainGitDir, "worktrees", "wt-feat"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "config"), []byte("[core]\n\tbare = false\n"), 0o644))

	// Worktree on feature-x branch.
	wtGitDir := filepath.Join(mainGitDir, "worktrees", "wt-feat")
	require.NoError(t, os.WriteFile(filepath.Join(wtGitDir, "commondir"), []byte("../.."), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(wtGitDir, "HEAD"), []byte("ref: refs/heads/feature-x\n"), 0o644))

	originalDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(originalDir, ".git"),
		[]byte("gitdir: "+wtGitDir+"\n"),
		0o644,
	))

	snapshotDir := t.TempDir()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	sanitizer := NewConfigSanitizer(logger)

	err := sanitizer.Process(context.Background(), originalDir, snapshotDir)
	require.NoError(t, err)

	headData, err := os.ReadFile(filepath.Join(snapshotDir, ".git", "HEAD"))
	require.NoError(t, err)
	assert.Equal(t, "ref: refs/heads/feature-x\n", string(headData))
}

func TestProcess_NormalRepo_NoDoubleCreate(t *testing.T) {
	t.Parallel()

	originalDir := t.TempDir()
	snapshotDir := t.TempDir()

	// Set up a normal repo with .git directory containing HEAD.
	gitDir := filepath.Join(originalDir, ".git")
	require.NoError(t, os.MkdirAll(gitDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "config"), []byte("[core]\n\tbare = false\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	sanitizer := NewConfigSanitizer(logger)

	err := sanitizer.Process(context.Background(), originalDir, snapshotDir)
	require.NoError(t, err)

	// For normal repos, Process should NOT create HEAD/objects/refs
	// (those come from the snapshot walker, not the sanitizer).
	_, err = os.Stat(filepath.Join(snapshotDir, ".git", "HEAD"))
	assert.True(t, os.IsNotExist(err), "HEAD should not be created for normal repos")

	_, err = os.Stat(filepath.Join(snapshotDir, ".git", "objects"))
	assert.True(t, os.IsNotExist(err), "objects/ should not be created for normal repos")

	_, err = os.Stat(filepath.Join(snapshotDir, ".git", "refs"))
	assert.True(t, os.IsNotExist(err), "refs/ should not be created for normal repos")
}

func TestProcess_Worktree_MaliciousGitdir(t *testing.T) {
	t.Parallel()

	// A .git file pointing to an arbitrary path (path traversal attempt).
	// The resolved gitdir does not contain HEAD, so initWorktreeGitDir
	// should fail gracefully without leaking file contents.
	originalDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(originalDir, ".git"),
		[]byte("gitdir: ../../../etc\n"),
		0o644,
	))

	// We still need a valid config for resolveGitConfigPath to find.
	// Create a fake gitdir that has a config but no HEAD.
	fakeGitdir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(fakeGitdir, "config"),
		[]byte("[core]\n\tbare = false\n"),
		0o644,
	))

	// Point .git at the fake gitdir (which has config but no HEAD).
	require.NoError(t, os.WriteFile(
		filepath.Join(originalDir, ".git"),
		[]byte("gitdir: "+fakeGitdir+"\n"),
		0o644,
	))

	snapshotDir := t.TempDir()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	sanitizer := NewConfigSanitizer(logger)

	// Process should succeed (worktree init failure is non-fatal).
	err := sanitizer.Process(context.Background(), originalDir, snapshotDir)
	require.NoError(t, err)

	// Config should be written (sanitizer resolves config independently).
	_, err = os.ReadFile(filepath.Join(snapshotDir, ".git", "config"))
	require.NoError(t, err)

	// HEAD should NOT be created since gitdir validation failed.
	_, err = os.Stat(filepath.Join(snapshotDir, ".git", "HEAD"))
	assert.True(t, os.IsNotExist(err), "HEAD should not be created for malicious gitdir")
}
