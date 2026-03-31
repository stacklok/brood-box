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
			_, err := inj.Inject(rootfs, hostHome, manifest)

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
			_, err := inj.Inject(rootfs, hostHome, manifest)

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
			_, err := inj.Inject(rootfs, hostHome, manifest)
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
			_, err := inj.Inject(rootfs, hostHome, manifest)
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
	_, err := inj.Inject(rootfs, hostHome, manifest)
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

func TestStripKeyRecursive(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  map[string]any
		subKey string
		want   map[string]any
	}{
		{
			name:   "strips key at top level",
			input:  map[string]any{"apiKey": "secret", "theme": "dark"},
			subKey: "apiKey",
			want:   map[string]any{"theme": "dark"},
		},
		{
			name: "strips key at nested level",
			input: map[string]any{
				"github": map[string]any{
					"url":    "https://github.com",
					"apiKey": "ghp_secret",
				},
				"theme": "dark",
			},
			subKey: "apiKey",
			want: map[string]any{
				"github": map[string]any{
					"url": "https://github.com",
				},
				"theme": "dark",
			},
		},
		{
			name: "strips key at three levels deep",
			input: map[string]any{
				"level1": map[string]any{
					"level2": map[string]any{
						"level3": map[string]any{
							"apiKey": "deep-secret",
							"safe":   "keep",
						},
						"apiKey": "mid-secret",
					},
					"apiKey": "shallow-secret",
				},
			},
			subKey: "apiKey",
			want: map[string]any{
				"level1": map[string]any{
					"level2": map[string]any{
						"level3": map[string]any{
							"safe": "keep",
						},
					},
				},
			},
		},
		{
			name: "handles mixed nesting (maps and scalars)",
			input: map[string]any{
				"apiKey": "top-secret",
				"count":  42,
				"nested": map[string]any{
					"apiKey": "nested-secret",
					"list":   []string{"a", "b"},
				},
				"flat": "value",
			},
			subKey: "apiKey",
			want: map[string]any{
				"count": 42,
				"nested": map[string]any{
					"list": []string{"a", "b"},
				},
				"flat": "value",
			},
		},
		{
			name:   "no-op when key does not exist",
			input:  map[string]any{"theme": "dark", "editor": "vim"},
			subKey: "apiKey",
			want:   map[string]any{"theme": "dark", "editor": "vim"},
		},
		{
			name:   "empty map is a no-op",
			input:  map[string]any{},
			subKey: "apiKey",
			want:   map[string]any{},
		},
		{
			name: "strips key only from maps, not scalar siblings",
			input: map[string]any{
				"providers": map[string]any{
					"gh": map[string]any{
						"apiKey": "ghp_123",
						"url":    "https://github.com",
					},
					"gl": map[string]any{
						"apiKey": "glpat_456",
						"url":    "https://gitlab.com",
					},
				},
				"other": "scalar-value",
			},
			subKey: "apiKey",
			want: map[string]any{
				"providers": map[string]any{
					"gh": map[string]any{
						"url": "https://github.com",
					},
					"gl": map[string]any{
						"url": "https://gitlab.com",
					},
				},
				"other": "scalar-value",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stripKeyRecursive(tt.input, tt.subKey)
			assert.Equal(t, tt.want, tt.input)
		})
	}
}

