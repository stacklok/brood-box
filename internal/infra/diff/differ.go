// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package diff provides a SHA-256 based file diff engine for comparing
// workspace snapshots.
package diff

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sergi/go-diff/diffmatchpatch"

	"github.com/stacklok/sandbox-agent/internal/domain/snapshot"
	"github.com/stacklok/sandbox-agent/internal/infra/exclude"
)

// Differ computes the differences between an original workspace and its snapshot.
type Differ interface {
	// Diff returns the list of file changes between originalDir and snapshotDir.
	// The matcher is used to skip excluded files (which shouldn't appear in the
	// snapshot but we check defensively).
	Diff(originalDir, snapshotDir string, matcher exclude.Matcher) ([]snapshot.FileChange, error)
}

// FSDiffer implements Differ by walking both directories and comparing SHA-256 hashes.
type FSDiffer struct{}

// NewFSDiffer creates a new filesystem-based differ.
func NewFSDiffer() *FSDiffer {
	return &FSDiffer{}
}

// fileEntry holds the hash and path info for a single file.
type fileEntry struct {
	hash    string
	absPath string
}

// Diff walks both directories and produces a sorted list of FileChange.
func (d *FSDiffer) Diff(originalDir, snapshotDir string, matcher exclude.Matcher) ([]snapshot.FileChange, error) {
	origIndex, err := buildIndex(originalDir, matcher)
	if err != nil {
		return nil, fmt.Errorf("indexing original directory: %w", err)
	}

	snapIndex, err := buildIndex(snapshotDir, nil)
	if err != nil {
		return nil, fmt.Errorf("indexing snapshot directory: %w", err)
	}

	var changes []snapshot.FileChange

	// Find modified and deleted files.
	for relPath, origEntry := range origIndex {
		snapEntry, exists := snapIndex[relPath]
		if !exists {
			changes = append(changes, snapshot.FileChange{
				RelPath: relPath,
				Kind:    snapshot.Deleted,
			})
			continue
		}

		if origEntry.hash != snapEntry.hash {
			diff, err := computeDiff(origEntry.absPath, snapEntry.absPath, relPath)
			if err != nil {
				return nil, fmt.Errorf("computing diff for %s: %w", relPath, err)
			}
			changes = append(changes, snapshot.FileChange{
				RelPath:     relPath,
				Kind:        snapshot.Modified,
				UnifiedDiff: diff,
				Hash:        snapEntry.hash,
			})
		}
	}

	// Find added files.
	for relPath, snapEntry := range snapIndex {
		if _, exists := origIndex[relPath]; !exists {
			diff, err := computeAddedDiff(snapEntry.absPath, relPath)
			if err != nil {
				return nil, fmt.Errorf("computing diff for added file %s: %w", relPath, err)
			}
			changes = append(changes, snapshot.FileChange{
				RelPath:     relPath,
				Kind:        snapshot.Added,
				UnifiedDiff: diff,
				Hash:        snapEntry.hash,
			})
		}
	}

	// Sort by path for deterministic output.
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].RelPath < changes[j].RelPath
	})

	return changes, nil
}

// buildIndex walks a directory and builds a map of relPath -> fileEntry.
// If matcher is non-nil, excluded paths are skipped.
func buildIndex(root string, matcher exclude.Matcher) (map[string]fileEntry, error) {
	index := make(map[string]fileEntry)

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		relPath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}

		if d.IsDir() {
			if matcher != nil && (matcher.Match(relPath) || matcher.Match(relPath+"/")) {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip non-regular files (symlinks, etc.).
		if !d.Type().IsRegular() {
			return nil
		}

		if matcher != nil && matcher.Match(relPath) {
			return nil
		}

		hash, err := hashFile(path)
		if err != nil {
			return fmt.Errorf("hashing %s: %w", relPath, err)
		}

		index[relPath] = fileEntry{
			hash:    hash,
			absPath: path,
		}

		return nil
	})

	return index, err
}

// hashFile computes the SHA-256 hex digest of a file.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// HashFile computes the SHA-256 hex digest of a file. Exported for use by
// the flusher to re-verify hashes before copying.
func HashFile(path string) (string, error) {
	return hashFile(path)
}

// isBinary checks if data contains null bytes in the first 8KB, indicating
// a binary file.
func isBinary(data []byte) bool {
	limit := 8192
	if len(data) < limit {
		limit = len(data)
	}
	return bytes.Contains(data[:limit], []byte{0})
}

// computeDiff generates a unified diff between two files.
func computeDiff(origPath, snapPath, relPath string) (string, error) {
	origData, err := os.ReadFile(origPath)
	if err != nil {
		return "", err
	}
	snapData, err := os.ReadFile(snapPath)
	if err != nil {
		return "", err
	}

	if isBinary(origData) || isBinary(snapData) {
		return "Binary file differs", nil
	}

	dmp := diffmatchpatch.New()
	a, b, c := dmp.DiffLinesToChars(string(origData), string(snapData))
	diffs := dmp.DiffMain(a, b, false)
	diffs = dmp.DiffCharsToLines(diffs, c)
	patches := dmp.PatchMake(string(origData), diffs)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("--- a/%s\n+++ b/%s\n", relPath, relPath))
	for _, p := range patches {
		sb.WriteString(p.String())
	}

	return sb.String(), nil
}

// computeAddedDiff generates a diff showing a new file.
func computeAddedDiff(snapPath, relPath string) (string, error) {
	data, err := os.ReadFile(snapPath)
	if err != nil {
		return "", err
	}

	if isBinary(data) {
		return "Binary file differs", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("--- /dev/null\n+++ b/%s\n", relPath))
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		sb.WriteString("+" + line + "\n")
	}

	return sb.String(), nil
}
