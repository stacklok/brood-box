// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/brood-box/pkg/domain/config"
)

func TestNeverPullFetcher_ReturnsError(t *testing.T) {
	t.Parallel()

	f := neverPullFetcher{}
	img, err := f.Pull(context.Background(), "ghcr.io/org/image:latest")

	assert.Nil(t, img)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found in cache")
	assert.Contains(t, err.Error(), config.PullNever)
	assert.Contains(t, err.Error(), "ghcr.io/org/image:latest")
}

func TestDeleteRefIndex_RemovesExistingRef(t *testing.T) {
	t.Parallel()

	cacheDir := t.TempDir()
	refsDir := filepath.Join(cacheDir, "refs")
	require.NoError(t, os.MkdirAll(refsDir, 0o700))

	imageRef := "ghcr.io/org/image:latest"
	h := sha256.Sum256([]byte(imageRef))
	refFile := filepath.Join(refsDir, hex.EncodeToString(h[:]))
	require.NoError(t, os.WriteFile(refFile, []byte(imageRef+"\tsha256:abc123\n"), 0o600))

	err := deleteRefIndex(cacheDir, imageRef)
	require.NoError(t, err)

	_, statErr := os.Stat(refFile)
	assert.True(t, os.IsNotExist(statErr))
}

func TestDeleteRefIndex_NoErrorWhenMissing(t *testing.T) {
	t.Parallel()

	cacheDir := t.TempDir()
	err := deleteRefIndex(cacheDir, "ghcr.io/org/nonexistent:latest")
	assert.NoError(t, err)
}

func TestDeleteRefIndex_NoErrorWhenRefsDirectoryMissing(t *testing.T) {
	t.Parallel()

	cacheDir := t.TempDir()
	// Don't create refs/ subdirectory.
	err := deleteRefIndex(cacheDir, "ghcr.io/org/image:latest")
	assert.NoError(t, err)
}
