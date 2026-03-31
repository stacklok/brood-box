// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/brood-box/pkg/domain/settings"
)

// mockInjector records calls to Inject for verification.
type mockInjector struct {
	calls []mockInjectCall
}

type mockInjectCall struct {
	RootfsPath  string
	HostHomeDir string
	Manifest    settings.Manifest
}

func (m *mockInjector) Inject(rootfsPath, hostHomeDir string, manifest settings.Manifest) (settings.InjectionResult, error) {
	m.calls = append(m.calls, mockInjectCall{
		RootfsPath:  rootfsPath,
		HostHomeDir: hostHomeDir,
		Manifest:    manifest,
	})
	return settings.InjectionResult{FileCount: len(manifest.Entries)}, nil
}

func (m *mockInjector) Extract(_, _ string, _ settings.Manifest) (settings.InjectionResult, error) {
	return settings.InjectionResult{}, nil
}

func TestInjectSettings_NilManifest(t *testing.T) {
	t.Parallel()

	inj := &mockInjector{}
	hook := InjectSettings(inj, nil)

	err := hook(t.TempDir(), nil)
	require.NoError(t, err)
	assert.Empty(t, inj.calls, "injector should not be called for nil manifest")
}

func TestInjectSettings_EmptyManifestEntries(t *testing.T) {
	t.Parallel()

	inj := &mockInjector{}
	manifest := &settings.Manifest{Entries: []settings.Entry{}}
	hook := InjectSettings(inj, manifest)

	err := hook(t.TempDir(), nil)
	require.NoError(t, err)
	assert.Empty(t, inj.calls, "injector should not be called for empty manifest entries")
}

func TestInjectSettings_NonNilManifestCallsInjector(t *testing.T) {
	t.Parallel()

	inj := &mockInjector{}
	manifest := &settings.Manifest{
		Entries: []settings.Entry{
			{
				Category:  "settings",
				HostPath:  "settings.json",
				GuestPath: "settings.json",
				Kind:      settings.KindFile,
			},
			{
				Category:  "skills",
				HostPath:  ".skills",
				GuestPath: ".skills",
				Kind:      settings.KindDirectory,
				Optional:  true,
			},
		},
	}

	rootfs := t.TempDir()
	hook := InjectSettings(inj, manifest)

	err := hook(rootfs, nil)
	require.NoError(t, err)

	require.Len(t, inj.calls, 1, "injector should be called exactly once")

	call := inj.calls[0]
	assert.Equal(t, rootfs, call.RootfsPath)
	assert.NotEmpty(t, call.HostHomeDir, "host home dir should be resolved")
	assert.Equal(t, *manifest, call.Manifest)
	assert.Len(t, call.Manifest.Entries, 2)
}
