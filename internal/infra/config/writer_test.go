// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteDefault(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setup    func(t *testing.T, dir string) string
		force    bool
		wantErr  string
		assertFn func(t *testing.T, path string)
	}{
		{
			name: "creates file when it does not exist",
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				return filepath.Join(dir, "config.yaml")
			},
			wantErr: "",
			assertFn: func(t *testing.T, path string) {
				t.Helper()
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Contains(t, string(data), "# Brood Box")

				info, err := os.Stat(path)
				require.NoError(t, err)
				assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
			},
		},
		{
			name: "creates parent directories",
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				return filepath.Join(dir, "a", "b", "c", "config.yaml")
			},
			wantErr: "",
			assertFn: func(t *testing.T, path string) {
				t.Helper()
				_, err := os.Stat(path)
				require.NoError(t, err)

				parentDir := filepath.Dir(path)
				info, err := os.Stat(parentDir)
				require.NoError(t, err)
				assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
			},
		},
		{
			name: "errors when file exists and force is false",
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				path := filepath.Join(dir, "config.yaml")
				require.NoError(t, os.WriteFile(path, []byte("existing"), 0o644))
				return path
			},
			force:   false,
			wantErr: "already exists",
		},
		{
			name: "overwrites when file exists and force is true",
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				path := filepath.Join(dir, "config.yaml")
				require.NoError(t, os.WriteFile(path, []byte("old content"), 0o644))
				return path
			},
			force:   true,
			wantErr: "",
			assertFn: func(t *testing.T, path string) {
				t.Helper()
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Contains(t, string(data), "# Brood Box")
				assert.NotContains(t, string(data), "old content")

				// Verify permissions are reset to 0600 even though
				// the original file was 0644.
				info, err := os.Stat(path)
				require.NoError(t, err)
				assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
			},
		},
		{
			name: "template is valid YAML that parses to zero-value Config",
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				return filepath.Join(dir, "config.yaml")
			},
			wantErr: "",
			assertFn: func(t *testing.T, path string) {
				t.Helper()
				loader := NewLoader(path)
				cfg, err := loader.Load()
				require.NoError(t, err)
				assert.Zero(t, cfg.Defaults.CPUs)
				assert.Zero(t, cfg.Defaults.Memory)
				assert.Nil(t, cfg.Agents)
				assert.Nil(t, cfg.Review.Enabled)
			},
		},
		{
			name: "template contains all config sections",
			setup: func(t *testing.T, dir string) string {
				t.Helper()
				return filepath.Join(dir, "config.yaml")
			},
			wantErr: "",
			assertFn: func(t *testing.T, path string) {
				t.Helper()
				data, err := os.ReadFile(path)
				require.NoError(t, err)
				content := string(data)
				for _, section := range []string{
					"defaults:", "review:", "network:",
					"mcp:", "authz:", "config:",
					"git:", "auth:", "runtime:", "agents:",
				} {
					assert.Contains(t, content, section,
						"template should contain section %q", section)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path := tt.setup(t, dir)

			err := WriteDefault(path, tt.force)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			if tt.assertFn != nil {
				tt.assertFn(t, path)
			}
		})
	}
}