func TestDeepMerge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dst  map[string]any
		src  map[string]any
		want map[string]any
	}{
		{
			name: "disjoint keys are combined",
			dst:  map[string]any{"a": 1},
			src:  map[string]any{"b": 2},
			want: map[string]any{"a": 1, "b": 2},
		},
		{
			name: "overlapping scalar keys — src wins",
			dst:  map[string]any{"key": "old"},
			src:  map[string]any{"key": "new"},
			want: map[string]any{"key": "new"},
		},
		{
			name: "nested map merge is recursive",
			dst: map[string]any{
				"outer": map[string]any{
					"a": 1,
					"b": 2,
				},
			},
			src: map[string]any{
				"outer": map[string]any{
					"b": 99,
					"c": 3,
				},
			},
			want: map[string]any{
				"outer": map[string]any{
					"a": 1,
					"b": 99,
					"c": 3,
				},
			},
		},
		{
			name: "src has map, dst has scalar for same key — src wins",
			dst:  map[string]any{"key": "scalar"},
			src:  map[string]any{"key": map[string]any{"nested": true}},
			want: map[string]any{"key": map[string]any{"nested": true}},
		},
		{
			name: "dst has map, src has scalar for same key — src wins",
			dst:  map[string]any{"key": map[string]any{"nested": true}},
			src:  map[string]any{"key": "scalar"},
			want: map[string]any{"key": "scalar"},
		},
		{
			name: "empty src leaves dst unchanged",
			dst:  map[string]any{"a": 1, "b": 2},
			src:  map[string]any{},
			want: map[string]any{"a": 1, "b": 2},
		},
		{
			name: "empty dst returns src",
			dst:  map[string]any{},
			src:  map[string]any{"a": 1},
			want: map[string]any{"a": 1},
		},
		{
			name: "both empty returns empty",
			dst:  map[string]any{},
			src:  map[string]any{},
			want: map[string]any{},
		},
		{
			name: "nil values in maps are preserved",
			dst:  map[string]any{"a": nil},
			src:  map[string]any{"b": nil},
			want: map[string]any{"a": nil, "b": nil},
		},
		{
			name: "src nil value overwrites dst scalar",
			dst:  map[string]any{"key": "value"},
			src:  map[string]any{"key": nil},
			want: map[string]any{"key": nil},
		},
		{
			name: "does not mutate original dst",
			dst:  map[string]any{"a": 1},
			src:  map[string]any{"b": 2},
			want: map[string]any{"a": 1, "b": 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := deepMerge(tt.dst, tt.src)
			assert.Equal(t, tt.want, got)
		})
	}

	// Verify deepMerge does not mutate the original dst map.
	t.Run("original dst is not mutated", func(t *testing.T) {
		t.Parallel()

		dst := map[string]any{"a": 1}
		src := map[string]any{"b": 2}
		result := deepMerge(dst, src)

		// result has both keys
		assert.Equal(t, map[string]any{"a": 1, "b": 2}, result)
		// original dst should be unmodified
		_, hasBInDst := dst["b"]
		assert.False(t, hasBInDst, "original dst should not contain key 'b'")
	})
}

