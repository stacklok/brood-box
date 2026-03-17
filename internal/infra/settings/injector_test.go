// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	toml "github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/brood-box/pkg/domain/settings"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// setupRootfs creates the sandbox home directory inside rootfsPath.
func setupRootfs(t *testing.T, rootfsPath string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(rootfsPath, sandboxHome), 0755))
}

func TestFSInjector_File(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setup   func(t *testing.T, hostHome string)
		entry   settings.Entry
		check   func(t *testing.T, rootfs string)
		wantErr string
	}{
		{
			name: "copies file with correct permissions",
			setup: func(t *testing.T, hostHome string) {
				t.Helper()
				require.NoError(t, os.WriteFile(
					filepath.Join(hostHome, "settings.json"),
					[]byte(`{"key":"value"}`), 0644))
			},
			entry: settings.Entry{
				HostPath:  "settings.json",
				GuestPath: "settings.json",
				Kind:      settings.KindFile,
			},
			check: func(t *testing.T, rootfs string) {
				t.Helper()
				dst := filepath.Join(rootfs, sandboxHome, "settings.json")
				data, err := os.ReadFile(dst)
				require.NoError(t, err)
				assert.Equal(t, `{"key":"value"}`, string(data))

				info, err := os.Stat(dst)
				require.NoError(t, err)
				assert.Equal(t, os.FileMode(filePerm), info.Mode().Perm())
			},
		},
		{
			name:  "optional missing source is no-op",
			setup: func(_ *testing.T, _ string) {},
			entry: settings.Entry{
				HostPath:  "nonexistent.json",
				GuestPath: "nonexistent.json",
				Kind:      settings.KindFile,
				Optional:  true,
			},
			check: func(t *testing.T, rootfs string) {
				t.Helper()
				_, err := os.Stat(filepath.Join(rootfs, sandboxHome, "nonexistent.json"))
				assert.True(t, os.IsNotExist(err))
			},
		},
		{
			name: "rejects symlinks",
			setup: func(t *testing.T, hostHome string) {
				t.Helper()
				require.NoError(t, os.WriteFile(
					filepath.Join(hostHome, "real.json"),
					[]byte("real"), 0644))
				require.NoError(t, os.Symlink("real.json",
					filepath.Join(hostHome, "link.json")))
			},
			entry: settings.Entry{
				HostPath:  "link.json",
				GuestPath: "link.json",
				Kind:      settings.KindFile,
			},
			check: func(t *testing.T, rootfs string) {
				t.Helper()
				_, err := os.Stat(filepath.Join(rootfs, sandboxHome, "link.json"))
				assert.True(t, os.IsNotExist(err))
			},
		},
		{
			name: "rejects path traversal",
			setup: func(t *testing.T, hostHome string) {
				t.Helper()
				require.NoError(t, os.WriteFile(
					filepath.Join(hostHome, "data.json"),
					[]byte("data"), 0644))
			},
			entry: settings.Entry{
				HostPath:  "data.json",
				GuestPath: "../../etc/passwd",
				Kind:      settings.KindFile,
			},
			wantErr: "path containment",
		},
		{
			name: "rejects oversized file",
			setup: func(t *testing.T, hostHome string) {
				t.Helper()
				big := make([]byte, settings.MaxFileSize+1)
				require.NoError(t, os.WriteFile(
					filepath.Join(hostHome, "big.bin"),
					big, 0644))
			},
			entry: settings.Entry{
				HostPath:  "big.bin",
				GuestPath: "big.bin",
				Kind:      settings.KindFile,
			},
			wantErr: "exceeds max size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			hostHome := t.TempDir()
			rootfs := t.TempDir()
			setupRootfs(t, rootfs)

			tt.setup(t, hostHome)

			inj := NewFSInjector(testLogger())
			manifest := settings.Manifest{Entries: []settings.Entry{tt.entry}}
			err := inj.Inject(rootfs, hostHome, manifest)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			if tt.check != nil {
				tt.check(t, rootfs)
			}
		})
	}
}

