// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domaingit "github.com/stacklok/apiary/pkg/domain/git"
)

func TestInjectGitConfig_FullIdentityAndToken(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	identity := domaingit.Identity{Name: "Alice", Email: "alice@example.com"}

	hook := InjectGitConfig(identity, true)
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
	assert.Contains(t, content, "helper = /usr/local/bin/git-credential-apiary")

	// Verify credential helper exists.
	helperPath := filepath.Join(rootfs, "usr", "local", "bin", "git-credential-apiary")
	_, err = os.Stat(helperPath)
	assert.NoError(t, err)
}

func TestInjectGitConfig_IdentityOnly(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	identity := domaingit.Identity{Name: "Bob", Email: "bob@example.com"}

	hook := InjectGitConfig(identity, false)
	err := hook(rootfs, nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".gitconfig"))
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "[user]")
	assert.Contains(t, content, "name = Bob")
	assert.Contains(t, content, "email = bob@example.com")
	assert.NotContains(t, content, "[credential]")

	// Verify no credential helper.
	helperPath := filepath.Join(rootfs, "usr", "local", "bin", "git-credential-apiary")
	_, err = os.Stat(helperPath)
	assert.True(t, os.IsNotExist(err), "credential helper should not exist")
}

func TestInjectGitConfig_TokenOnly(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	identity := domaingit.Identity{} // empty — not complete

	hook := InjectGitConfig(identity, true)
	err := hook(rootfs, nil)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, ".gitconfig"))
	require.NoError(t, err)

	content := string(data)
	assert.NotContains(t, content, "[user]")
	assert.Contains(t, content, "[credential]")
	assert.Contains(t, content, "helper = /usr/local/bin/git-credential-apiary")

	// Verify credential helper exists.
	helperPath := filepath.Join(rootfs, "usr", "local", "bin", "git-credential-apiary")
	_, err = os.Stat(helperPath)
	assert.NoError(t, err)
}

func TestInjectGitConfig_NoOp(t *testing.T) {
	t.Parallel()

	rootfs := setupRootfs(t)
	identity := domaingit.Identity{} // empty

	hook := InjectGitConfig(identity, false)
	err := hook(rootfs, nil)
	require.NoError(t, err)

	// No .gitconfig should be written.
	_, err = os.Stat(filepath.Join(rootfs, sandboxHome, ".gitconfig"))
	assert.True(t, os.IsNotExist(err), ".gitconfig should not exist")

	// No credential helper should be written.
	_, err = os.Stat(filepath.Join(rootfs, "usr", "local", "bin", "git-credential-apiary"))
	assert.True(t, os.IsNotExist(err), "credential helper should not exist")
}

func TestCredentialHelper_Executable(t *testing.T) {
	t.Parallel()

	rootfs := t.TempDir()

	err := writeCredentialHelper(rootfs)
	require.NoError(t, err)

	helperPath := filepath.Join(rootfs, "usr", "local", "bin", "git-credential-apiary")
	info, err := os.Stat(helperPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm(),
		"credential helper must be executable (0755)")
}

func TestCredentialHelper_Content(t *testing.T) {
	t.Parallel()

	rootfs := t.TempDir()

	err := writeCredentialHelper(rootfs)
	require.NoError(t, err)

	helperPath := filepath.Join(rootfs, "usr", "local", "bin", "git-credential-apiary")
	data, err := os.ReadFile(helperPath)
	require.NoError(t, err)

	content := string(data)
	assert.Contains(t, content, "#!/bin/sh", "must have shebang")
	assert.Contains(t, content, "github.com", "must scope to github.com")
	assert.Contains(t, content, "GITHUB_TOKEN", "must reference GITHUB_TOKEN")
	assert.Contains(t, content, "GH_TOKEN", "must reference GH_TOKEN")
	assert.Contains(t, content, "x-access-token", "must use x-access-token username")
}
