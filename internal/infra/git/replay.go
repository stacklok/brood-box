// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package git

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	infraws "github.com/stacklok/brood-box/internal/infra/workspace"
	domaingit "github.com/stacklok/brood-box/pkg/domain/git"
)

// Compile-time interface check.
var _ domaingit.CommitReplayer = (*GitCommitReplayer)(nil)

// GitCommitReplayer replays commits from a snapshot repository onto the
// original workspace using git commands.
type GitCommitReplayer struct {
	logger *slog.Logger
}

// NewGitCommitReplayer creates a new GitCommitReplayer.
func NewGitCommitReplayer(logger *slog.Logger) *GitCommitReplayer {
	return &GitCommitReplayer{logger: logger}
}

// commitMeta holds metadata extracted from a single commit.
type commitMeta struct {
	AuthorName  string
	AuthorEmail string
	AuthorDate  string
	Message     string
}

// fileChange represents a single file change in a commit.
type fileChange struct {
	Status string // "A", "M", "D", "R", "C", etc.
	Path   string
}

// ResolveHEAD returns the current HEAD commit hash for the repo at the given path.
// Returns an empty string (not an error) if the repo has no commits or is not a git repository.
func (r *GitCommitReplayer) ResolveHEAD(ctx context.Context, repoPath string) (string, error) {
	out, err := r.gitOutput(ctx, repoPath, "rev-parse", "HEAD")
	if err != nil {
		// No commits or not a git repo — return empty per interface contract.
		r.logger.Debug("could not resolve HEAD", "path", repoPath, "error", err)
		return "", nil
	}
	return out, nil
}

// Replay extracts new commits from the snapshot's git history and recreates
// them in the original repo with the same metadata. Only files in the
// accepted set are included.
func (r *GitCommitReplayer) Replay(
	ctx context.Context, originalPath, snapshotPath, baseRef string, accepted []string,
) (*domaingit.ReplayResult, error) {
	result := &domaingit.ReplayResult{}

	// Early exit: no base ref means no initial HEAD (e.g. worktree case).
	if baseRef == "" {
		r.logger.Debug("no base ref, skipping commit replay")
		return result, nil
	}

	// Get current snapshot HEAD.
	snapshotHEAD, err := r.gitOutput(ctx, snapshotPath, "rev-parse", "HEAD")
	if err != nil {
		return result, fmt.Errorf("resolving snapshot HEAD: %w", err)
	}

	// No new commits.
	if snapshotHEAD == baseRef {
		r.logger.Debug("no new commits in snapshot")
		return result, nil
	}

	// Guard: ensure the original repo's HEAD hasn't moved since snapshot creation.
	originalHEAD, origErr := r.gitOutput(ctx, originalPath, "rev-parse", "HEAD")
	if origErr != nil {
		r.logger.Warn("could not resolve original HEAD, skipping replay", "error", origErr)
		result.Diverged = true
		return result, nil
	}
	if originalHEAD != baseRef {
		r.logger.Warn("original repo HEAD has diverged, skipping commit replay",
			"original_head", originalHEAD, "base_ref", baseRef)
		result.Diverged = true
		return result, nil
	}

	// List new commits oldest-first.
	commits, err := r.listNewCommits(ctx, snapshotPath, baseRef)
	if err != nil {
		return result, fmt.Errorf("listing new commits: %w", err)
	}

	if len(commits) == 0 {
		return result, nil
	}

	// Build accepted set for O(1) lookup.
	acceptedSet := make(map[string]bool, len(accepted))
	for _, p := range accepted {
		acceptedSet[p] = true
	}

	r.logger.Debug("replaying commits", "count", len(commits), "accepted_files", len(accepted))

	for _, hash := range commits {
		replayed, err := r.replayCommit(ctx, originalPath, snapshotPath, hash, acceptedSet)
		if err != nil {
			r.logger.Warn("failed to replay commit, continuing", "hash", hash, "error", err)
			result.Skipped++
			continue
		}
		if replayed {
			result.Replayed++
		} else {
			result.Skipped++
		}
	}

	// Restore uncommitted state: after replay, the working tree in originalPath
	// reflects the last replayed commit. We need to restore any files that were
	// flushed but not part of any commit (uncommitted changes from the snapshot).
	if err := r.restoreUncommittedChanges(ctx, originalPath, snapshotPath, acceptedSet); err != nil {
		r.logger.Warn("failed to restore uncommitted changes after replay", "error", err)
	}

	return result, nil
}