func TestFSInjector_Directory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setup   func(t *testing.T, hostHome string)
		entry   settings.Entry
		check   func(t *testing.T, rootfs string)
		wantErr string
	}{
		{
			name: "recursively copies directory",
			setup: func(t *testing.T, hostHome string) {
				t.Helper()
				dir := filepath.Join(hostHome, "config", "sub")
				require.NoError(t, os.MkdirAll(dir, 0755))
				require.NoError(t, os.WriteFile(
					filepath.Join(hostHome, "config", "a.txt"),
					[]byte("aaa"), 0644))
				require.NoError(t, os.WriteFile(
					filepath.Join(dir, "b.txt"),
					[]byte("bbb"), 0644))
			},
			entry: settings.Entry{
				HostPath:  "config",
				GuestPath: "config",
				Kind:      settings.KindDirectory,
			},
			check: func(t *testing.T, rootfs string) {
				t.Helper()
				dataA, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, "config", "a.txt"))
				require.NoError(t, err)
				assert.Equal(t, "aaa", string(dataA))

				dataB, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, "config", "sub", "b.txt"))
				require.NoError(t, err)
				assert.Equal(t, "bbb", string(dataB))
			},
		},
		{
			name: "respects depth limit",
			setup: func(t *testing.T, hostHome string) {
				t.Helper()
				// Build a chain deeper than MaxDepth.
				path := filepath.Join(hostHome, "deep")
				for i := 0; i < settings.MaxDepth+2; i++ {
					path = filepath.Join(path, "d")
				}
				require.NoError(t, os.MkdirAll(path, 0755))
				require.NoError(t, os.WriteFile(
					filepath.Join(path, "file.txt"),
					[]byte("too deep"), 0644))
			},
			entry: settings.Entry{
				HostPath:  "deep",
				GuestPath: "deep",
				Kind:      settings.KindDirectory,
			},
			check: func(t *testing.T, rootfs string) {
				t.Helper()
				// The deeply nested file should not exist.
				path := filepath.Join(rootfs, sandboxHome, "deep")
				for i := 0; i < settings.MaxDepth+2; i++ {
					path = filepath.Join(path, "d")
				}
				_, err := os.Stat(filepath.Join(path, "file.txt"))
				assert.True(t, os.IsNotExist(err))
			},
		},
		{
			name: "skips symlinks in directory",
			setup: func(t *testing.T, hostHome string) {
				t.Helper()
				dir := filepath.Join(hostHome, "withlink")
				require.NoError(t, os.MkdirAll(dir, 0755))
				require.NoError(t, os.WriteFile(
					filepath.Join(dir, "real.txt"),
					[]byte("real"), 0644))
				require.NoError(t, os.Symlink("real.txt",
					filepath.Join(dir, "link.txt")))
			},
			entry: settings.Entry{
				HostPath:  "withlink",
				GuestPath: "withlink",
				Kind:      settings.KindDirectory,
			},
			check: func(t *testing.T, rootfs string) {
				t.Helper()
				// Real file should exist.
				data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, "withlink", "real.txt"))
				require.NoError(t, err)
				assert.Equal(t, "real", string(data))

				// Symlink should not.
				_, err = os.Stat(filepath.Join(rootfs, sandboxHome, "withlink", "link.txt"))
				assert.True(t, os.IsNotExist(err))
			},
		},
		{
			name: "respects file count limit",
			setup: func(t *testing.T, hostHome string) {
				t.Helper()
				dir := filepath.Join(hostHome, "many")
				require.NoError(t, os.MkdirAll(dir, 0755))
				// Create MaxFileCount + 5 files.
				for i := 0; i < settings.MaxFileCount+5; i++ {
					name := filepath.Join(dir, fmt.Sprintf("f%04d.txt", i))
					require.NoError(t, os.WriteFile(name, []byte("x"), 0644))
				}
			},
			entry: settings.Entry{
				HostPath:  "many",
				GuestPath: "many",
				Kind:      settings.KindDirectory,
			},
			wantErr: "file count would exceed limit",
		},
		{
			name: "respects total size limit",
			setup: func(t *testing.T, hostHome string) {
				t.Helper()
				dir := filepath.Join(hostHome, "bigdir")
				require.NoError(t, os.MkdirAll(dir, 0755))
				// Write files that together exceed MaxTotalSize.
				// Each file is MaxFileSize (1 MiB), write enough to exceed 50 MiB.
				chunk := make([]byte, settings.MaxFileSize)
				count := int(settings.MaxTotalSize/settings.MaxFileSize) + 2
				for i := 0; i < count; i++ {
					name := filepath.Join(dir, fmt.Sprintf("chunk%d.bin", i))
					require.NoError(t, os.WriteFile(name, chunk, 0644))
				}
			},
			entry: settings.Entry{
				HostPath:  "bigdir",
				GuestPath: "bigdir",
				Kind:      settings.KindDirectory,
			},
			wantErr: "aggregate size would exceed limit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			hostHome := t.TempDir()
			rootfs := t.TempDir()
			setupRootfs(t, rootfs)

			tt.setup(t, hostHome)

			inj := NewFSInjector(testLogger())
			manifest := settings.Manifest{Entries: []settings.Entry{tt.entry}}
			err := inj.Inject(rootfs, hostHome, manifest)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			if tt.check != nil {
				tt.check(t, rootfs)
			}
		})
	}
}

