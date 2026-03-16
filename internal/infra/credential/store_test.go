// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package credential

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stacklok/brood-box/pkg/domain/credential"
)

func TestFSStore(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	tests := []struct {
		name  string
		setup func(t *testing.T, baseDir, rootfsPath string)
		check func(t *testing.T, baseDir, rootfsPath string)
	}{
		{
			name: "inject with no saved state is a no-op",
			setup: func(_ *testing.T, _, _ string) {
				// no setup — empty baseDir
			},
			check: func(t *testing.T, _, rootfsPath string) {
				t.Helper()
				// sandbox home should not have been created
				_, err := os.Stat(filepath.Join(rootfsPath, sandboxHome, ".claude"))
				if !os.IsNotExist(err) {
					t.Fatalf("expected no credential dir, got err=%v", err)
				}
			},
		},
		{
			name: "inject with saved state copies files into rootfs",
			setup: func(t *testing.T, baseDir, _ string) {
				t.Helper()
				dir := filepath.Join(baseDir, "claude-code", ".claude")
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(`{"token":"abc"}`), 0600); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, _, rootfsPath string) {
				t.Helper()
				data, err := os.ReadFile(filepath.Join(rootfsPath, sandboxHome, ".claude", "auth.json"))
				if err != nil {
					t.Fatalf("expected injected file: %v", err)
				}
				if string(data) != `{"token":"abc"}` {
					t.Fatalf("unexpected content: %s", data)
				}
			},
		},
		{
			name: "extract respects size cap",
			setup: func(t *testing.T, _, rootfsPath string) {
				t.Helper()
				dir := filepath.Join(rootfsPath, sandboxHome, ".claude")
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				// Write a file larger than MaxFileSize
				big := make([]byte, credential.MaxFileSize+1)
				if err := os.WriteFile(filepath.Join(dir, "toobig"), big, 0600); err != nil {
					t.Fatal(err)
				}
				// Also write a normal file
				if err := os.WriteFile(filepath.Join(dir, "ok.json"), []byte("ok"), 0600); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, baseDir, _ string) {
				t.Helper()
				// Oversized file should be skipped
				_, err := os.Stat(filepath.Join(baseDir, "claude-code", ".claude", "toobig"))
				if !os.IsNotExist(err) {
					t.Fatal("oversized file should not have been extracted")
				}
				// Normal file should be extracted
				data, err := os.ReadFile(filepath.Join(baseDir, "claude-code", ".claude", "ok.json"))
				if err != nil {
					t.Fatalf("expected ok.json: %v", err)
				}
				if string(data) != "ok" {
					t.Fatalf("unexpected content: %s", data)
				}
			},
		},
		{
			name: "extract rejects symlinks",
			setup: func(t *testing.T, _, rootfsPath string) {
				t.Helper()
				dir := filepath.Join(rootfsPath, sandboxHome, ".claude")
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "real.json"), []byte("real"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("real.json", filepath.Join(dir, "link.json")); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, baseDir, _ string) {
				t.Helper()
				// symlink should not be extracted
				_, err := os.Stat(filepath.Join(baseDir, "claude-code", ".claude", "link.json"))
				if !os.IsNotExist(err) {
					t.Fatal("symlink should not have been extracted")
				}
				// real file should be extracted
				_, err = os.Stat(filepath.Join(baseDir, "claude-code", ".claude", "real.json"))
				if err != nil {
					t.Fatalf("expected real.json: %v", err)
				}
			},
		},
		{
			name: "extract respects file count cap",
			setup: func(t *testing.T, _, rootfsPath string) {
				t.Helper()
				dir := filepath.Join(rootfsPath, sandboxHome, ".claude")
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				// Create MaxFileCount + 10 files
				for i := 0; i < credential.MaxFileCount+10; i++ {
					name := filepath.Join(dir, strings.Repeat("f", 1)+string(rune('a'+i%26))+strings.Repeat("0", 3))
					if err := os.WriteFile(name, []byte("x"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			},
			check: func(t *testing.T, baseDir, _ string) {
				t.Helper()
				dir := filepath.Join(baseDir, "claude-code", ".claude")
				entries, err := os.ReadDir(dir)
				if err != nil {
					t.Fatalf("reading extracted dir: %v", err)
				}
				// Filter out lock file
				count := 0
				for _, e := range entries {
					if !e.IsDir() {
						count++
					}
				}
				if count > credential.MaxFileCount {
					t.Fatalf("expected at most %d files, got %d", credential.MaxFileCount, count)
				}
			},
		},
		{
			name: "extract respects depth limit",
			setup: func(t *testing.T, _, rootfsPath string) {
				t.Helper()
				// Create a deeply nested file: depth 0 = .claude/, depth 1 = a/, depth 2 = b/, depth 3 = c/ (exceeds MaxDepth=3)
				deep := filepath.Join(rootfsPath, sandboxHome, ".claude", "a", "b", "c")
				if err := os.MkdirAll(deep, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(deep, "deep.json"), []byte("deep"), 0600); err != nil {
					t.Fatal(err)
				}
				// Also write at acceptable depth
				shallow := filepath.Join(rootfsPath, sandboxHome, ".claude", "a")
				if err := os.WriteFile(filepath.Join(shallow, "shallow.json"), []byte("shallow"), 0600); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, baseDir, _ string) {
				t.Helper()
				// Deep file should not exist
				_, err := os.Stat(filepath.Join(baseDir, "claude-code", ".claude", "a", "b", "c", "deep.json"))
				if !os.IsNotExist(err) {
					t.Fatal("deep file should not have been extracted")
				}
				// Shallow file should exist
				data, err := os.ReadFile(filepath.Join(baseDir, "claude-code", ".claude", "a", "shallow.json"))
				if err != nil {
					t.Fatalf("expected shallow.json: %v", err)
				}
				if string(data) != "shallow" {
					t.Fatalf("unexpected content: %s", data)
				}
			},
		},
		{
			name: "extract atomic write produces final file",
			setup: func(t *testing.T, _, rootfsPath string) {
				t.Helper()
				dir := filepath.Join(rootfsPath, sandboxHome, ".claude")
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, "token"), []byte("secret"), 0600); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, baseDir, _ string) {
				t.Helper()
				data, err := os.ReadFile(filepath.Join(baseDir, "claude-code", ".claude", "token"))
				if err != nil {
					t.Fatalf("expected token file: %v", err)
				}
				if string(data) != "secret" {
					t.Fatalf("unexpected content: %s", data)
				}
				// No temp files should remain
				entries, err := os.ReadDir(filepath.Join(baseDir, "claude-code", ".claude"))
				if err != nil {
					t.Fatal(err)
				}
				for _, e := range entries {
					if strings.HasPrefix(e.Name(), ".cred-") && strings.HasSuffix(e.Name(), ".tmp") {
						t.Fatalf("temp file left behind: %s", e.Name())
					}
				}
			},
		},
		{
			name: "path traversal rejection",
			setup: func(t *testing.T, _, rootfsPath string) {
				t.Helper()
				dir := filepath.Join(rootfsPath, sandboxHome, ".claude")
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				// Create a file with .. in its name
				if err := os.WriteFile(filepath.Join(dir, "..sneaky"), []byte("evil"), 0600); err != nil {
					t.Fatal(err)
				}
			},
			check: func(t *testing.T, baseDir, _ string) {
				t.Helper()
				// File with .. should be skipped
				_, err := os.Stat(filepath.Join(baseDir, "claude-code", ".claude", "..sneaky"))
				if !os.IsNotExist(err) {
					t.Fatal("path traversal file should not have been extracted")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			baseDir := t.TempDir()
			rootfsPath := t.TempDir()

			// Ensure sandbox home exists in rootfs
			if err := os.MkdirAll(filepath.Join(rootfsPath, sandboxHome), 0700); err != nil {
				t.Fatal(err)
			}

			tt.setup(t, baseDir, rootfsPath)

			store := NewFSStore(baseDir, logger)

			credPaths := []string{".claude"}

			isExtractTest := strings.HasPrefix(tt.name, "extract") || strings.HasPrefix(tt.name, "path")
			if isExtractTest {
				err := store.Extract(rootfsPath, "claude-code", credPaths)
				if err != nil {
					t.Fatalf("Extract failed: %v", err)
				}
			} else {
				err := store.Inject(rootfsPath, "claude-code", credPaths)
				if err != nil {
					t.Fatalf("Inject failed: %v", err)
				}
			}

			tt.check(t, baseDir, rootfsPath)
		})
	}
}

func TestSeedFile(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	t.Run("seeds new file", func(t *testing.T) {
		t.Parallel()
		baseDir := t.TempDir()
		store := NewFSStore(baseDir, logger)

		err := store.SeedFile("claude-code", ".claude/.credentials.json", []byte(`{"token":"abc"}`))
		if err != nil {
			t.Fatalf("SeedFile failed: %v", err)
		}

		data, err := os.ReadFile(filepath.Join(baseDir, "claude-code", ".claude", ".credentials.json"))
		if err != nil {
			t.Fatalf("expected seeded file: %v", err)
		}
		if string(data) != `{"token":"abc"}` {
			t.Fatalf("unexpected content: %s", data)
		}
	})

	t.Run("skips existing file", func(t *testing.T) {
		t.Parallel()
		baseDir := t.TempDir()
		store := NewFSStore(baseDir, logger)

		// Pre-create the file.
		dst := filepath.Join(baseDir, "claude-code", ".claude", ".credentials.json")
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}

		err := store.SeedFile("claude-code", ".claude/.credentials.json", []byte("replacement"))
		if err != nil {
			t.Fatalf("SeedFile failed: %v", err)
		}

		data, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "original" {
			t.Fatalf("seed should not overwrite existing file, got: %s", data)
		}
	})

	t.Run("rejects path traversal", func(t *testing.T) {
		t.Parallel()
		baseDir := t.TempDir()
		store := NewFSStore(baseDir, logger)

		err := store.SeedFile("claude-code", "../escape/creds.json", []byte("evil"))
		if err == nil {
			t.Fatal("expected error for path traversal")
		}
	})

	t.Run("rejects invalid agent name", func(t *testing.T) {
		t.Parallel()
		baseDir := t.TempDir()
		store := NewFSStore(baseDir, logger)

		err := store.SeedFile("../evil", ".claude/.credentials.json", []byte("x"))
		if err == nil {
			t.Fatal("expected error for invalid agent name")
		}
	})
}

