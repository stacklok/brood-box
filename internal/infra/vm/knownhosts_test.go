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
)

func TestInjectSSHKnownHosts_WritesFile(t *testing.T) {
	t.Parallel()

	rootfs := t.TempDir()
	hook := InjectSSHKnownHosts(nopChown)
	require.NoError(t, hook(rootfs, nil))

	path := filepath.Join(rootfs, sandboxHome, ".ssh", "known_hosts")
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotEmpty(t, data)
}

func TestInjectSSHKnownHosts_FilePermissions(t *testing.T) {
	t.Parallel()

	rootfs := t.TempDir()
	hook := InjectSSHKnownHosts(nopChown)
	require.NoError(t, hook(rootfs, nil))

	path := filepath.Join(rootfs, sandboxHome, ".ssh", "known_hosts")
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestInjectSSHKnownHosts_SSHDirPermissions(t *testing.T) {
	t.Parallel()

	rootfs := t.TempDir()
	hook := InjectSSHKnownHosts(nopChown)
	require.NoError(t, hook(rootfs, nil))

	sshDir := filepath.Join(rootfs, sandboxHome, ".ssh")
	info, err := os.Stat(sshDir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func TestInjectSSHKnownHosts_ContainsExpectedHosts(t *testing.T) {
	t.Parallel()

	rootfs := t.TempDir()
	hook := InjectSSHKnownHosts(nopChown)
	require.NoError(t, hook(rootfs, nil))

	path := filepath.Join(rootfs, sandboxHome, ".ssh", "known_hosts")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	content := string(data)
	for _, host := range []string{"github.com", "gitlab.com", "bitbucket.org"} {
		assert.Contains(t, content, host, "known_hosts should contain %s", host)
	}
}

func TestInjectSSHKnownHosts_ContainsExpectedKeyTypes(t *testing.T) {
	t.Parallel()

	rootfs := t.TempDir()
	hook := InjectSSHKnownHosts(nopChown)
	require.NoError(t, hook(rootfs, nil))

	path := filepath.Join(rootfs, sandboxHome, ".ssh", "known_hosts")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	content := string(data)
	for _, keyType := range []string{"ssh-ed25519", "ecdsa-sha2-nistp256", "ssh-rsa"} {
		assert.True(t,
			strings.Contains(content, "github.com "+keyType),
			"known_hosts should contain github.com %s key", keyType,
		)
	}
}

func TestInjectSSHKnownHosts_ChownCalled(t *testing.T) {
	t.Parallel()

	rootfs := t.TempDir()
	var calls []chownCall
	recorder := func(path string, uid, gid int) error {
		calls = append(calls, chownCall{Path: path, UID: uid, GID: gid})
		return nil
	}

	hook := InjectSSHKnownHosts(recorder)
	require.NoError(t, hook(rootfs, nil))

	// Should chown both .ssh dir and known_hosts file.
	require.Len(t, calls, 2)
	assert.Equal(t, sandboxUID, calls[0].UID)
	assert.Equal(t, sandboxGID, calls[0].GID)
	assert.True(t, strings.HasSuffix(calls[0].Path, ".ssh"))
	assert.Equal(t, sandboxUID, calls[1].UID)
	assert.Equal(t, sandboxGID, calls[1].GID)
	assert.True(t, strings.HasSuffix(calls[1].Path, "known_hosts"))
}

// nopChown is a no-op ChownFunc for tests that don't need ownership tracking.
func nopChown(_ string, _, _ int) error { return nil }
