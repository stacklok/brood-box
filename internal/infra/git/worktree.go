// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package git

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/stacklok/brood-box/pkg/domain/workspace"
)

const (
	// gitObjectsTag is the virtiofs tag for the git objects mount.
	gitObjectsTag = "git-objects"

	// gitObjectsGuestPath is where git objects are mounted inside the guest VM.
	gitObjectsGuestPath = "/mnt/git-objects"
)

// Ensure WorktreeProcessor implements workspace.SnapshotPostProcessor at compile time.
var _ workspace.SnapshotPostProcessor = (*WorktreeProcessor)(nil)

// WorktreeProcessor detects git worktrees and reconstructs a proper .git/
// directory in the snapshot with an objects/info/alternates file pointing
// to a guest mount path. The actual git objects directory is exposed as a
// read-only virtiofs mount.
type WorktreeProcessor struct {
	logger *slog.Logger
}

// NewWorktreeProcessor creates a new WorktreeProcessor.
func NewWorktreeProcessor(logger *slog.Logger) *WorktreeProcessor {
	return &WorktreeProcessor{logger: logger}
}

// Process checks whether the snapshot workspace is a git worktree (where
// .git is a file, not a directory). If so, it reconstructs a proper .git/
// directory in the snapshot containing refs, config, HEAD, and an
// objects/info/alternates file that points to the guest mount path.
//
// Returns nil, nil if the workspace is not a worktree or is a normal repo.
func (w *WorktreeProcessor) Process(_ context.Context, originalPath, snapshotPath string) (*workspace.PostProcessResult, error) {
	// Step 1: Check if .git is a regular file (worktree indicator).
	dotGitPath := filepath.Join(snapshotPath, ".git")
	info, err := os.Lstat(dotGitPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("checking .git in snapshot: %w", err)
	}
	if info.IsDir() {
		// Normal repo with .git directory — nothing to do.
		return nil, nil
	}
	if !info.Mode().IsRegular() {
		return nil, nil
	}

	// Step 2: Parse the gitdir pointer from the .git file.
	data, err := os.ReadFile(dotGitPath)
	if err != nil {
		return nil, fmt.Errorf("reading .git file: %w", err)
	}
	content := strings.TrimSpace(string(data))
	if !strings.HasPrefix(content, "gitdir: ") {
		w.logger.Warn("malformed .git file: missing 'gitdir: ' prefix",
			"path", dotGitPath, "content", content)
		return nil, nil
	}
	gitdirPath := strings.TrimPrefix(content, "gitdir: ")

	// Step 3: Resolve gitdir to absolute path (relative paths resolve against originalPath).
	if !filepath.IsAbs(gitdirPath) {
		gitdirPath = filepath.Join(originalPath, gitdirPath)
	}
	gitdirPath = filepath.Clean(gitdirPath)

	// Defense-in-depth: validate the gitdir path looks like a git worktree.
	// Legitimate worktree gitdirs live under <repo>/.git/worktrees/<name>/.
	// Reject paths that don't match this pattern to prevent crafted .git files
	// from pointing to arbitrary host directories.
	if !isWorktreeGitdir(gitdirPath) {
		w.logger.Warn("gitdir path does not appear to be a git worktree, skipping",
			"path", gitdirPath)
		return nil, nil
	}

	// Step 4: Read commondir to find the main repo .git directory.
	mainGitDir := gitdirPath // default: gitdir IS the main .git dir
	commondirData, err := os.ReadFile(filepath.Join(gitdirPath, "commondir"))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("reading commondir: %w", err)
		}
		// No commondir file — gitdir is the main .git dir (rare but valid).
		w.logger.Debug("no commondir file, using gitdir as main git dir",
			"gitdir", gitdirPath)
	} else {
		commondir := strings.TrimSpace(string(commondirData))
		if !filepath.IsAbs(commondir) {
			commondir = filepath.Join(gitdirPath, commondir)
		}
		mainGitDir = filepath.Clean(commondir)
	}

	// Defense-in-depth: verify mainGitDir is an ancestor of gitdirPath.
	// For legitimate worktrees, gitdirPath is always under mainGitDir
	// (e.g. /repo/.git/worktrees/branch lives under /repo/.git/).
	// A crafted commondir could redirect to an arbitrary host directory.
	rel, err := filepath.Rel(mainGitDir, gitdirPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		w.logger.Warn("commondir resolves outside the git directory, skipping worktree reconstruction",
			"mainGitDir", mainGitDir, "gitdirPath", gitdirPath)
		return nil, nil
	}

	// Step 5: Validate that mainGitDir looks like a git directory.
	// Verify HEAD exists before treating the resolved path as a git directory.
	if _, err := os.Stat(filepath.Join(mainGitDir, "HEAD")); err != nil {
		w.logger.Warn("resolved git directory does not contain HEAD, skipping worktree reconstruction",
			"path", mainGitDir, "error", err)
		return nil, nil
	}

	// Step 6: Determine the objects path.
	objectsPath := filepath.Join(mainGitDir, "objects")
	if _, err := os.Stat(objectsPath); err != nil {
		w.logger.Warn("git objects directory not found, skipping worktree reconstruction",
			"path", objectsPath, "error", err)
		return nil, nil
	}

	// Step 7: Reconstruct .git/ directory in the snapshot.
	// 7a: Remove the .git file.
	if err := os.Remove(dotGitPath); err != nil {
		return nil, fmt.Errorf("removing .git file from snapshot: %w", err)
	}

	// 7b: Create .git/ directory.
	if err := os.MkdirAll(dotGitPath, 0o755); err != nil {
		return nil, fmt.Errorf("creating .git directory in snapshot: %w", err)
	}

	// 7c: Copy HEAD and optional state files from the worktree gitdir.
	requiredWorktreeFiles := []string{"HEAD"}
	for _, name := range requiredWorktreeFiles {
		src := filepath.Join(gitdirPath, name)
		dst := filepath.Join(dotGitPath, name)
		if err := copyFile(src, dst); err != nil {
			return nil, fmt.Errorf("copying %s from worktree gitdir: %w", name, err)
		}
	}
	optionalWorktreeFiles := []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG"}
	for _, name := range optionalWorktreeFiles {
		src := filepath.Join(gitdirPath, name)
		dst := filepath.Join(dotGitPath, name)
		if cpErr := copyFile(src, dst); cpErr != nil {
			if errors.Is(cpErr, fs.ErrNotExist) {
				w.logger.Debug("optional worktree file not found, skipping",
					"file", name)
				continue
			}
			return nil, fmt.Errorf("copying %s from worktree gitdir: %w", name, cpErr)
		}
	}

	// 7d: Copy packed-refs and info/exclude from main repo .git/.
	// Config sanitization is handled by ConfigSanitizer (separate post-processor)
	// which owns .git/config for all repo types.
	optionalMainFiles := []string{"packed-refs"}
	for _, name := range optionalMainFiles {
		src := filepath.Join(mainGitDir, name)
		dst := filepath.Join(dotGitPath, name)
		if cpErr := copyFile(src, dst); cpErr != nil {
			if errors.Is(cpErr, fs.ErrNotExist) {
				w.logger.Debug("optional main repo file not found, skipping",
					"file", name)
				continue
			}
			return nil, fmt.Errorf("copying %s from main repo: %w", name, cpErr)
		}
	}

	// info/exclude (optional).
	infoExcludeSrc := filepath.Join(mainGitDir, "info", "exclude")
	infoExcludeDst := filepath.Join(dotGitPath, "info", "exclude")
	if cpErr := copyFile(infoExcludeSrc, infoExcludeDst); cpErr != nil {
		if !errors.Is(cpErr, fs.ErrNotExist) {
			return nil, fmt.Errorf("copying info/exclude from main repo: %w", cpErr)
		}
		w.logger.Debug("info/exclude not found in main repo, skipping")
	}

	// 7e: Copy refs/ from both main repo (shared) and worktree gitdir (worktree-specific).
	// Main repo refs first (shared branches, tags).
	mainRefsDir := filepath.Join(mainGitDir, "refs")
	snapshotRefsDir := filepath.Join(dotGitPath, "refs")
	if err := copyDirRecursive(mainRefsDir, snapshotRefsDir); err != nil {
		return nil, fmt.Errorf("copying refs from main repo: %w", err)
	}

	// Worktree-specific refs override (typically in refs/worktree/, no overlap).
	worktreeRefsDir := filepath.Join(gitdirPath, "refs")
	if err := copyDirRecursive(worktreeRefsDir, snapshotRefsDir); err != nil {
		return nil, fmt.Errorf("copying refs from worktree gitdir: %w", err)
	}

	// 7f: Create objects/ with info/alternates pointing to guest mount.
	objectsInfoDir := filepath.Join(dotGitPath, "objects", "info")
	if err := os.MkdirAll(objectsInfoDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating objects/info directory: %w", err)
	}
	alternatesContent := gitObjectsGuestPath + "\n"
	if err := os.WriteFile(filepath.Join(objectsInfoDir, "alternates"), []byte(alternatesContent), 0o644); err != nil {
		return nil, fmt.Errorf("writing objects/info/alternates: %w", err)
	}

	// 7g: Create empty objects/pack/ directory (git expects it).
	objectsPackDir := filepath.Join(dotGitPath, "objects", "pack")
	if err := os.MkdirAll(objectsPackDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating objects/pack directory: %w", err)
	}

	// Step 8: Return mount request and diff exclusions.
	return &workspace.PostProcessResult{
		Mounts: []workspace.MountRequest{{
			Tag:       gitObjectsTag,
			HostPath:  objectsPath,
			GuestPath: gitObjectsGuestPath,
			ReadOnly:  true,
		}},
		DiffExclude: []string{".git"},
	}, nil
}

