// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package git

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo creates a git repo at the given path with an initial commit.
func initRepo(t *testing.T, path string) {
	t.Helper()
	run(t, path, "git", "init")
	run(t, path, "git", "config", "user.name", "Test Author")
	run(t, path, "git", "config", "user.email", "test@example.com")
	writeFile(t, filepath.Join(path, "README.md"), "# Test\n")
	run(t, path, "git", "add", "README.md")
	run(t, path, "git", "commit", "-m", "Initial commit")
}

// run executes a command in the given directory.
func run(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed in %s: %v\n%s", name, args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// writeFile writes content to a file, creating parent dirs as needed.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// getHEAD returns the HEAD commit hash of a repo.
func getHEAD(t *testing.T, dir string) string {
	t.Helper()
	return run(t, dir, "git", "rev-parse", "HEAD")
}

// newTestReplayer creates a GitCommitReplayer with a discard logger.
func newTestReplayer() *GitCommitReplayer {
	return NewGitCommitReplayer(slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestReplay_NoNewCommits(t *testing.T) {
	t.Parallel()

	snapshot := t.TempDir()
	original := t.TempDir()

	initRepo(t, snapshot)
	baseRef := getHEAD(t, snapshot)

	initRepo(t, original)

	replayer := newTestReplayer()
	result, err := replayer.Replay(context.Background(), original, snapshot, baseRef, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Replayed != 0 || result.Skipped != 0 {
		t.Errorf("expected 0 replayed/skipped, got %d/%d", result.Replayed, result.Skipped)
	}
}

func TestReplay_EmptyBaseRef(t *testing.T) {
	t.Parallel()

	replayer := newTestReplayer()
	result, err := replayer.Replay(context.Background(), "/tmp", "/tmp", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Replayed != 0 {
		t.Errorf("expected 0 replayed, got %d", result.Replayed)
	}
}

func TestReplay_SingleCommit(t *testing.T) {
	t.Parallel()

	// Set up snapshot repo with an initial commit.
	snapshot := t.TempDir()
	initRepo(t, snapshot)
	baseRef := getHEAD(t, snapshot)

	// Clone snapshot to simulate original (same initial state).
	original := t.TempDir()
	run(t, "", "git", "clone", snapshot, original)
	run(t, original, "git", "config", "user.name", "Test Author")
	run(t, original, "git", "config", "user.email", "test@example.com")

	// Make a commit in the snapshot.
	writeFile(t, filepath.Join(snapshot, "new.txt"), "hello world\n")
	run(t, snapshot, "git", "add", "new.txt")
	run(t, snapshot, "git", "commit", "-m", "Add new file")

	// Also write the file to the original to simulate flush.
	writeFile(t, filepath.Join(original, "new.txt"), "hello world\n")

	replayer := newTestReplayer()
	result, err := replayer.Replay(context.Background(), original, snapshot, baseRef, []string{"new.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Replayed != 1 {
		t.Errorf("expected 1 replayed, got %d", result.Replayed)
	}

	// Verify the commit exists in original.
	logOut := run(t, original, "git", "log", "--oneline")
	if !strings.Contains(logOut, "Add new file") {
		t.Errorf("expected 'Add new file' in git log, got:\n%s", logOut)
	}

	// Verify the file content is correct (restored from flush).
	content, err := os.ReadFile(filepath.Join(original, "new.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello world\n" {
		t.Errorf("expected 'hello world\\n', got %q", content)
	}
}

func TestReplay_MultipleCommits(t *testing.T) {
	t.Parallel()

	snapshot := t.TempDir()
	initRepo(t, snapshot)
	baseRef := getHEAD(t, snapshot)

	original := t.TempDir()
	run(t, "", "git", "clone", snapshot, original)
	run(t, original, "git", "config", "user.name", "Test Author")
	run(t, original, "git", "config", "user.email", "test@example.com")

	// First commit in snapshot.
	writeFile(t, filepath.Join(snapshot, "a.txt"), "aaa\n")
	run(t, snapshot, "git", "add", "a.txt")
	run(t, snapshot, "git", "commit", "-m", "Add a")

	// Second commit in snapshot.
	writeFile(t, filepath.Join(snapshot, "b.txt"), "bbb\n")
	run(t, snapshot, "git", "add", "b.txt")
	run(t, snapshot, "git", "commit", "-m", "Add b")

	// Simulate flush: write final state to original.
	writeFile(t, filepath.Join(original, "a.txt"), "aaa\n")
	writeFile(t, filepath.Join(original, "b.txt"), "bbb\n")

	replayer := newTestReplayer()
	result, err := replayer.Replay(context.Background(), original, snapshot, baseRef, []string{"a.txt", "b.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Replayed != 2 {
		t.Errorf("expected 2 replayed, got %d", result.Replayed)
	}

	// Verify commit order.
	logOut := run(t, original, "git", "log", "--oneline")
	lines := strings.Split(strings.TrimSpace(logOut), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 commits, got:\n%s", logOut)
	}
	// Most recent should be "Add b".
	if !strings.Contains(lines[0], "Add b") {
		t.Errorf("expected first line to contain 'Add b', got %q", lines[0])
	}
	if !strings.Contains(lines[1], "Add a") {
		t.Errorf("expected second line to contain 'Add a', got %q", lines[1])
	}
}

func TestReplay_PartialAcceptance(t *testing.T) {
	t.Parallel()

	snapshot := t.TempDir()
	initRepo(t, snapshot)
	baseRef := getHEAD(t, snapshot)

	original := t.TempDir()
	run(t, "", "git", "clone", snapshot, original)
	run(t, original, "git", "config", "user.name", "Test Author")
	run(t, original, "git", "config", "user.email", "test@example.com")

	// Commit that touches two files.
	writeFile(t, filepath.Join(snapshot, "accepted.txt"), "yes\n")
	writeFile(t, filepath.Join(snapshot, "rejected.txt"), "no\n")
	run(t, snapshot, "git", "add", "accepted.txt", "rejected.txt")
	run(t, snapshot, "git", "commit", "-m", "Add two files")

	// Only flush accepted file.
	writeFile(t, filepath.Join(original, "accepted.txt"), "yes\n")

	replayer := newTestReplayer()
	result, err := replayer.Replay(context.Background(), original, snapshot, baseRef, []string{"accepted.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Replayed != 1 {
		t.Errorf("expected 1 replayed, got %d", result.Replayed)
	}

	// Verify only accepted.txt is in the commit.
	showOut := run(t, original, "git", "diff-tree", "--no-commit-id", "--name-only", "-r", "HEAD")
	if strings.TrimSpace(showOut) != "accepted.txt" {
		t.Errorf("expected only accepted.txt in commit, got %q", showOut)
	}
}

func TestReplay_CommitWithNoAcceptedFiles(t *testing.T) {
	t.Parallel()

	snapshot := t.TempDir()
	initRepo(t, snapshot)
	baseRef := getHEAD(t, snapshot)

	original := t.TempDir()
	run(t, "", "git", "clone", snapshot, original)
	run(t, original, "git", "config", "user.name", "Test Author")
	run(t, original, "git", "config", "user.email", "test@example.com")

	// Add a file that won't be accepted.
	writeFile(t, filepath.Join(snapshot, "rejected.txt"), "no\n")
	run(t, snapshot, "git", "add", "rejected.txt")
	run(t, snapshot, "git", "commit", "-m", "Add rejected file")

	replayer := newTestReplayer()
	result, err := replayer.Replay(context.Background(), original, snapshot, baseRef, []string{"other.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Replayed != 0 {
		t.Errorf("expected 0 replayed, got %d", result.Replayed)
	}
	if result.Skipped != 1 {
		t.Errorf("expected 1 skipped, got %d", result.Skipped)
	}
}

func TestReplay_FileDeletion(t *testing.T) {
	t.Parallel()

	snapshot := t.TempDir()
	initRepo(t, snapshot)

	// Add a file to delete later.
	writeFile(t, filepath.Join(snapshot, "deleteme.txt"), "bye\n")
	run(t, snapshot, "git", "add", "deleteme.txt")
	run(t, snapshot, "git", "commit", "-m", "Add file to delete")
	baseRef := getHEAD(t, snapshot)

	original := t.TempDir()
	run(t, "", "git", "clone", snapshot, original)
	run(t, original, "git", "config", "user.name", "Test Author")
	run(t, original, "git", "config", "user.email", "test@example.com")

	// Delete the file in snapshot.
	run(t, snapshot, "git", "rm", "deleteme.txt")
	run(t, snapshot, "git", "commit", "-m", "Delete file")

	replayer := newTestReplayer()
	result, err := replayer.Replay(context.Background(), original, snapshot, baseRef, []string{"deleteme.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Replayed != 1 {
		t.Errorf("expected 1 replayed, got %d", result.Replayed)
	}
}

func TestReplay_MergeCommitSkipped(t *testing.T) {
	t.Parallel()

	snapshot := t.TempDir()
	initRepo(t, snapshot)
	baseRef := getHEAD(t, snapshot)

	// Create a branch, make a commit, merge.
	defaultBranch := run(t, snapshot, "git", "branch", "--show-current")
	run(t, snapshot, "git", "checkout", "-b", "feature")
	writeFile(t, filepath.Join(snapshot, "feature.txt"), "feat\n")
	run(t, snapshot, "git", "add", "feature.txt")
	run(t, snapshot, "git", "commit", "-m", "Feature commit")

	run(t, snapshot, "git", "checkout", defaultBranch)
	writeFile(t, filepath.Join(snapshot, "main.txt"), "main\n")
	run(t, snapshot, "git", "add", "main.txt")
	run(t, snapshot, "git", "commit", "-m", "Main commit")

	run(t, snapshot, "git", "merge", "feature", "--no-ff", "--no-edit")

	original := t.TempDir()
	initRepo(t, original)

	// Simulate flush.
	writeFile(t, filepath.Join(original, "feature.txt"), "feat\n")
	writeFile(t, filepath.Join(original, "main.txt"), "main\n")

	replayer := newTestReplayer()
	result, err := replayer.Replay(context.Background(), original, snapshot, baseRef, []string{"feature.txt", "main.txt"})
	if err != nil {
		t.Fatal(err)
	}
	// The merge commit should be skipped, but the regular commits should be replayed.
	if result.Skipped < 1 {
		t.Errorf("expected at least 1 skipped (merge commit), got %d", result.Skipped)
	}
}

func TestReplay_PreservesAuthorMetadata(t *testing.T) {
	t.Parallel()

	snapshot := t.TempDir()
	initRepo(t, snapshot)
	baseRef := getHEAD(t, snapshot)

	original := t.TempDir()
	run(t, "", "git", "clone", snapshot, original)
	run(t, original, "git", "config", "user.name", "Test Author")
	run(t, original, "git", "config", "user.email", "test@example.com")

	// Make a commit with specific author.
	writeFile(t, filepath.Join(snapshot, "authored.txt"), "content\n")
	run(t, snapshot, "git", "add", "authored.txt")
	run(t, snapshot, "git", "commit", "--author", "Agent Bot <agent@bot.com>", "-m", "Agent commit")

	writeFile(t, filepath.Join(original, "authored.txt"), "content\n")

	replayer := newTestReplayer()
	_, err := replayer.Replay(context.Background(), original, snapshot, baseRef, []string{"authored.txt"})
	if err != nil {
		t.Fatal(err)
	}

	// Check author of HEAD commit in original.
	author := run(t, original, "git", "log", "-1", "--format=%an <%ae>")
	if author != "Agent Bot <agent@bot.com>" {
		t.Errorf("expected author 'Agent Bot <agent@bot.com>', got %q", author)
	}
}

func TestReplay_UncommittedChangesRestored(t *testing.T) {
	t.Parallel()

	snapshot := t.TempDir()
	initRepo(t, snapshot)
	baseRef := getHEAD(t, snapshot)

	original := t.TempDir()
	run(t, "", "git", "clone", snapshot, original)
	run(t, original, "git", "config", "user.name", "Test Author")
	run(t, original, "git", "config", "user.email", "test@example.com")

	// Make a commit that modifies a file.
	writeFile(t, filepath.Join(snapshot, "file.txt"), "committed version\n")
	run(t, snapshot, "git", "add", "file.txt")
	run(t, snapshot, "git", "commit", "-m", "Add file")

	// Then modify the file further without committing (uncommitted changes).
	writeFile(t, filepath.Join(snapshot, "file.txt"), "uncommitted version\n")

	// Simulate flush: original has the final (uncommitted) state.
	writeFile(t, filepath.Join(original, "file.txt"), "uncommitted version\n")

	replayer := newTestReplayer()
	result, err := replayer.Replay(context.Background(), original, snapshot, baseRef, []string{"file.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Replayed != 1 {
		t.Errorf("expected 1 replayed, got %d", result.Replayed)
	}

	// The working tree should have the uncommitted version restored.
	content, err := os.ReadFile(filepath.Join(original, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "uncommitted version\n" {
		t.Errorf("expected uncommitted version, got %q", content)
	}
}

func TestResolveHEAD_NoCommits(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	run(t, dir, "git", "init")

	replayer := newTestReplayer()
	head, err := replayer.ResolveHEAD(context.Background(), dir)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if head != "" {
		t.Errorf("expected empty HEAD for repo with no commits, got %q", head)
	}
}

func TestReplay_HeadDiverged(t *testing.T) {
	t.Parallel()

	// Create a common bare repo to clone from so both repos share the same initial commit.
	bare := t.TempDir()
	run(t, bare, "git", "init", "--bare")

	snapshot := t.TempDir()
	run(t, "", "git", "clone", bare, snapshot)
	run(t, snapshot, "git", "config", "user.name", "Test Author")
	run(t, snapshot, "git", "config", "user.email", "test@example.com")
	writeFile(t, filepath.Join(snapshot, "README.md"), "# Test\n")
	run(t, snapshot, "git", "add", "README.md")
	run(t, snapshot, "git", "commit", "-m", "Initial commit")
	run(t, snapshot, "git", "push", "-u", "origin", "HEAD")

	baseRef := getHEAD(t, snapshot)

	original := t.TempDir()
	run(t, "", "git", "clone", bare, original)
	run(t, original, "git", "config", "user.name", "Test Author")
	run(t, original, "git", "config", "user.email", "test@example.com")

	// Simulate agent work in snapshot.
	writeFile(t, filepath.Join(snapshot, "agent.txt"), "agent work\n")
	run(t, snapshot, "git", "add", "agent.txt")
	run(t, snapshot, "git", "commit", "-m", "Agent work")

	// Simulate divergence: someone commits to the original while VM was running.
	writeFile(t, filepath.Join(original, "other.txt"), "other work\n")
	run(t, original, "git", "add", "other.txt")
	run(t, original, "git", "commit", "-m", "Concurrent work")

	replayer := newTestReplayer()
	result, err := replayer.Replay(context.Background(), original, snapshot, baseRef, []string{"agent.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Diverged {
		t.Error("expected Diverged=true")
	}
	if result.Replayed != 0 {
		t.Errorf("expected 0 replayed, got %d", result.Replayed)
	}
}

func TestReplay_PreservesExecutableBit(t *testing.T) {
	t.Parallel()

	snapshot := t.TempDir()
	initRepo(t, snapshot)
	baseRef := getHEAD(t, snapshot)

	original := t.TempDir()
	run(t, "", "git", "clone", snapshot, original)
	run(t, original, "git", "config", "user.name", "Test Author")
	run(t, original, "git", "config", "user.email", "test@example.com")

	// Create an executable file in the snapshot.
	scriptPath := filepath.Join(snapshot, "script.sh")
	writeFile(t, scriptPath, "#!/bin/sh\necho hello\n")
	if err := os.Chmod(scriptPath, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, snapshot, "git", "add", "script.sh")
	run(t, snapshot, "git", "update-index", "--chmod=+x", "script.sh")
	run(t, snapshot, "git", "commit", "-m", "Add executable script")

	// Simulate flush.
	writeFile(t, filepath.Join(original, "script.sh"), "#!/bin/sh\necho hello\n")

	replayer := newTestReplayer()
	result, err := replayer.Replay(context.Background(), original, snapshot, baseRef, []string{"script.sh"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Replayed != 1 {
		t.Errorf("expected 1 replayed, got %d", result.Replayed)
	}

	// Verify the file has executable permission.
	info, err := os.Stat(filepath.Join(original, "script.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("expected executable bit set, got mode %o", info.Mode().Perm())
	}
}

func TestGetFileModeAtCommit(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	initRepo(t, repo)

	// Add a regular file and an executable file.
	writeFile(t, filepath.Join(repo, "regular.txt"), "content\n")
	run(t, repo, "git", "add", "regular.txt")

	writeFile(t, filepath.Join(repo, "exec.sh"), "#!/bin/sh\n")
	run(t, repo, "git", "add", "exec.sh")
	run(t, repo, "git", "update-index", "--chmod=+x", "exec.sh")

	run(t, repo, "git", "commit", "-m", "Add files")
	hash := getHEAD(t, repo)

	replayer := newTestReplayer()

	tests := []struct {
		name     string
		path     string
		wantMode os.FileMode
	}{
		{"regular file", "regular.txt", 0o644},
		{"executable file", "exec.sh", 0o755},
		{"nonexistent file", "nosuchfile.txt", 0o644},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mode := replayer.getFileModeAtCommit(context.Background(), repo, hash, tt.path)
			if mode != tt.wantMode {
				t.Errorf("getFileModeAtCommit(%q) = %o, want %o", tt.path, mode, tt.wantMode)
			}
		})
	}
}

func TestValidatePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"valid relative", "src/main.go", false},
		{"valid simple", "file.txt", false},
		{"empty", "", true},
		{"absolute", "/etc/passwd", true},
		{"parent traversal", "../escape", true},
		{"deep traversal", "foo/../../escape", true},
		{"dot-git exact", ".git", true},
		{"dot-git subpath", ".git/hooks/pre-commit", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validatePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("validatePath(%q) err=%v, wantErr=%v", tt.path, err, tt.wantErr)
			}
		})
	}
}
