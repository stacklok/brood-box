// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- helpers ---

type testTarEntry struct {
	Name     string
	Content  []byte
	Typeflag byte
	Mode     int64
}

// createTestTarGz creates a tar.gz archive from entries and returns its path.
func createTestTarGz(t *testing.T, entries []testTarEntry) string {
	t.Helper()
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "test.tar.gz")

	f, err := os.Create(archivePath)
	require.NoError(t, err)
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.Name,
			Size:     int64(len(e.Content)),
			Typeflag: e.Typeflag,
			Mode:     e.Mode,
		}
		if e.Typeflag == 0 {
			hdr.Typeflag = tar.TypeReg
		}
		require.NoError(t, tw.WriteHeader(hdr))
		if len(e.Content) > 0 {
			_, err := tw.Write(e.Content)
			require.NoError(t, err)
		}
	}

	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	require.NoError(t, f.Close())
	return archivePath
}

// --- extractTarGz tests ---

func TestExtractTarGz_HappyPath(t *testing.T) {
	t.Parallel()

	archive := createTestTarGz(t, []testTarEntry{
		{Name: "lib/libkrunfw.so.5", Content: []byte("firmware-data"), Mode: 0o644},
		{Name: "README", Content: []byte("readme"), Mode: 0o644},
	})

	dest := t.TempDir()
	require.NoError(t, extractTarGz(archive, dest, 1<<20))

	content, err := os.ReadFile(filepath.Join(dest, "lib", "libkrunfw.so.5"))
	require.NoError(t, err)
	assert.Equal(t, "firmware-data", string(content))

	content, err = os.ReadFile(filepath.Join(dest, "README"))
	require.NoError(t, err)
	assert.Equal(t, "readme", string(content))
}

func TestExtractTarGz_PathTraversal(t *testing.T) {
	t.Parallel()

	archive := createTestTarGz(t, []testTarEntry{
		{Name: "../evil", Content: []byte("pwned"), Mode: 0o644},
	})

	dest := t.TempDir()
	err := extractTarGz(archive, dest, 1<<20)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid firmware entry")
}

func TestExtractTarGz_AbsolutePath(t *testing.T) {
	t.Parallel()

	archive := createTestTarGz(t, []testTarEntry{
		{Name: "/etc/passwd", Content: []byte("root:x:0:0"), Mode: 0o644},
	})

	dest := t.TempDir()
	err := extractTarGz(archive, dest, 1<<20)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid firmware entry")
}

func TestExtractTarGz_SymlinkRejected(t *testing.T) {
	t.Parallel()

	archive := createTestTarGz(t, []testTarEntry{
		{Name: "evil-link", Typeflag: tar.TypeSymlink, Mode: 0o777},
	})

	dest := t.TempDir()
	err := extractTarGz(archive, dest, 1<<20)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported firmware entry type")
}

func TestExtractTarGz_SizeCap(t *testing.T) {
	t.Parallel()

	bigContent := make([]byte, 1024)
	archive := createTestTarGz(t, []testTarEntry{
		{Name: "big.bin", Content: bigContent, Mode: 0o644},
	})

	dest := t.TempDir()
	err := extractTarGz(archive, dest, 512) // cap below content size
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeded")
}

func TestExtractTarGz_CumulativeSizeCap(t *testing.T) {
	t.Parallel()

	// Two files that individually fit but collectively exceed the limit.
	archive := createTestTarGz(t, []testTarEntry{
		{Name: "a.bin", Content: make([]byte, 400), Mode: 0o644},
		{Name: "b.bin", Content: make([]byte, 400), Mode: 0o644},
	})

	dest := t.TempDir()
	err := extractTarGz(archive, dest, 600) // 400+400=800 > 600
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeded")
}

func TestExtractTarGz_EntryCountCap(t *testing.T) {
	t.Parallel()

	// Create an archive with more entries than maxFirmwareEntries.
	entries := make([]testTarEntry, maxFirmwareEntries+1)
	for i := range entries {
		entries[i] = testTarEntry{
			Name:    fmt.Sprintf("file-%d", i),
			Content: nil,
			Mode:    0o644,
		}
	}
	archive := createTestTarGz(t, entries)

	dest := t.TempDir()
	err := extractTarGz(archive, dest, 1<<30)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeded")
	assert.Contains(t, err.Error(), "entries")
}

