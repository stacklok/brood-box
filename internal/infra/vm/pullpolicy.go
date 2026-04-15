// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/stacklok/go-microvm/image"

	"github.com/stacklok/brood-box/pkg/domain/config"
)

// backgroundRefreshTimeout is the maximum time allowed for a background
// image refresh. This covers the registry manifest fetch and, if the
// image changed, layer extraction and caching.
const backgroundRefreshTimeout = 5 * time.Minute

// neverPullFetcher implements image.ImageFetcher and always returns an error.
// Used with PullNever: if the cache ref index misses, this fetcher prevents
// any network access and returns a clear error message.
type neverPullFetcher struct{}

func (neverPullFetcher) Pull(_ context.Context, ref string) (v1.Image, error) {
	return nil, fmt.Errorf("image %q not found in cache and pull policy is %q", ref, config.PullNever)
}

// deleteRefIndex removes the ref index entry for the given image reference
// from the OCI image cache. This forces the next pull to contact the registry
// for a fresh digest, while the digest-based cache still avoids re-extraction
// if the image hasn't changed.
//
// The ref index stores files at {cacheDir}/refs/{sha256(imageRef)}, matching
// the go-microvm image.Cache.refPath() convention.
func deleteRefIndex(cacheDir, imageRef string) error {
	h := sha256.Sum256([]byte(imageRef))
	refPath := filepath.Join(cacheDir, "refs", hex.EncodeToString(h[:]))
	if err := os.Remove(refPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing ref index entry for %q: %w", imageRef, err)
	}
	return nil
}

// backgroundImageRefresh checks the registry for a newer image and caches
// it for the next run. The current VM continues using its existing rootfs.
// This is a fire-and-forget operation — errors are logged but do not
// affect the running session.
func backgroundImageRefresh(imageCacheDir, imageRef string, logger *slog.Logger) {
	ctx, cancel := context.WithTimeout(context.Background(), backgroundRefreshTimeout)
	defer cancel()

	// Delete the ref index entry so PullWithFetcher contacts the registry
	// instead of short-circuiting on the cached ref.
	if err := deleteRefIndex(imageCacheDir, imageRef); err != nil {
		logger.Debug("background image refresh: failed to clear ref index", "error", err)
		return
	}

	cache := image.NewCache(imageCacheDir)
	rootfs, err := image.PullWithFetcher(ctx, imageRef, cache, nil)
	if err != nil {
		logger.Debug("background image refresh failed", "image", imageRef, "error", err)
		return
	}

	if rootfs.FromCache {
		logger.Debug("background image refresh: image unchanged", "image", imageRef)
	} else {
		logger.Info("background image refresh: cached newer image for next run", "image", imageRef)
	}
}