// isWorktreeGitdir checks if the path looks like a legitimate git worktree
// gitdir (contains /.git/worktrees/ as a path component).
func isWorktreeGitdir(path string) bool {
	sep := string(filepath.Separator)
	return strings.Contains(path, sep+".git"+sep+"worktrees"+sep)
}

// copyFile reads src and writes it to dst with the same permissions.
// Parent directories of dst are created as needed.
func copyFile(src, dst string) error {
	// Reject symlinks to prevent reading arbitrary host files.
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil // silently skip symlinks
	}

	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}

	perm := info.Mode().Perm()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("creating parent directory for %s: %w", dst, err)
	}

	return os.WriteFile(dst, data, perm)
}

// copyDirRecursive walks src and recreates its structure in dst.
// Only regular files and directories are copied (symlinks are skipped).
// If src does not exist, this is a no-op (returns nil).
func copyDirRecursive(src, dst string) error {
	srcInfo, err := os.Lstat(src)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("lstat %s: %w", src, err)
	}
	if srcInfo.Mode()&os.ModeSymlink != 0 {
		return nil // silently skip symlinked directories
	}
	if !srcInfo.IsDir() {
		return fmt.Errorf("%s is not a directory", src)
	}

	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("computing relative path: %w", err)
		}
		target := filepath.Join(dst, rel)

		// Skip symlinks.
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		if !d.Type().IsRegular() {
			return nil
		}

		return copyFile(path, target)
	})
}
