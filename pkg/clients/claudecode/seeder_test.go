// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package claudecode

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"testing"

	infracred "github.com/stacklok/brood-box/internal/infra/credential"
)

func TestExtractExpiresAt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want int64
	}{
		{
			name: "valid credentials",
			data: `{"claudeAiOauth":{"accessToken":"tok","expiresAt":1773402187165}}`,
			want: 1773402187165,
		},
		{
			name: "missing expiresAt",
			data: `{"claudeAiOauth":{"accessToken":"tok"}}`,
			want: 0,
		},
		{
			name: "empty object",
			data: `{}`,
			want: 0,
		},
		{
			name: "invalid JSON",
			data: `not json`,
			want: 0,
		},
		{
			name: "empty input",
			data: ``,
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractExpiresAt([]byte(tt.data))
			if got != tt.want {
				t.Errorf("extractExpiresAt() = %d, want %d", got, tt.want)
			}
		})
	}
}

// makeCreds returns a JSON credential blob with the given expiresAt value.
func makeCreds(t *testing.T, expiresAt int64) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"claudeAiOauth": map[string]any{
			"accessToken":  "sk-ant-oat01-test",
			"refreshToken": "sk-ant-ort01-test",
			"expiresAt":    expiresAt,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// testSeeder creates a seeder with injected host credential reader
// and time function for testing.
func testSeeder(t *testing.T, readHost func() ([]byte, string, error), nowMs func() int64) *seeder {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	s := newSeeder(logger)
	if readHost != nil {
		s.readHost = readHost
	}
	if nowMs != nil {
		s.nowMs = nowMs
	}
	return s
}

// TestSeedExpiry tests the expiry-aware credential seeding logic.
func TestSeedExpiry(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	t.Run("seeds when no stored credentials exist", func(t *testing.T) {
		t.Parallel()
		baseDir := t.TempDir()
		store := infracred.NewFSStore(baseDir, logger)
		hostCreds := makeCreds(t, 9999999999999)

		s := testSeeder(t, func() ([]byte, string, error) {
			return hostCreds, "test", nil
		}, nil)

		if err := s.Seed(store); err != nil {
			t.Fatalf("Seed failed: %v", err)
		}

		data, err := store.ReadFile(Name, credPath)
		if err != nil {
			t.Fatalf("expected seeded file: %v", err)
		}
		if extractExpiresAt(data) != 9999999999999 {
			t.Fatalf("expected expiresAt=9999999999999, got %d", extractExpiresAt(data))
		}
	})

	t.Run("keeps valid stored credentials", func(t *testing.T) {
		t.Parallel()
		baseDir := t.TempDir()
		store := infracred.NewFSStore(baseDir, logger)

		if err := store.SeedFile(Name, credPath, makeCreds(t, 9999999999999)); err != nil {
			t.Fatal(err)
		}

		s := testSeeder(t, func() ([]byte, string, error) {
			return makeCreds(t, 8888888888888), "test", nil
		}, func() int64 { return 1000000000000 })

		if err := s.Seed(store); err != nil {
			t.Fatalf("Seed failed: %v", err)
		}

		data, err := store.ReadFile(Name, credPath)
		if err != nil {
			t.Fatal(err)
		}
		if extractExpiresAt(data) != 9999999999999 {
			t.Fatal("stored credentials should not have been overwritten")
		}
	})

	t.Run("overwrites expired with fresher host creds", func(t *testing.T) {
		t.Parallel()
		baseDir := t.TempDir()
		store := infracred.NewFSStore(baseDir, logger)

		if err := store.SeedFile(Name, credPath, makeCreds(t, 1000000000000)); err != nil {
			t.Fatal(err)
		}

		s := testSeeder(t, func() ([]byte, string, error) {
			return makeCreds(t, 3000000000000), "test", nil
		}, func() int64 { return 2000000000000 })

		if err := s.Seed(store); err != nil {
			t.Fatalf("Seed failed: %v", err)
		}

		data, err := store.ReadFile(Name, credPath)
		if err != nil {
			t.Fatal(err)
		}
		if extractExpiresAt(data) != 3000000000000 {
			t.Fatalf("expected expiresAt=3000000000000, got %d", extractExpiresAt(data))
		}
	})

	t.Run("skips when host creds not fresher", func(t *testing.T) {
		t.Parallel()
		baseDir := t.TempDir()
		store := infracred.NewFSStore(baseDir, logger)

		if err := store.SeedFile(Name, credPath, makeCreds(t, 1000000000000)); err != nil {
			t.Fatal(err)
		}

		s := testSeeder(t, func() ([]byte, string, error) {
			return makeCreds(t, 500000000000), "test", nil
		}, func() int64 { return 2000000000000 })

		if err := s.Seed(store); err != nil {
			t.Fatalf("Seed failed: %v", err)
		}

		data, err := store.ReadFile(Name, credPath)
		if err != nil {
			t.Fatal(err)
		}
		if extractExpiresAt(data) != 1000000000000 {
			t.Fatal("stored credentials should not have been changed")
		}
	})

	t.Run("no-op when host creds unavailable", func(t *testing.T) {
		t.Parallel()
		baseDir := t.TempDir()
		store := infracred.NewFSStore(baseDir, logger)

		s := testSeeder(t, func() ([]byte, string, error) {
			return nil, "", fmt.Errorf("no credentials available")
		}, nil)

		if err := s.Seed(store); err != nil {
			t.Fatalf("Seed should return nil when host creds unavailable: %v", err)
		}

		_, err := store.ReadFile(Name, credPath)
		if err == nil {
			t.Fatal("expected no stored credentials when host creds unavailable")
		}
	})
}