func TestReadFile(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	t.Run("reads existing file", func(t *testing.T) {
		t.Parallel()
		baseDir := t.TempDir()
		store := NewFSStore(baseDir, logger)

		// Pre-create the file.
		dst := filepath.Join(baseDir, "claude-code", ".claude", ".credentials.json")
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(`{"token":"abc"}`), 0600); err != nil {
			t.Fatal(err)
		}

		data, err := store.ReadFile("claude-code", ".claude/.credentials.json")
		if err != nil {
			t.Fatalf("ReadFile failed: %v", err)
		}
		if string(data) != `{"token":"abc"}` {
			t.Fatalf("unexpected content: %s", data)
		}
	})

	t.Run("returns error for missing file", func(t *testing.T) {
		t.Parallel()
		baseDir := t.TempDir()
		store := NewFSStore(baseDir, logger)

		_, err := store.ReadFile("claude-code", ".claude/.credentials.json")
		if err == nil {
			t.Fatal("expected error for missing file")
		}
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected os.ErrNotExist, got: %v", err)
		}
	})

	t.Run("rejects invalid agent name", func(t *testing.T) {
		t.Parallel()
		baseDir := t.TempDir()
		store := NewFSStore(baseDir, logger)

		_, err := store.ReadFile("../evil", ".claude/.credentials.json")
		if err == nil {
			t.Fatal("expected error for invalid agent name")
		}
	})

	t.Run("rejects path traversal", func(t *testing.T) {
		t.Parallel()
		baseDir := t.TempDir()
		store := NewFSStore(baseDir, logger)

		_, err := store.ReadFile("claude-code", "../escape/creds.json")
		if err == nil {
			t.Fatal("expected error for path traversal")
		}
	})
}