// replayCommit recreates a single commit in the original repo.
// Returns true if the commit was replayed, false if skipped.
func (r *GitCommitReplayer) replayCommit(
	ctx context.Context, originalPath, snapshotPath, hash string, acceptedSet map[string]bool,
) (bool, error) {
	// Skip merge commits (more than one parent).
	parents, err := r.gitOutput(ctx, snapshotPath, "rev-list", "--parents", "-1", hash)
	if err != nil {
		return false, fmt.Errorf("checking parents: %w", err)
	}
	parentParts := strings.Fields(parents)
	if len(parentParts) > 2 { // first field is the commit itself
		r.logger.Debug("skipping merge commit", "hash", hash)
		return false, nil
	}

	// Get commit metadata.
	meta, err := r.getCommitMeta(ctx, snapshotPath, hash)
	if err != nil {
		return false, fmt.Errorf("getting commit metadata: %w", err)
	}

	// Get changed files.
	changes, err := r.getCommitFiles(ctx, snapshotPath, hash)
	if err != nil {
		return false, fmt.Errorf("getting commit files: %w", err)
	}

	// Filter to accepted files only.
	var acceptedChanges []fileChange
	for _, fc := range changes {
		if acceptedSet[fc.Path] {
			acceptedChanges = append(acceptedChanges, fc)
		}
	}

	if len(acceptedChanges) == 0 {
		r.logger.Debug("skipping commit with no accepted files", "hash", hash)
		return false, nil
	}

	if len(acceptedChanges) < len(changes) {
		r.logger.Warn("replaying partial commit (some files were rejected)",
			"hash", hash, "total", len(changes), "accepted", len(acceptedChanges))
	}

	// Stage each accepted file change.
	for _, fc := range acceptedChanges {
		if err := r.stageFileChange(ctx, originalPath, snapshotPath, hash, fc); err != nil {
			return false, fmt.Errorf("staging %s (%s): %w", fc.Path, fc.Status, err)
		}
	}

	// Check if there are any staged changes before committing.
	// This avoids creating misleading empty commits when the content
	// already matches (e.g. the flush already wrote the file).
	_, diffErr := r.gitOutput(ctx, originalPath, "diff", "--cached", "--quiet")
	if diffErr == nil {
		// No staged changes — skip this commit.
		r.logger.Debug("skipping commit with no effective changes after staging", "hash", hash)
		return false, nil
	}

	// Create commit with original metadata.
	author := fmt.Sprintf("%s <%s>", meta.AuthorName, meta.AuthorEmail)
	_, err = r.gitOutput(ctx, originalPath,
		"commit",
		"--author", author,
		"--date", meta.AuthorDate,
		"-m", meta.Message,
	)
	if err != nil {
		return false, fmt.Errorf("creating commit: %w", err)
	}

	r.logger.Debug("replayed commit", "hash", hash, "files", len(acceptedChanges))
	return true, nil
}

// stageFileChange stages a single file change in the original repo.
func (r *GitCommitReplayer) stageFileChange(
	ctx context.Context, originalPath, snapshotPath, hash string, fc fileChange,
) error {
	// Validate path: must not escape workspace.
	if err := validatePath(fc.Path); err != nil {
		return err
	}

	switch fc.Status {
	case "D":
		// File was deleted — remove from index if it exists.
		_, err := r.gitOutput(ctx, originalPath, "rm", "--cached", "--ignore-unmatch", "--", fc.Path)
		return err

	default:
		// Added or modified — extract content from snapshot commit and write to original.
		content, err := r.getFileAtCommit(ctx, snapshotPath, hash, fc.Path)
		if err != nil {
			return fmt.Errorf("reading file content: %w", err)
		}

		destPath := filepath.Join(originalPath, fc.Path)

		// Symlink-aware bounds check (matches flusher's defense-in-depth).
		if err := infraws.ValidateInBounds(originalPath, destPath); err != nil {
			return fmt.Errorf("path traversal rejected for %s: %w", fc.Path, err)
		}

		// Ensure parent directory exists.
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return fmt.Errorf("creating parent dir: %w", err)
		}

		fileMode := r.getFileModeAtCommit(ctx, snapshotPath, hash, fc.Path)
		if err := os.WriteFile(destPath, content, fileMode); err != nil {
			return fmt.Errorf("writing file: %w", err)
		}
		// WriteFile only applies mode on creation; chmod ensures correct
		// permissions when the file already exists (e.g. from flush).
		if err := os.Chmod(destPath, fileMode); err != nil {
			return fmt.Errorf("setting file mode: %w", err)
		}

		_, err = r.gitOutput(ctx, originalPath, "add", "--", fc.Path)
		return err
	}
}

