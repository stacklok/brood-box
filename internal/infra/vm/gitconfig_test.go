// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domaingit "github.com/stacklok/brood-box/pkg/domain/git"
)

func TestInjectGitConfig_FullIdentityAndToken(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, getCalls := recordingChown()
	identity := domaingit.Identity{Name: "Alice", Email: "alice@example.com"}

	hook := InjectGitConfig(identity, true, chown)
	err := hook(rootfs, nil)
	require.NoError(t, err)

	// Verify .gitconfig exists and has both sections.
	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".gitconfig"))
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "[user]")
	assert.Contains(t, content, "name = Alice")
	assert.Contains(t, content, "email = alice@example.com")
	assert.Contains(t, content, "[credential]")
	assert.Contains(t, content, "helper = /usr/local/bin/git-credential-bbox")
	assert.Contains(t, content, "[safe]")
	assert.Contains(t, content, "directory = /workspace")

	// Verify credential helper exists.
	helperPath := filepath.Join(rootfs, "usr", "local", "bin", "git-credential-bbox")
	_, err = os.Stat(helperPath)
	assert.NoError(t, err)

	// Verify chown was called with sandbox UID/GID.
	calls := getCalls()
	require.NotEmpty(t, calls, "chown must be called")
	for _, c := range calls {
		assert.Equal(t, sandboxUID, c.UID)
		assert.Equal(t, sandboxGID, c.GID)
	}
}

func TestInjectGitConfig_IdentityOnly(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	identity := domaingit.Identity{Name: "Bob", Email: "bob@example.com"}

	hook := InjectGitConfig(identity, false, chown)
	err := hook(rootfs, nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".gitconfig"))
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "[user]")
	assert.Contains(t, content, "name = Bob")
	assert.Contains(t, content, "email = bob@example.com")
	assert.NotContains(t, content, "[credential]")
	assert.Contains(t, content, "[safe]")
	assert.Contains(t, content, "directory = /workspace")

	// Verify no credential helper.
	helperPath := filepath.Join(rootfs, "usr", "local", "bin", "git-credential-bbox")
	_, err = os.Stat(helperPath)
	assert.True(t, os.IsNotExist(err), "credential helper should not exist")
}

func TestInjectGitConfig_TokenOnly(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	identity := domaingit.Identity{} // empty — not complete

	hook := InjectGitConfig(identity, true, chown)
	err := hook(rootfs, nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".gitconfig"))
	require.NoError(t, err)

	content := string(data)
	assert.NotContains(t, content, "[user]")
	assert.Contains(t, content, "[credential]")
	assert.Contains(t, content, "helper = /usr/local/bin/git-credential-bbox")
	assert.Contains(t, content, "[safe]")
	assert.Contains(t, content, "directory = /workspace")

	// Verify credential helper exists.
	helperPath := filepath.Join(rootfs, "usr", "local", "bin", "git-credential-bbox")
	_, err = os.Stat(helperPath)
	assert.NoError(t, err)
}

func TestInjectGitConfig_SafeDirectoryOnly(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, getCalls := recordingChown()
	identity := domaingit.Identity{} // empty — not complete

	hook := InjectGitConfig(identity, false, chown)
	err := hook(rootfs, nil)
	require.NoError(t, err)

	// .gitconfig should be created with only [safe] section.
	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".gitconfig"))
	require.NoError(t, err)

	content := string(data)
	assert.NotContains(t, content, "[user]")
	assert.NotContains(t, content, "[credential]")
	assert.Contains(t, content, "[safe]")
	assert.Contains(t, content, "directory = /workspace")

	// No credential helper should be written.
	_, err = os.Stat(filepath.Join(rootfs, "usr", "local", "bin", "git-credential-bbox"))
	assert.True(t, os.IsNotExist(err), "credential helper should not exist")

	// Chown should still be called for the .gitconfig file.
	calls := getCalls()
	require.NotEmpty(t, calls, "chown must be called")
	for _, c := range calls {
		assert.Equal(t, sandboxUID, c.UID)
		assert.Equal(t, sandboxGID, c.GID)
	}
}

func TestCredentialHelper_Executable(t *testing.T) {
	t.Parallel()

	rootfs := t.TempDir()

	err := writeCredentialHelper(rootfs)
	require.NoError(t, err)

	helperPath := filepath.Join(rootfs, "usr", "local", "bin", "git-credential-bbox")
	info, err := os.Stat(helperPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm(),
		"credential helper must be executable (0755)")
}

func TestSanitizeGitValue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "normal name", input: "Alice Smith", expected: "Alice Smith"},
		{name: "normal email", input: "alice@example.com", expected: "alice@example.com"},
		{name: "UTF-8 latin", input: "José García", expected: "José García"},
		{name: "CJK name", input: "田中太郎", expected: "田中太郎"},
		{
			name:     "newline and section injection",
			input:    "Alice\n[credential]\nhelper=evil",
			expected: "Alicecredentialhelper=evil",
		},
		{name: "backslash stripped", input: `value\continuation`, expected: "valuecontinuation"},
		{name: "hash comment stripped", input: "name # comment", expected: "name  comment"},
		{name: "semicolon comment stripped", input: "name ; comment", expected: "name  comment"},
		{name: "brackets stripped", input: "a]b[c", expected: "abc"},
		{name: "double quote stripped", input: `Alice "Bob"`, expected: "Alice Bob"},
		{name: "exceeds max length", input: strings.Repeat("a", maxGitValueLength+1), expected: ""},
		{name: "empty input", input: "", expected: ""},
		{name: "all control chars", input: "\t\n\r", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, sanitizeGitValue(tt.input))
		})
	}
}

func TestCredentialHelper_Content(t *testing.T) {
	t.Parallel()

	rootfs := t.TempDir()

	err := writeCredentialHelper(rootfs)
	require.NoError(t, err)

	helperPath := filepath.Join(rootfs, "usr", "local", "bin", "git-credential-bbox")
	data, err := os.ReadFile(helperPath)
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "#!/bin/sh", "must have shebang")
	assert.Contains(t, content, "github.com", "must scope to github.com")
	assert.Contains(t, content, "GITHUB_TOKEN", "must reference GITHUB_TOKEN")
	assert.Contains(t, content, "GH_TOKEN", "must reference GH_TOKEN")
	assert.Contains(t, content, "x-access-token", "must use x-access-token username")
}

func TestGitConfigFilePermissions(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	chown, _ := recordingChown()
	identity := domaingit.Identity{Name: "Alice", Email: "alice@example.com"}

	hook := InjectGitConfig(identity, true, chown)
	err := hook(rootfs, nil)
	require.NoError(t, err)

	info, err := os.Stat(filepath.Join(rootfs, sandboxHome, ".gitconfig"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"gitconfig should be owner-only (0600)")
}
