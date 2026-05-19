// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

// Some tests require the test process to be a member of a supplementary
// group distinct from its effective gid: chown to a non-default gid is
// the only way a non-root process can observably change ownership. Tests
// requiring this t.Skip when the requirement isn't met. CI runs as a
// user with multiple groups (e.g. GitHub Actions' `runner`) so this
// works there; local runs in barebones containers may need
// `--group-add 100` or similar.
package homefs

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// discardLogger returns a slog.Logger that drops everything.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// statGID returns the gid of a path. The fix only changes gid in tests
// because non-root processes cannot set arbitrary uids, but can set the
// gid to any group the process belongs to.
func statGID(t *testing.T, path string) uint32 {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	return info.Sys().(*syscall.Stat_t).Gid
}

// chownableGIDs returns two distinct gids the running process is permitted
// to chown to: its effective gid and one of its supplementary groups. If
// the process has no supplementary group distinct from its egid, the
// caller should t.Skip.
func chownableGIDs(t *testing.T) (egid, otherGID int, ok bool) {
	t.Helper()
	egid = os.Getegid()
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatalf("getgroups: %v", err)
	}
	for _, g := range groups {
		if g != egid {
			return egid, g, true
		}
	}
	return egid, 0, false
}

func TestChownRecursive_WalksEntireTree(t *testing.T) {
	// Pre-chown every file to a non-target gid, then run chownRecursive
	// to bring them to the target gid. If any file ends up with the
	// pre-chown gid, the walk skipped it. This is the assertion the
	// original tautological version was missing.
	target, other, ok := chownableGIDs(t)
	if !ok {
		t.Skip("test process has no supplementary group distinct from egid; cannot vary gid")
	}

	root := t.TempDir()
	tree := []string{
		"a/b/c.txt",
		"a/b/d.txt",
		"a/e.txt",
		"f.txt",
	}
	for _, rel := range tree {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir parent of %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		if err := os.Lchown(full, os.Geteuid(), other); err != nil {
			t.Fatalf("pre-chown %s to gid %d: %v", rel, other, err)
		}
	}

	chownRecursive(root, os.Geteuid(), target, discardLogger())

	for _, rel := range tree {
		if got := statGID(t, filepath.Join(root, rel)); int(got) != target {
			t.Errorf("%s: gid=%d, want %d (walk did not visit, or chown failed)", rel, got, target)
		}
	}
}

func TestChownRecursive_ContinuesPastUnreadableSubdir(t *testing.T) {
	// A subdirectory we cannot read should not abort the walk. The
	// assertion is "a sibling that sorts after the unreadable dir was
	// still visited", which proves continuation.
	if os.Geteuid() == 0 {
		t.Skip("running as root sees everything; cannot create unreadable dir")
	}
	target, other, ok := chownableGIDs(t)
	if !ok {
		t.Skip("test process has no supplementary group distinct from egid; cannot vary gid")
	}

	root := t.TempDir()

	// "bad/" — mode 0000, so ReadDir on it fails.
	bad := filepath.Join(root, "bad")
	if err := os.Mkdir(bad, 0o000); err != nil {
		t.Fatalf("mkdir bad: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(bad, 0o700) })

	// "good.txt" — sorts lexically after "bad", so reaching it requires
	// the walk to continue past the bad dir's ReadDir failure.
	good := filepath.Join(root, "good.txt")
	if err := os.WriteFile(good, []byte("x"), 0o600); err != nil {
		t.Fatalf("write good: %v", err)
	}
	if err := os.Lchown(good, os.Geteuid(), other); err != nil {
		t.Fatalf("pre-chown good: %v", err)
	}

	chownRecursive(root, os.Geteuid(), target, discardLogger())

	if got := statGID(t, good); int(got) != target {
		t.Errorf("good.txt gid=%d, want %d (walk aborted at 'bad/' instead of continuing)", got, target)
	}
}

func TestChownRecursive_NonexistentRootDoesNotPanic(t *testing.T) {
	// Best-effort semantics: missing root logs a warning, doesn't panic.
	chownRecursive("/this/path/does/not/exist", 1000, 1000, discardLogger())
}

func TestChownRecursive_HandlesSymlinks(t *testing.T) {
	// Lchown operates on the symlink itself, not its target. Confirm the
	// symlink survives as a symlink after chownRecursive runs.
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatalf("write target: %v", err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink("target.txt", link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	chownRecursive(root, os.Geteuid(), os.Getegid(), discardLogger())

	info, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat link after chown: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("link is no longer a symlink — chown may have followed it")
	}
}

func TestCopyTree_PreservesStructure(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	if err := os.MkdirAll(filepath.Join(src, "a/b"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "a/b/c.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("write c.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, "top.txt"), []byte("top"), 0o600); err != nil {
		t.Fatalf("write top.txt: %v", err)
	}
	if err := os.Symlink("c.txt", filepath.Join(src, "a/b/link")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dst, "a/b/c.txt"))
	if err != nil {
		t.Fatalf("read copied c.txt: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("c.txt: got %q, want %q", data, "hello")
	}

	info, err := os.Lstat(filepath.Join(dst, "a/b/link"))
	if err != nil {
		t.Fatalf("lstat copied link: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("link did not survive as symlink")
	}

	if _, err := os.Stat(filepath.Join(dst, "top.txt")); err != nil {
		t.Errorf("top.txt missing in destination: %v", err)
	}
}

func TestCopyTree_RootDoesNotExist(t *testing.T) {
	dst := t.TempDir()
	if err := copyTree("/nonexistent/path", dst); err == nil {
		t.Fatal("expected error from copying nonexistent root, got nil")
	}
}