func TestDeepMergeN(t *testing.T) {
	t.Parallel()

	t.Run("respects MaxDepth — replaces wholesale at depth limit", func(t *testing.T) {
		t.Parallel()

		// The check in deepMergeN is: if depth < MaxDepth, recurse; else replace.
		// At depth d, processing a key whose src and dst are both maps:
		//   - d < MaxDepth  -> recurse into depth d+1
		//   - d >= MaxDepth -> src map replaces dst map wholesale
		//
		// We need MaxDepth+1 layers of map nesting so that the innermost
		// pair of leaf maps is encountered at depth == MaxDepth, where
		// the wholesale-replace path fires.
		dstLeaf := map[string]any{"dstOnly": "kept-by-dst", "shared": "dst-val"}
		srcLeaf := map[string]any{"srcOnly": "from-src", "shared": "src-val"}

		// Wrap in MaxDepth+1 layers so the leaf maps sit at depth MaxDepth.
		var dst, src any = dstLeaf, srcLeaf
		for i := 0; i < settings.MaxDepth+1; i++ {
			dst = map[string]any{"nest": dst}
			src = map[string]any{"nest": src}
		}

		dstMap := dst.(map[string]any)
		srcMap := src.(map[string]any)

		result := deepMergeN(dstMap, srcMap, 0)

		// Navigate to the leaf.
		current := result
		for i := 0; i < settings.MaxDepth+1; i++ {
			nested, ok := current["nest"].(map[string]any)
			require.True(t, ok, "expected map at depth %d", i)
			current = nested
		}

		// At MaxDepth, src replaces dst wholesale — so dstOnly should NOT exist.
		assert.Equal(t, "from-src", current["srcOnly"], "srcOnly should be present")
		assert.Equal(t, "src-val", current["shared"], "shared should be src value")
		_, hasDstOnly := current["dstOnly"]
		assert.False(t, hasDstOnly, "dstOnly should be absent because src replaced dst at MaxDepth")
	})

	t.Run("merges recursively below MaxDepth", func(t *testing.T) {
		t.Parallel()

		// With MaxDepth wrappings, the leaf maps sit at depth MaxDepth-1
		// (one below the cutoff), so they should be recursively merged.
		dstLeaf := map[string]any{"dstOnly": "kept", "shared": "dst-val"}
		srcLeaf := map[string]any{"srcOnly": "added", "shared": "src-val"}

		var dst, src any = dstLeaf, srcLeaf
		for i := 0; i < settings.MaxDepth; i++ {
			dst = map[string]any{"nest": dst}
			src = map[string]any{"nest": src}
		}

		dstMap := dst.(map[string]any)
		srcMap := src.(map[string]any)

		result := deepMergeN(dstMap, srcMap, 0)

		// Navigate to the leaf.
		current := result
		for i := 0; i < settings.MaxDepth; i++ {
			nested, ok := current["nest"].(map[string]any)
			require.True(t, ok, "expected map at depth %d", i)
			current = nested
		}

		// Below MaxDepth — recursive merge should happen, so dstOnly IS present.
		assert.Equal(t, "kept", current["dstOnly"], "dstOnly should be preserved by merge")
		assert.Equal(t, "added", current["srcOnly"], "srcOnly should be added by merge")
		assert.Equal(t, "src-val", current["shared"], "shared should be overridden by src")
	})

	t.Run("depth parameter offsets the limit", func(t *testing.T) {
		t.Parallel()

		dst := map[string]any{
			"inner": map[string]any{"a": 1, "b": 2},
		}
		src := map[string]any{
			"inner": map[string]any{"b": 99, "c": 3},
		}

		// Call with depth == MaxDepth: should NOT recurse into "inner".
		result := deepMergeN(dst, src, settings.MaxDepth)
		inner, ok := result["inner"].(map[string]any)
		require.True(t, ok)
		// src replaces wholesale, so "a" (dst-only) should be gone.
		_, hasA := inner["a"]
		assert.False(t, hasA, "at MaxDepth, src map replaces dst map wholesale")
		assert.Equal(t, 99, inner["b"])
		assert.Equal(t, 3, inner["c"])
	})

	t.Run("depth below MaxDepth recurses", func(t *testing.T) {
		t.Parallel()

		dst := map[string]any{
			"inner": map[string]any{"a": 1, "b": 2},
		}
		src := map[string]any{
			"inner": map[string]any{"b": 99, "c": 3},
		}

		// Call with depth == MaxDepth-1: should recurse into "inner".
		result := deepMergeN(dst, src, settings.MaxDepth-1)
		inner, ok := result["inner"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, 1, inner["a"], "a preserved from dst")
		assert.Equal(t, 99, inner["b"], "b overridden by src")
		assert.Equal(t, 3, inner["c"], "c added from src")
	})
}

func TestApplyDenySubKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		val      any
		patterns []string
		want     any
	}{
		{
			name: "wildcard strips key recursively in nested maps",
			val: map[string]any{
				"github": map[string]any{
					"url":    "https://github.com",
					"apiKey": "ghp_secret",
				},
				"gitlab": map[string]any{
					"url":    "https://gitlab.com",
					"apiKey": "glpat_secret",
					"nested": map[string]any{
						"apiKey": "deep-secret",
						"safe":   "keep",
					},
				},
			},
			patterns: []string{"*.apiKey"},
			want: map[string]any{
				"github": map[string]any{
					"url": "https://github.com",
				},
				"gitlab": map[string]any{
					"url": "https://gitlab.com",
					"nested": map[string]any{
						"safe": "keep",
					},
				},
			},
		},
		{
			name: "plain pattern deletes top-level key only",
			val: map[string]any{
				"apiKey": "top-level",
				"nested": map[string]any{
					"apiKey": "should-remain",
				},
			},
			patterns: []string{"apiKey"},
			want: map[string]any{
				"nested": map[string]any{
					"apiKey": "should-remain",
				},
			},
		},
		{
			name: "multiple patterns applied together",
			val: map[string]any{
				"provider": map[string]any{
					"apiKey": "secret",
					"token":  "also-secret",
					"url":    "https://example.com",
				},
			},
			patterns: []string{"*.apiKey", "*.token"},
			want: map[string]any{
				"provider": map[string]any{
					"url": "https://example.com",
				},
			},
		},
		{
			name:     "non-map value is returned unchanged",
			val:      "scalar-string",
			patterns: []string{"*.apiKey"},
			want:     "scalar-string",
		},
		{
			name:     "nil value is returned unchanged",
			val:      nil,
			patterns: []string{"*.apiKey"},
			want:     nil,
		},
		{
			name: "empty patterns is a no-op",
			val: map[string]any{
				"apiKey": "kept",
			},
			patterns: []string{},
			want: map[string]any{
				"apiKey": "kept",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := applyDenySubKeys(tt.val, tt.patterns)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestApplyDenySubKeys_EndToEnd(t *testing.T) {
	t.Parallel()

	// Simulate a realistic scenario: multi-level provider config with
	// API keys at various depths, all stripped by "*.apiKey".
	data := map[string]any{
		"providers": map[string]any{
			"github": map[string]any{
				"url":    "https://github.com",
				"apiKey": "ghp_top",
				"oauth": map[string]any{
					"apiKey":      "ghp_oauth",
					"callbackURL": "http://localhost/callback",
				},
			},
			"gitlab": map[string]any{
				"url":    "https://gitlab.com",
				"apiKey": "glpat_top",
			},
		},
		"theme": "dark",
	}

	filter := &settings.FieldFilter{
		AllowKeys:   []string{"providers", "theme"},
		DenySubKeys: map[string][]string{"providers": {"*.apiKey"}},
	}

	result := applyFilter(data, filter)

	// theme should be preserved.
	assert.Equal(t, "dark", result["theme"])

	providers, ok := result["providers"].(map[string]any)
	require.True(t, ok)

	// github
	gh, ok := providers["github"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "https://github.com", gh["url"])
	_, hasKey := gh["apiKey"]
	assert.False(t, hasKey, "github top-level apiKey should be stripped")

	oauth, ok := gh["oauth"].(map[string]any)
	require.True(t, ok)
	_, hasKey = oauth["apiKey"]
	assert.False(t, hasKey, "github oauth apiKey should be stripped")
	assert.Equal(t, "http://localhost/callback", oauth["callbackURL"])

	// gitlab
	gl, ok := providers["gitlab"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "https://gitlab.com", gl["url"])
	_, hasKey = gl["apiKey"]
	assert.False(t, hasKey, "gitlab apiKey should be stripped")
}

func TestFSInjector_Extract_File(t *testing.T) {
	t.Parallel()

	rootfs := t.TempDir()
	hostHome := t.TempDir()
	setupRootfs(t, rootfs)

	// Place a file in the guest rootfs (as if agent created it).
	guestFile := filepath.Join(rootfs, sandboxHome, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(guestFile), 0755))
	require.NoError(t, os.WriteFile(guestFile, []byte(`{"theme":"dark"}`), 0644))

	entry := settings.Entry{
		Category:  "settings",
		HostPath:  ".claude/settings.json",
		GuestPath: ".claude/settings.json",
		Kind:      settings.KindFile,
	}

	inj := NewFSInjector(testLogger())
	manifest := settings.Manifest{Entries: []settings.Entry{entry}}
	result, err := inj.Extract(rootfs, hostHome, manifest)
	require.NoError(t, err)
	assert.Equal(t, 1, result.FileCount)

	// Verify file was written to host.
	data, err := os.ReadFile(filepath.Join(hostHome, ".claude", "settings.json"))
	require.NoError(t, err)
	assert.Equal(t, `{"theme":"dark"}`, string(data))
}

func TestFSInjector_Extract_SkipsMissingGuest(t *testing.T) {
	t.Parallel()

	rootfs := t.TempDir()
	hostHome := t.TempDir()
	setupRootfs(t, rootfs)

	entry := settings.Entry{
		Category:  "settings",
		HostPath:  ".claude/settings.json",
		GuestPath: ".claude/settings.json",
		Kind:      settings.KindFile,
	}

	inj := NewFSInjector(testLogger())
	manifest := settings.Manifest{Entries: []settings.Entry{entry}}
	result, err := inj.Extract(rootfs, hostHome, manifest)
	require.NoError(t, err)
	assert.Equal(t, 0, result.FileCount)
}

func TestFSInjector_Extract_MergeFileSkipsWhenHostExists(t *testing.T) {
	t.Parallel()

	rootfs := t.TempDir()
	hostHome := t.TempDir()
	setupRootfs(t, rootfs)

	// Place file in both guest and host.
	guestFile := filepath.Join(rootfs, sandboxHome, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(guestFile), 0755))
	require.NoError(t, os.WriteFile(guestFile, []byte(`{"modified":true}`), 0644))

	hostFile := filepath.Join(hostHome, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(hostFile), 0755))
	require.NoError(t, os.WriteFile(hostFile, []byte(`{"original":true}`), 0644))

	entry := settings.Entry{
		Category:  "settings",
		HostPath:  ".claude/settings.json",
		GuestPath: ".claude/settings.json",
		Kind:      settings.KindMergeFile,
		Format:    "json",
	}

	inj := NewFSInjector(testLogger())
	manifest := settings.Manifest{Entries: []settings.Entry{entry}}
	result, err := inj.Extract(rootfs, hostHome, manifest)
	require.NoError(t, err)
	assert.Equal(t, 0, result.FileCount)

	// Host file should be unchanged.
	data, err := os.ReadFile(hostFile)
	require.NoError(t, err)
	assert.Equal(t, `{"original":true}`, string(data))
}