func TestOverwriteFile(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	t.Run("writes new file", func(t *testing.T) {
		t.Parallel()
		baseDir := t.TempDir()
		store := NewFSStore(baseDir, logger)

		err := store.OverwriteFile("claude-code", ".claude/.credentials.json", []byte(`{"token":"abc"}`))
		if err != nil {
			t.Fatalf("OverwriteFile failed: %v", err)
		}

		data, err := os.ReadFile(filepath.Join(baseDir, "claude-code", ".claude", ".credentials.json"))
		if err != nil {
			t.Fatalf("expected written file: %v", err)
		}
		if string(data) != `{"token":"abc"}` {
			t.Fatalf("unexpected content: %s", data)
		}
	})

	t.Run("overwrites existing file", func(t *testing.T) {
		t.Parallel()
		baseDir := t.TempDir()
		store := NewFSStore(baseDir, logger)

		// Pre-create the file with original content.
		dst := filepath.Join(baseDir, "claude-code", ".claude", ".credentials.json")
		if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte("original"), 0600); err != nil {
			t.Fatal(err)
		}

		err := store.OverwriteFile("claude-code", ".claude/.credentials.json", []byte("replacement"))
		if err != nil {
			t.Fatalf("OverwriteFile failed: %v", err)
		}

		data, err := os.ReadFile(dst)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "replacement" {
			t.Fatalf("expected overwritten content, got: %s", data)
		}
	})

	t.Run("rejects invalid agent name", func(t *testing.T) {
		t.Parallel()
		baseDir := t.TempDir()
		store := NewFSStore(baseDir, logger)

		err := store.OverwriteFile("../evil", ".claude/.credentials.json", []byte("x"))
		if err == nil {
			t.Fatal("expected error for invalid agent name")
		}
	})

	t.Run("rejects path traversal", func(t *testing.T) {
		t.Parallel()
		baseDir := t.TempDir()
		store := NewFSStore(baseDir, logger)

		err := store.OverwriteFile("claude-code", "../escape/creds.json", []byte("evil"))
		if err == nil {
			t.Fatal("expected error for path traversal")
		}
	})
}

func TestContainedPath(t *testing.T) {
	t.Parallel()

	base := t.TempDir()

	tests := []struct {
		name    string
		target  string
		wantErr bool
	}{
		{
			name:    "valid child path",
			target:  filepath.Join(base, "child", "file.txt"),
			wantErr: false,
		},
		{
			name:    "path traversal with ..",
			target:  filepath.Join(base, "..", "escape"),
			wantErr: true,
		},
		{
			name:    "exact base is valid",
			target:  filepath.Join(base, "file"),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := containedPath(base, tt.target)
			if (err != nil) != tt.wantErr {
				t.Errorf("containedPath(%q, %q) error = %v, wantErr %v", base, tt.target, err, tt.wantErr)
			}
		})
	}
}