func TestFSInjector_MergeFileJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		hostContent  string
		guestContent string // empty means no pre-existing guest file
		entry        settings.Entry
		wantKeys     map[string]any
		wantAbsent   []string
	}{
		{
			name:        "allowlist filtering",
			hostContent: `{"theme":"dark","apiKey":"secret","editor":"vim"}`,
			entry: settings.Entry{
				HostPath:  "config.json",
				GuestPath: "config.json",
				Kind:      settings.KindMergeFile,
				Format:    "json",
				Filter: &settings.FieldFilter{
					AllowKeys: []string{"theme", "editor"},
				},
			},
			wantKeys:   map[string]any{"theme": "dark", "editor": "vim"},
			wantAbsent: []string{"apiKey"},
		},
		{
			name:        "deny sub-keys with wildcard",
			hostContent: `{"providers":{"github":{"url":"https://github.com","apiKey":"ghp_secret"},"gitlab":{"url":"https://gitlab.com","apiKey":"glpat_secret"}},"theme":"dark"}`,
			entry: settings.Entry{
				HostPath:  "config.json",
				GuestPath: "config.json",
				Kind:      settings.KindMergeFile,
				Format:    "json",
				Filter: &settings.FieldFilter{
					AllowKeys:   []string{"providers", "theme"},
					DenySubKeys: map[string][]string{"providers": {"*.apiKey"}},
				},
			},
			wantKeys:   map[string]any{"theme": "dark"},
			wantAbsent: []string{
				// apiKey should be stripped from both providers.
			},
		},
		{
			name:         "preserves existing guest keys",
			hostContent:  `{"theme":"dark","editor":"vim"}`,
			guestContent: `{"credentials":"token123","theme":"light"}`,
			entry: settings.Entry{
				HostPath:  "config.json",
				GuestPath: "config.json",
				Kind:      settings.KindMergeFile,
				Format:    "json",
			},
			wantKeys: map[string]any{"credentials": "token123", "theme": "dark", "editor": "vim"},
		},
		{
			name:        "merge into empty rootfs",
			hostContent: `{"setting":"value"}`,
			entry: settings.Entry{
				HostPath:  "config.json",
				GuestPath: "new-config.json",
				Kind:      settings.KindMergeFile,
				Format:    "json",
			},
			wantKeys: map[string]any{"setting": "value"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			hostHome := t.TempDir()
			rootfs := t.TempDir()
			setupRootfs(t, rootfs)

			require.NoError(t, os.WriteFile(
				filepath.Join(hostHome, tt.entry.HostPath),
				[]byte(tt.hostContent), 0644))

			if tt.guestContent != "" {
				guestPath := filepath.Join(rootfs, sandboxHome, tt.entry.GuestPath)
				require.NoError(t, os.MkdirAll(filepath.Dir(guestPath), 0755))
				require.NoError(t, os.WriteFile(guestPath, []byte(tt.guestContent), 0600))
			}

			inj := NewFSInjector(testLogger())
			manifest := settings.Manifest{Entries: []settings.Entry{tt.entry}}
			err := inj.Inject(rootfs, hostHome, manifest)
			require.NoError(t, err)

			dstPath := filepath.Join(rootfs, sandboxHome, tt.entry.GuestPath)
			data, err := os.ReadFile(dstPath)
			require.NoError(t, err)

			var result map[string]any
			require.NoError(t, json.Unmarshal(data, &result))

			for k, v := range tt.wantKeys {
				assert.Equal(t, v, result[k], "key %q", k)
			}

			for _, k := range tt.wantAbsent {
				_, exists := result[k]
				assert.False(t, exists, "key %q should be absent", k)
			}

			// Special check for deny sub-keys test: verify apiKey is stripped.
			if tt.name == "deny sub-keys with wildcard" {
				providers, ok := result["providers"].(map[string]any)
				require.True(t, ok)
				for name, prov := range providers {
					provMap, ok := prov.(map[string]any)
					require.True(t, ok, "provider %q", name)
					_, hasAPIKey := provMap["apiKey"]
					assert.False(t, hasAPIKey, "provider %q should not have apiKey", name)
					_, hasURL := provMap["url"]
					assert.True(t, hasURL, "provider %q should still have url", name)
				}
			}
		})
	}
}