func TestFSInjector_Extract_MergeFileExtractsWhenHostMissing(t *testing.T) {
	t.Parallel()

	rootfs := t.TempDir()
	hostHome := t.TempDir()
	setupRootfs(t, rootfs)

	// Place file only in guest.
	guestFile := filepath.Join(rootfs, sandboxHome, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(guestFile), 0755))
	require.NoError(t, os.WriteFile(guestFile, []byte(`{"new":true}`), 0644))

	entry := settings.Entry{
		Category:  "settings",
		HostPath:  ".claude/settings.json",
		GuestPath: ".claude/settings.json",
		Kind:      settings.KindMergeFile,
		Format:    "json",
	}

	inj := NewFSInjector(testLogger())
	manifest := settings.Manifest{Entries: []settings.Entry{entry}}
	result, err := inj.Extract(rootfs, hostHome, manifest)
	require.NoError(t, err)
	assert.Equal(t, 1, result.FileCount)

	data, err := os.ReadFile(filepath.Join(hostHome, ".claude", "settings.json"))
	require.NoError(t, err)
	assert.Equal(t, `{"new":true}`, string(data))
}

func TestFSInjector_Extract_Directory(t *testing.T) {
	t.Parallel()

	rootfs := t.TempDir()
	hostHome := t.TempDir()
	setupRootfs(t, rootfs)

	// Create a directory with files in the guest.
	rulesDir := filepath.Join(rootfs, sandboxHome, ".claude", "rules")
	require.NoError(t, os.MkdirAll(rulesDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(rulesDir, "rule1.md"), []byte("rule 1"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(rulesDir, "rule2.md"), []byte("rule 2"), 0644))

	entry := settings.Entry{
		Category:  "rules",
		HostPath:  ".claude/rules",
		GuestPath: ".claude/rules",
		Kind:      settings.KindDirectory,
		Optional:  true,
	}

	inj := NewFSInjector(testLogger())
	manifest := settings.Manifest{Entries: []settings.Entry{entry}}
	result, err := inj.Extract(rootfs, hostHome, manifest)
	require.NoError(t, err)
	assert.Equal(t, 2, result.FileCount)

	data1, err := os.ReadFile(filepath.Join(hostHome, ".claude", "rules", "rule1.md"))
	require.NoError(t, err)
	assert.Equal(t, "rule 1", string(data1))

	data2, err := os.ReadFile(filepath.Join(hostHome, ".claude", "rules", "rule2.md"))
	require.NoError(t, err)
	assert.Equal(t, "rule 2", string(data2))
}