func TestExtractTarGz_ExecutableMode(t *testing.T) {
	t.Parallel()

	archive := createTestTarGz(t, []testTarEntry{
		{Name: "runner", Content: []byte("bin"), Mode: 0o755},
	})

	dest := t.TempDir()
	require.NoError(t, extractTarGz(archive, dest, 1<<20))

	info, err := os.Stat(filepath.Join(dest, "runner"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())
}

// --- findFirmwareFile tests ---

func TestFindFirmwareFile_TopLevel(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	libPath := filepath.Join(dir, "libkrunfw.so.5")
	require.NoError(t, os.WriteFile(libPath, []byte("fw"), 0o644))

	found, err := findFirmwareFile(dir, "linux")
	require.NoError(t, err)
	assert.Equal(t, libPath, found)
}

func TestFindFirmwareFile_InSubdir(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	subdir := filepath.Join(dir, "lib")
	require.NoError(t, os.MkdirAll(subdir, 0o755))
	libPath := filepath.Join(subdir, "libkrunfw.so.5")
	require.NoError(t, os.WriteFile(libPath, []byte("fw"), 0o644))

	found, err := findFirmwareFile(dir, "linux")
	require.NoError(t, err)
	assert.Equal(t, libPath, found)
}

func TestFindFirmwareFile_NotFound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, err := findFirmwareFile(dir, "linux")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestFindFirmwareFile_Darwin(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	libPath := filepath.Join(dir, "libkrunfw.5.dylib")
	require.NoError(t, os.WriteFile(libPath, []byte("fw"), 0o644))

	found, err := findFirmwareFile(dir, "darwin")
	require.NoError(t, err)
	assert.Equal(t, libPath, found)
}

func TestFindFirmwareFile_PrefixMatch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	libPath := filepath.Join(dir, "libkrunfw.so.4")
	require.NoError(t, os.WriteFile(libPath, []byte("fw"), 0o644))

	found, err := findFirmwareFile(dir, "linux")
	require.NoError(t, err)
	assert.Equal(t, libPath, found)
}

func TestFindFirmwareFile_NonexistentDir(t *testing.T) {
	t.Parallel()

	_, err := findFirmwareFile("/nonexistent/path", "linux")
	require.Error(t, err)
}

// --- findFirmwareInDirs tests ---

func TestFindFirmwareInDirs_Found(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	libPath := filepath.Join(dir, "libkrunfw.so.5")
	require.NoError(t, os.WriteFile(libPath, []byte("fw"), 0o644))

	found, err := findFirmwareInDirs([]string{"/nonexistent", dir}, []string{"libkrunfw.so.5"})
	require.NoError(t, err)
	assert.Equal(t, libPath, found)
}

func TestFindFirmwareInDirs_NotFound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, err := findFirmwareInDirs([]string{dir}, []string{"libkrunfw.so.5"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestFindFirmwareInDirs_EmptyDirs(t *testing.T) {
	t.Parallel()

	_, err := findFirmwareInDirs([]string{"", ""}, []string{"libkrunfw.so.5"})
	require.Error(t, err)
}

func TestFindFirmwareInDirs_SkipsEmpty(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	libPath := filepath.Join(dir, "libkrunfw.so.5")
	require.NoError(t, os.WriteFile(libPath, []byte("fw"), 0o644))

	found, err := findFirmwareInDirs([]string{"", dir}, []string{"libkrunfw.so.5"})
	require.NoError(t, err)
	assert.Equal(t, libPath, found)
}

// --- hashFile tests ---

func TestHashFile_Empty(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "empty")
	require.NoError(t, os.WriteFile(path, nil, 0o644))

	hash, err := hashFile(path)
	require.NoError(t, err)
	// SHA-256 of empty input.
	assert.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", hash)
}

func TestHashFile_KnownContent(t *testing.T) {
	t.Parallel()

	content := []byte("hello")
	path := filepath.Join(t.TempDir(), "hello")
	require.NoError(t, os.WriteFile(path, content, 0o644))

	hash, err := hashFile(path)
	require.NoError(t, err)

	expected := sha256.Sum256(content)
	assert.Equal(t, hex.EncodeToString(expected[:]), hash)
}

func TestHashFile_Nonexistent(t *testing.T) {
	t.Parallel()

	_, err := hashFile("/nonexistent/file")
	require.Error(t, err)
}

// --- safeFileMode tests ---

func TestSafeFileMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mode int64
		want os.FileMode
	}{
		{"executable 755", 0o755, 0o755},
		{"non-executable 644", 0o644, 0o644},
		{"user exec only", 0o100, 0o755},
		{"no exec 600", 0o600, 0o644},
		{"group exec", 0o010, 0o755},
		{"other exec", 0o001, 0o755},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, safeFileMode(tt.mode))
		})
	}
}