// restoreUncommittedChanges copies the final working tree state from the
// snapshot for any accepted files, undoing intermediate states written
// during commit replay.
func (r *GitCommitReplayer) restoreUncommittedChanges(
	ctx context.Context, originalPath, snapshotPath string, acceptedSet map[string]bool,
) error {
	for path := range acceptedSet {
		if err := validatePath(path); err != nil {
			continue
		}

		srcPath := filepath.Join(snapshotPath, path)
		destPath := filepath.Join(originalPath, path)

		// Symlink-aware bounds check.
		if err := infraws.ValidateInBounds(originalPath, destPath); err != nil {
			r.logger.Warn("skipping file with out-of-bounds path", "path", path, "error", err)
			continue
		}

		srcInfo, err := os.Lstat(srcPath)
		if err != nil {
			if os.IsNotExist(err) {
				// File was deleted in snapshot — ensure it's deleted in original too.
				_ = os.Remove(destPath)
				continue
			}
			return fmt.Errorf("stat %s in snapshot: %w", path, err)
		}

		// Skip non-regular files.
		if !srcInfo.Mode().IsRegular() {
			continue
		}

		content, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("reading %s from snapshot: %w", path, err)
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return fmt.Errorf("creating parent dir for %s: %w", path, err)
		}

		if err := os.WriteFile(destPath, content, srcInfo.Mode().Perm()); err != nil {
			return fmt.Errorf("writing %s to original: %w", path, err)
		}
	}

	// Reset the index to match the working tree so we don't leave staged changes.
	_, err := r.gitOutput(ctx, originalPath, "reset")
	if err != nil {
		r.logger.Debug("failed to reset index after restoring uncommitted changes", "error", err)
	}

	return nil
}

// listNewCommits returns commit hashes between baseRef and HEAD, oldest first.
func (r *GitCommitReplayer) listNewCommits(ctx context.Context, repoPath, baseRef string) ([]string, error) {
	out, err := r.gitOutput(ctx, repoPath, "rev-list", "--reverse", baseRef+"..HEAD")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// getCommitMeta extracts author name, email, date, and message from a commit.
func (r *GitCommitReplayer) getCommitMeta(ctx context.Context, repoPath, hash string) (commitMeta, error) {
	// Format: author name, author email, author date (ISO), commit message body
	out, err := r.gitOutput(ctx, repoPath, "log", "-1", "--format=%an%n%ae%n%aI%n%B", hash)
	if err != nil {
		return commitMeta{}, err
	}

	lines := strings.SplitN(out, "\n", 4)
	if len(lines) < 4 {
		return commitMeta{}, fmt.Errorf("unexpected log format for %s: got %d lines", hash, len(lines))
	}

	return commitMeta{
		AuthorName:  lines[0],
		AuthorEmail: lines[1],
		AuthorDate:  lines[2],
		Message:     strings.TrimRight(lines[3], "\n"),
	}, nil
}

// getCommitFiles returns the list of changed files in a commit.
func (r *GitCommitReplayer) getCommitFiles(ctx context.Context, repoPath, hash string) ([]fileChange, error) {
	out, err := r.gitOutput(ctx, repoPath, "diff-tree", "--no-commit-id", "--name-status", "-r", hash)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}

	var changes []fileChange
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		status := parts[0]
		path := parts[1]

		// For rename/copy, diff-tree outputs "R100\told\tnew".
		// Emit two changes: delete old path + add new path for renames,
		// or just add new path for copies.
		if strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C") {
			paths := strings.SplitN(parts[1], "\t", 2)
			if len(paths) == 2 {
				if strings.HasPrefix(status, "R") {
					// Rename: delete old path.
					changes = append(changes, fileChange{Status: "D", Path: paths[0]})
				}
				// Add new path.
				changes = append(changes, fileChange{Status: "A", Path: paths[1]})
				continue
			}
		}

		changes = append(changes, fileChange{Status: status, Path: path})
	}

	return changes, nil
}

// getFileModeAtCommit returns the file permission for a path at a specific commit.
// Uses git ls-tree to query the tree mode. Returns 0o755 for executable files
// (mode 100755), 0o644 for everything else. Falls back to 0o644 on failure.
func (r *GitCommitReplayer) getFileModeAtCommit(
	ctx context.Context, repoPath, hash, path string,
) os.FileMode {
	out, err := r.gitOutput(ctx, repoPath, "ls-tree", hash, "--", path)
	if err != nil {
		r.logger.Debug("could not query file mode, using default",
			"hash", hash, "path", path, "error", err)
		return 0o644
	}
	// Output: "<mode> <type> <hash>\t<path>"
	fields := strings.Fields(out)
	if len(fields) >= 1 && fields[0] == "100755" {
		return 0o755
	}
	return 0o644
}

// getFileAtCommit extracts file content at a specific commit.
func (r *GitCommitReplayer) getFileAtCommit(ctx context.Context, repoPath, hash, path string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "show", hash+":"+path)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git show %s:%s: %w: %s", hash, path, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

// gitOutput runs a git command and returns trimmed stdout.
func (r *GitCommitReplayer) gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}

// validatePath rejects paths that could escape the workspace.
func validatePath(path string) error {
	if path == "" {
		return fmt.Errorf("empty path")
	}
	if filepath.IsAbs(path) {
		return fmt.Errorf("absolute path not allowed: %s", path)
	}
	cleaned := filepath.Clean(path)
	if strings.HasPrefix(cleaned, "..") {
		return fmt.Errorf("path traversal not allowed: %s", path)
	}
	// Block .git directory manipulation.
	if cleaned == ".git" || strings.HasPrefix(cleaned, ".git/") || strings.HasPrefix(cleaned, ".git\\") {
		return fmt.Errorf("path inside .git not allowed: %s", path)
	}
	return nil
}