func TestFSInjector_MergeFileTOML(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		hostContent  string
		guestContent string
		entry        settings.Entry
		wantKeys     map[string]any
		wantAbsent   []string
	}{
		{
			name:        "allowlist filtering TOML",
			hostContent: "theme = \"dark\"\napiKey = \"secret\"\neditor = \"vim\"\n",
			entry: settings.Entry{
				HostPath:  "config.toml",
				GuestPath: "config.toml",
				Kind:      settings.KindMergeFile,
				Format:    "toml",
				Filter: &settings.FieldFilter{
					AllowKeys: []string{"theme", "editor"},
				},
			},
			wantKeys:   map[string]any{"theme": "dark", "editor": "vim"},
			wantAbsent: []string{"apiKey"},
		},
		{
			name:         "preserves existing guest keys TOML",
			hostContent:  "theme = \"dark\"\n",
			guestContent: "credentials = \"token123\"\ntheme = \"light\"\n",
			entry: settings.Entry{
				HostPath:  "config.toml",
				GuestPath: "config.toml",
				Kind:      settings.KindMergeFile,
				Format:    "toml",
			},
			wantKeys: map[string]any{"credentials": "token123", "theme": "dark"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			hostHome := t.TempDir()
			rootfs := t.TempDir()
			setupRootfs(t, rootfs)

			require.NoError(t, os.WriteFile(
				filepath.Join(hostHome, tt.entry.HostPath),
				[]byte(tt.hostContent), 0644))

			if tt.guestContent != "" {
				guestPath := filepath.Join(rootfs, sandboxHome, tt.entry.GuestPath)
				require.NoError(t, os.MkdirAll(filepath.Dir(guestPath), 0755))
				require.NoError(t, os.WriteFile(guestPath, []byte(tt.guestContent), 0600))
			}

			inj := NewFSInjector(testLogger())
			manifest := settings.Manifest{Entries: []settings.Entry{tt.entry}}
			err := inj.Inject(rootfs, hostHome, manifest)
			require.NoError(t, err)

			dstPath := filepath.Join(rootfs, sandboxHome, tt.entry.GuestPath)
			data, err := os.ReadFile(dstPath)
			require.NoError(t, err)

			var result map[string]any
			require.NoError(t, toml.Unmarshal(data, &result))

			for k, v := range tt.wantKeys {
				assert.Equal(t, v, result[k], "key %q", k)
			}

			for _, k := range tt.wantAbsent {
				_, exists := result[k]
				assert.False(t, exists, "key %q should be absent", k)
			}
		})
	}
}

func TestStripJSONC(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "strips line comments",
			input: "{\n  // this is a comment\n  \"key\": \"value\"\n}",
			want:  "{\n  \n  \"key\": \"value\"\n}",
		},
		{
			name:  "strips block comments",
			input: "{\n  /* block\n  comment */\n  \"key\": \"value\"\n}",
			want:  "{\n  \n  \"key\": \"value\"\n}",
		},
		{
			name:  "preserves strings with slashes",
			input: `{"url": "https://example.com/path"}`,
			want:  `{"url": "https://example.com/path"}`,
		},
		{
			name:  "preserves escaped quotes in strings",
			input: `{"msg": "say \"hello\""}`,
			want:  `{"msg": "say \"hello\""}`,
		},
		{
			name:  "handles mixed comments",
			input: "{\n  // line\n  \"a\": 1, /* inline */ \"b\": 2\n}",
			want:  "{\n  \n  \"a\": 1,  \"b\": 2\n}",
		},
		{
			name:  "trailing commas",
			input: `{"a": 1, "b": [2, 3,], }`,
			want:  `{"a": 1, "b": [2, 3] }`,
		},
		{
			name:  "trailing comma after comment",
			input: "{\"a\": 1, // comment\n}",
			want:  "{\"a\": 1 \n}",
		},
		{
			name:  "no comments passthrough",
			input: `{"key": "value"}`,
			want:  `{"key": "value"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := stripJSONC([]byte(tt.input))
			assert.Equal(t, tt.want, string(got))
		})
	}
}

func TestFSInjector_MergeFileJSONC(t *testing.T) {
	t.Parallel()

	hostHome := t.TempDir()
	rootfs := t.TempDir()
	setupRootfs(t, rootfs)

	hostContent := `{
  // Editor preference
  "theme": "dark",
  /* API credentials - should be filtered */
  "apiKey": "secret",
  "editor": "vim"
}`

	require.NoError(t, os.WriteFile(
		filepath.Join(hostHome, "settings.jsonc"),
		[]byte(hostContent), 0644))

	entry := settings.Entry{
		HostPath:  "settings.jsonc",
		GuestPath: "settings.json",
		Kind:      settings.KindMergeFile,
		Format:    "jsonc",
		Filter: &settings.FieldFilter{
			AllowKeys: []string{"theme", "editor"},
		},
	}

	inj := NewFSInjector(testLogger())
	manifest := settings.Manifest{Entries: []settings.Entry{entry}}
	err := inj.Inject(rootfs, hostHome, manifest)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(rootfs, sandboxHome, "settings.json"))
	require.NoError(t, err)

	var result map[string]any
	require.NoError(t, json.Unmarshal(data, &result))

	assert.Equal(t, "dark", result["theme"])
	assert.Equal(t, "vim", result["editor"])
	_, hasAPIKey := result["apiKey"]
	assert.False(t, hasAPIKey)
}