// --- ensureSecureCacheRoot tests ---

func TestEnsureSecureCacheRoot_CreatesNew(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "new-cache")
	require.NoError(t, ensureSecureCacheRoot(path))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	assert.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func TestEnsureSecureCacheRoot_ExistingCorrect(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cache")
	require.NoError(t, os.MkdirAll(path, 0o700))
	require.NoError(t, ensureSecureCacheRoot(path))
}

func TestEnsureSecureCacheRoot_TooOpen(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cache")
	require.NoError(t, os.MkdirAll(path, 0o755))

	err := ensureSecureCacheRoot(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permissions too open")
}

func TestEnsureSecureCacheRoot_Symlink(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	target := filepath.Join(base, "real")
	require.NoError(t, os.MkdirAll(target, 0o700))

	link := filepath.Join(base, "link")
	require.NoError(t, os.Symlink(target, link))

	err := ensureSecureCacheRoot(link)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlink")
}

func TestEnsureSecureCacheRoot_NotDir(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(path, []byte("not a dir"), 0o700))

	err := ensureSecureCacheRoot(path)
	require.Error(t, err) // os.MkdirAll fails when the path is a regular file.
}

// --- fetchReleaseAssets tests ---

func TestFetchReleaseAssets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		body    string
		status  int
		wantErr string
		wantLen int
	}{
		{
			name: "happy path",
			body: `{"assets":[
				{"name":"sha256sums.txt","url":"https://api.github.com/repos/o/r/releases/assets/1","browser_download_url":"https://github.com/o/r/releases/download/v1/sha256sums.txt"},
				{"name":"propolis-firmware-linux-amd64.tar.gz","url":"https://api.github.com/repos/o/r/releases/assets/2","browser_download_url":"https://github.com/o/r/releases/download/v1/propolis-firmware-linux-amd64.tar.gz"}
			]}`,
			status:  http.StatusOK,
			wantLen: 2,
		},
		{
			name:    "404",
			body:    `{"message":"Not Found"}`,
			status:  http.StatusNotFound,
			wantErr: "unexpected status",
		},
		{
			name:    "empty assets",
			body:    `{"assets":[]}`,
			status:  http.StatusOK,
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			// Temporarily override the API URL by calling the internal function directly
			// with a test server URL. We test fetchReleaseAssets indirectly via the URL.
			got, err := fetchReleaseAssetsFromURL(t.Context(), srv.URL+"/repos/o/r/releases/tags/v1")
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
				assert.Len(t, got, tt.wantLen)
			}
		})
	}
}

// --- parseChecksumMap tests ---

func TestParseChecksumMap(t *testing.T) {
	t.Parallel()

	validHash1 := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	validHash2 := "b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3"

	tests := []struct {
		name    string
		text    string
		want    map[string]string
		wantErr string
	}{
		{
			name: "valid multi-entry",
			text: validHash1 + "  firmware-linux-amd64.tar.gz\n" + validHash2 + "  firmware-linux-arm64.tar.gz\n",
			want: map[string]string{
				"firmware-linux-amd64.tar.gz": validHash1,
				"firmware-linux-arm64.tar.gz": validHash2,
			},
		},
		{
			name: "empty",
			text: "",
			want: map[string]string{},
		},
		{
			name:    "invalid hex",
			text:    "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz  file.tar.gz\n",
			wantErr: "invalid checksum hex",
		},
		{
			name:    "wrong length",
			text:    "abc123  file.tar.gz\n",
			wantErr: "invalid checksum length",
		},
		{
			name: "trailing whitespace",
			text: validHash1 + "  file.tar.gz  \n",
			want: map[string]string{
				"file.tar.gz": validHash1,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseChecksumMap(tt.text)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// --- downloadChecksums tests ---

func TestDownloadChecksums(t *testing.T) {
	t.Parallel()

	validHash := "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"

	tests := []struct {
		name    string
		body    string
		status  int
		wantErr string
		wantLen int
	}{
		{
			name:    "happy path",
			body:    validHash + "  file1.tar.gz\n" + validHash + "  file2.tar.gz\n",
			status:  http.StatusOK,
			wantLen: 2,
		},
		{
			name:    "404",
			body:    "not found",
			status:  http.StatusNotFound,
			wantErr: "unexpected status",
		},
		{
			name:    "empty body",
			body:    "",
			status:  http.StatusOK,
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			got, err := downloadChecksums(t.Context(), srv.URL)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				require.NoError(t, err)
				assert.Len(t, got, tt.wantLen)
			}
		})
	}
}

// --- manifest round-trip tests ---

func TestWriteReadFirmwareManifest_RoundTrip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "firmware.json")

	ts := time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)
	manifest := FirmwareManifest{
		Version:     "v0.1.0",
		OS:          "linux",
		Arch:        "amd64",
		Source:      firmwareSourceDownload,
		URL:         "https://example.com/fw.tar.gz",
		LibraryHash: "abc123def456",
		Timestamp:   ts,
	}

	require.NoError(t, writeFirmwareManifest(path, manifest))

	// Verify file permissions.
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	got, ok := readFirmwareManifestRaw(path)
	require.True(t, ok)
	assert.Equal(t, manifest, got)
}

func TestReadFirmwareManifestRaw_InvalidJSON(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "bad.json")
	require.NoError(t, os.WriteFile(path, []byte("{invalid"), 0o600))

	_, ok := readFirmwareManifestRaw(path)
	assert.False(t, ok)
}

func TestReadFirmwareManifestRaw_Missing(t *testing.T) {
	t.Parallel()

	_, ok := readFirmwareManifestRaw("/nonexistent/firmware.json")
	assert.False(t, ok)
}

func TestReadFirmwareManifestRaw_LegacyNoHash(t *testing.T) {
	t.Parallel()

	// Simulate a manifest written before LibraryHash was added.
	legacy := map[string]any{
		"version":   "v0.0.1",
		"os":        "linux",
		"arch":      "amd64",
		"source":    "download",
		"url":       "https://example.com/fw.tar.gz",
		"timestamp": time.Now().UTC(),
	}
	data, err := json.Marshal(legacy)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "firmware.json")
	require.NoError(t, os.WriteFile(path, data, 0o600))

	got, ok := readFirmwareManifestRaw(path)
	require.True(t, ok)
	assert.Empty(t, got.LibraryHash, "legacy manifest should have empty LibraryHash")
}

// --- cache-hit Dir resolution test ---

func TestCacheHit_DirResolution(t *testing.T) {
	t.Parallel()

	// Set up a cache with firmware in a subdirectory (like real archives).
	cacheRoot := t.TempDir()
	require.NoError(t, os.Chmod(cacheRoot, 0o700))

	version := "v0.1.0"
	osName := "linux"
	arch := "amd64"
	cacheDir := filepath.Join(cacheRoot, version, fmt.Sprintf("%s-%s", osName, arch))
	libDir := filepath.Join(cacheDir, "lib")
	require.NoError(t, os.MkdirAll(libDir, 0o755))

	fwContent := []byte("firmware-binary-content")
	libPath := filepath.Join(libDir, "libkrunfw.so.5")
	require.NoError(t, os.WriteFile(libPath, fwContent, 0o644))

	expectedHash := sha256.Sum256(fwContent)
	expectedHashStr := hex.EncodeToString(expectedHash[:])

	manifest := FirmwareManifest{
		Version:     version,
		OS:          osName,
		Arch:        arch,
		Source:      firmwareSourceDownload,
		URL:         "https://example.com/fw.tar.gz",
		LibraryHash: expectedHashStr,
		Timestamp:   time.Now().UTC(),
	}
	manifestPath := filepath.Join(cacheDir, "firmware.json")
	require.NoError(t, writeFirmwareManifest(manifestPath, manifest))

	res, err := downloadFirmware(t.Context(), cacheRoot, version, osName, arch)
	require.NoError(t, err)

	// Dir should point to the subdirectory containing the lib, not the cache root.
	assert.Equal(t, libDir, res.Dir)
	assert.Equal(t, firmwareSourceDownload, res.Source)
}

// --- WriteFirmwareReference tests ---

func TestWriteFirmwareReference(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "firmware.ref.json")

	ts := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	ref := FirmwareReference{
		Version:   "v0.1.0",
		Source:    firmwareSourceDownload,
		Path:      "/cache/firmware",
		URL:       "https://example.com/fw.tar.gz",
		Timestamp: ts,
	}

	require.NoError(t, WriteFirmwareReference(path, ref))

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var got FirmwareReference
	require.NoError(t, json.Unmarshal(data, &got))
	assert.Equal(t, ref, got)
}

// --- cache re-verification integration test ---

func TestCacheHit_VerifiesHash(t *testing.T) {
	t.Parallel()

	// Set up a cache directory with a firmware file and manifest.
	cacheDir := t.TempDir()
	libDir := filepath.Join(cacheDir, "lib")
	require.NoError(t, os.MkdirAll(libDir, 0o755))

	fwContent := []byte("firmware-binary-content")
	libPath := filepath.Join(libDir, "libkrunfw.so.5")
	require.NoError(t, os.WriteFile(libPath, fwContent, 0o644))

	expectedHash := sha256.Sum256(fwContent)
	expectedHashStr := hex.EncodeToString(expectedHash[:])

	manifestPath := filepath.Join(cacheDir, "firmware.json")

	// Write manifest with correct hash.
	manifest := FirmwareManifest{
		Version:     "v0.1.0",
		OS:          "linux",
		Arch:        "amd64",
		Source:      firmwareSourceDownload,
		LibraryHash: expectedHashStr,
		Timestamp:   time.Now().UTC(),
	}
	require.NoError(t, writeFirmwareManifest(manifestPath, manifest))

	// Verify cache hit with matching hash.
	m, ok := readFirmwareManifestRaw(manifestPath)
	require.True(t, ok)
	require.NotEmpty(t, m.LibraryHash)

	fwPath, err := findFirmwareFile(cacheDir, "linux")
	require.NoError(t, err)

	fileHash, err := hashFile(fwPath)
	require.NoError(t, err)
	assert.Equal(t, m.LibraryHash, fileHash, "cache hit should pass hash verification")

	// Now tamper with the firmware file and verify hash mismatch.
	require.NoError(t, os.WriteFile(libPath, []byte("tampered"), 0o644))

	tamperedHash, err := hashFile(fwPath)
	require.NoError(t, err)
	assert.NotEqual(t, m.LibraryHash, tamperedHash, "tampered file should fail hash verification")
}

func TestCacheHit_LegacyManifestInvalidates(t *testing.T) {
	t.Parallel()

	// A manifest without LibraryHash should not be trusted.
	manifest := FirmwareManifest{
		Version:   "v0.0.1",
		OS:        "linux",
		Arch:      "amd64",
		Source:    firmwareSourceDownload,
		Timestamp: time.Now().UTC(),
	}

	path := filepath.Join(t.TempDir(), "firmware.json")
	require.NoError(t, writeFirmwareManifest(path, manifest))

	m, ok := readFirmwareManifestRaw(path)
	require.True(t, ok)
	assert.Empty(t, m.LibraryHash, "legacy manifest has no hash — should trigger re-download")
}

// --- firmwareURL test ---

func TestFirmwareURL(t *testing.T) {
	t.Parallel()

	url := firmwareURL("v0.1.0", "linux", "amd64")
	assert.Equal(t, "https://github.com/stacklok/propolis/releases/download/v0.1.0/propolis-firmware-linux-amd64.tar.gz", url)
}

// --- firmwareLibNames tests ---

func TestFirmwareLibNames(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"libkrunfw.so.5"}, firmwareLibNames("linux"))
	assert.Equal(t, []string{"libkrunfw.5.dylib"}, firmwareLibNames("darwin"))
}

// --- systemFirmwareDirs test (smoke) ---

func TestSystemFirmwareDirs_NonEmpty(t *testing.T) {
	t.Parallel()

	dirs := systemFirmwareDirs()
	assert.NotEmpty(t, dirs)
	for _, d := range dirs {
		assert.NotEmpty(t, d)
	}
}

// --- downloadToFile tests ---

func TestDownloadToFile_HappyPath(t *testing.T) {
	t.Parallel()

	content := []byte("test firmware archive content")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	tmpFile, err := os.CreateTemp(t.TempDir(), "dl-*.tar.gz")
	require.NoError(t, err)
	defer func() { _ = tmpFile.Close() }()

	hash, err := downloadToFile(t.Context(), srv.URL, tmpFile, 1<<20)
	require.NoError(t, err)

	expected := sha256.Sum256(content)
	assert.Equal(t, hex.EncodeToString(expected[:]), hash)
}

func TestDownloadToFile_ExceedsMax(t *testing.T) {
	t.Parallel()

	content := make([]byte, 1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(content)
	}))
	defer srv.Close()

	tmpFile, err := os.CreateTemp(t.TempDir(), "dl-*.tar.gz")
	require.NoError(t, err)
	defer func() { _ = tmpFile.Close() }()

	_, err = downloadToFile(t.Context(), srv.URL, tmpFile, 512)
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("exceeded %d bytes", 512))
}

func TestDownloadToFile_HTTPError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	tmpFile, err := os.CreateTemp(t.TempDir(), "dl-*.tar.gz")
	require.NoError(t, err)
	defer func() { _ = tmpFile.Close() }()

	_, err = downloadToFile(t.Context(), srv.URL, tmpFile, 1<<20)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status")
}
