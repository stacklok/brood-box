// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package credential implements persistent credential storage for agent
// authentication files across sandbox VM sessions.
package credential

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/stacklok/brood-box/pkg/domain/credential"
)

const (
	// sandboxHome is the home directory of the sandbox user inside the guest rootfs.
	sandboxHome = "home/sandbox"

	// sandboxUID is the UID of the sandbox user inside the guest.
	sandboxUID = 1000
	// sandboxGID is the GID of the sandbox user inside the guest.
	sandboxGID = 1000

	dirPerm  = 0700
	filePerm = 0600
)

// FSStore persists agent credential files on the host filesystem.
type FSStore struct {
	baseDir string
	logger  *slog.Logger
}

// NewFSStore creates a new filesystem-backed credential store rooted at baseDir.
func NewFSStore(baseDir string, logger *slog.Logger) *FSStore {
	return &FSStore{
		baseDir: baseDir,
		logger:  logger,
	}
}

// Inject copies previously saved credentials into the guest rootfs before VM boot.
func (s *FSStore) Inject(rootfsPath, agentName string, credentialPaths []string) error {
	for _, credPath := range credentialPaths {
		srcBase := filepath.Join(s.baseDir, agentName, credPath)
		dstBase := filepath.Join(rootfsPath, sandboxHome, credPath)

		if err := s.injectPath(srcBase, dstBase); err != nil {
			return fmt.Errorf("injecting credential %q: %w", credPath, err)
		}
	}
	return nil
}

func (s *FSStore) injectPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil // no saved state — skip
		}
		return fmt.Errorf("stat source: %w", err)
	}

	if info.IsDir() {
		return s.injectDir(src, dst)
	}
	return s.injectFile(src, dst)
}

func (s *FSStore) injectDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("reading directory: %w", err)
	}

	if err := mkdirAndChown(dst); err != nil {
		return err
	}

	for _, entry := range entries {
		name := entry.Name()
		if !isSafeName(name) {
			s.logger.Warn("skipping suspicious path component during inject", "name", name)
			continue
		}
		srcChild := filepath.Join(src, name)
		dstChild := filepath.Join(dst, name)

		if entry.IsDir() {
			if err := s.injectDir(srcChild, dstChild); err != nil {
				return err
			}
		} else {
			if err := s.injectFile(srcChild, dstChild); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *FSStore) injectFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
		return fmt.Errorf("creating parent dirs: %w", err)
	}
	bestEffortChown(filepath.Dir(dst))

	sf, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("opening source: %w", err)
	}
	defer func() { _ = sf.Close() }()

	df, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, filePerm)
	if err != nil {
		return fmt.Errorf("creating destination: %w", err)
	}

	if _, err := io.Copy(df, sf); err != nil {
		_ = df.Close()
		return fmt.Errorf("copying data: %w", err)
	}

	if err := df.Close(); err != nil {
		return fmt.Errorf("closing destination: %w", err)
	}

	bestEffortChown(dst)
	return nil
}

// Extract copies credential files from the guest rootfs after the session ends.
func (s *FSStore) Extract(rootfsPath, agentName string, credentialPaths []string) error {
	if !isSafeName(agentName) {
		return fmt.Errorf("invalid agent name: %q", agentName)
	}
	agentDir := filepath.Join(s.baseDir, agentName)

	lockPath := filepath.Join(agentDir, ".lock")
	if err := os.MkdirAll(agentDir, dirPerm); err != nil {
		return fmt.Errorf("creating agent dir: %w", err)
	}

	unlock, err := exclusiveLock(lockPath)
	if err != nil {
		return fmt.Errorf("acquiring lock: %w", err)
	}
	defer unlock()

	for _, credPath := range credentialPaths {
		guestPath := filepath.Join(rootfsPath, sandboxHome, credPath)

		info, err := os.Lstat(guestPath)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return fmt.Errorf("stat credential %q: %w", credPath, err)
		}

		dstBase := filepath.Join(agentDir, credPath)

		if info.IsDir() {
			if err := s.extractDir(guestPath, dstBase, agentDir, 0); err != nil {
				return fmt.Errorf("extracting credential %q: %w", credPath, err)
			}
		} else {
			if info.Mode()&os.ModeSymlink != 0 {
				s.logger.Warn("skipping symlink credential", "path", credPath)
				continue
			}
			if err := s.extractFile(guestPath, dstBase, agentDir); err != nil {
				return fmt.Errorf("extracting credential %q: %w", credPath, err)
			}
		}
	}
	return nil
}

// fileCounter tracks the total number of files extracted.
type fileCounter struct {
	count int
}

func (s *FSStore) extractDir(src, dst, agentDir string, depth int) error {
	counter := &fileCounter{}
	return s.extractDirInner(src, dst, agentDir, depth, counter)
}

func (s *FSStore) extractDirInner(src, dst, agentDir string, depth int, counter *fileCounter) error {
	if depth >= credential.MaxDepth {
		s.logger.Warn("skipping directory exceeding max depth", "path", src, "depth", depth)
		return nil
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return fmt.Errorf("reading directory: %w", err)
	}

	if err := os.MkdirAll(dst, dirPerm); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	for _, entry := range entries {
		name := entry.Name()

		if !isSafeName(name) {
			s.logger.Warn("skipping suspicious path component", "name", name)
			continue
		}

		srcChild := filepath.Join(src, name)
		dstChild := filepath.Join(dst, name)

		info, err := os.Lstat(srcChild)
		if err != nil {
			return fmt.Errorf("lstat %q: %w", srcChild, err)
		}

		if info.Mode()&os.ModeSymlink != 0 {
			s.logger.Warn("skipping symlink", "path", srcChild)
			continue
		}

		if info.IsDir() {
			if err := s.extractDirInner(srcChild, dstChild, agentDir, depth+1, counter); err != nil {
				return err
			}
			continue
		}

		if counter.count >= credential.MaxFileCount {
			s.logger.Warn("file count cap reached, stopping extraction", "cap", credential.MaxFileCount)
			return nil
		}

		if info.Size() > credential.MaxFileSize {
			s.logger.Warn("skipping oversized file", "path", srcChild, "size", info.Size(), "max", credential.MaxFileSize)
			continue
		}

		if _, err := containedPath(agentDir, dstChild); err != nil {
			s.logger.Warn("path containment violation", "path", dstChild, "err", err)
			continue
		}

		if err := s.extractFile(srcChild, dstChild, agentDir); err != nil {
			return err
		}
		counter.count++
	}
	return nil
}

func (s *FSStore) extractFile(src, dst, agentDir string) error {
	if _, err := containedPath(agentDir, dst); err != nil {
		return fmt.Errorf("path containment: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
		return fmt.Errorf("creating parent dirs: %w", err)
	}

	// Open with O_NOFOLLOW to prevent TOCTOU symlink attacks between
	// the Lstat check and the actual file open.
	fd, err := syscall.Open(src, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("opening source (O_NOFOLLOW): %w", err)
	}
	sf := os.NewFile(uintptr(fd), src)
	defer func() { _ = sf.Close() }()

	// Atomic write: create temp file with restrictive permissions from the start
	// to avoid a window where the file is world-readable.
	tmpPath, err := tempFilePath(filepath.Dir(dst))
	if err != nil {
		return fmt.Errorf("generating temp path: %w", err)
	}
	tmpFile, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, filePerm)
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}

	if _, err := io.Copy(tmpFile, sf); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("writing temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("closing temp file: %w", err)
	}

	if err := os.Rename(tmpPath, dst); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic rename: %w", err)
	}

	return nil
}

// resolvePath validates agentName and relPath, then returns the resolved
// filesystem path. Returns an error for invalid names or path traversal.
func (s *FSStore) resolvePath(agentName, relPath string) (string, error) {
	if !isSafeName(agentName) {
		return "", fmt.Errorf("invalid agent name: %q", agentName)
	}

	for _, part := range strings.Split(relPath, "/") {
		if part == "" {
			continue
		}
		if !isSafeName(part) {
			return "", fmt.Errorf("invalid path component %q in %q", part, relPath)
		}
	}

	resolved := filepath.Join(s.baseDir, agentName, relPath)

	agentDir := filepath.Join(s.baseDir, agentName)
	if _, err := containedPath(agentDir, resolved); err != nil {
		return "", fmt.Errorf("path containment: %w", err)
	}

	return resolved, nil
}

// SeedFile writes a file into the credential store for an agent if it does
// not already exist. relPath is relative to the agent's home directory
// (e.g. ".claude/.credentials.json").
func (s *FSStore) SeedFile(agentName, relPath string, content []byte) error {
	dst, err := s.resolvePath(agentName, relPath)
	if err != nil {
		return err
	}

	// No-op if the file already exists.
	if _, err := os.Stat(dst); err == nil {
		s.logger.Debug("credential file already exists, skipping seed",
			"agent", agentName, "path", relPath)
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
		return fmt.Errorf("creating parent dirs: %w", err)
	}

	// Atomic write: temp file + rename to avoid partial files on crash,
	// matching the OverwriteFile and extractFile patterns.
	tmpPath, err := tempFilePath(filepath.Dir(dst))
	if err != nil {
		return fmt.Errorf("generating temp path: %w", err)
	}

	if err := os.WriteFile(tmpPath, content, filePerm); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("writing temp file: %w", err)
	}

	if err := os.Rename(tmpPath, dst); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic rename: %w", err)
	}

	s.logger.Info("seeded credential file",
		"agent", agentName, "path", relPath)
	return nil
}

// ReadFile reads a file from the credential store for an agent. relPath is
// relative to the agent's home directory (e.g. ".claude/.credentials.json").
// Returns os.ErrNotExist if the file does not exist.
func (s *FSStore) ReadFile(agentName, relPath string) ([]byte, error) {
	src, err := s.resolvePath(agentName, relPath)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(src)
	if err != nil {
		return nil, fmt.Errorf("reading credential file: %w", err)
	}

	if int64(len(data)) > credential.MaxFileSize {
		return nil, fmt.Errorf("credential file exceeds max size (%d bytes)", credential.MaxFileSize)
	}

	return data, nil
}

// OverwriteFile writes a file into the credential store for an agent,
// replacing any existing content. relPath is relative to the agent's home
// directory (e.g. ".claude/.credentials.json").
func (s *FSStore) OverwriteFile(agentName, relPath string, content []byte) error {
	dst, err := s.resolvePath(agentName, relPath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dst), dirPerm); err != nil {
		return fmt.Errorf("creating parent dirs: %w", err)
	}

	// Atomic write: temp file + rename to avoid data-loss window and
	// TOCTOU symlink risk, matching the extractFile pattern.
	tmpPath, err := tempFilePath(filepath.Dir(dst))
	if err != nil {
		return fmt.Errorf("generating temp path: %w", err)
	}

	if err := os.WriteFile(tmpPath, content, filePerm); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("writing temp file: %w", err)
	}

	if err := os.Rename(tmpPath, dst); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic rename: %w", err)
	}

	s.logger.Info("overwrote credential file",
		"agent", agentName, "path", relPath)
	return nil
}

// isSafeName rejects path components that could cause traversal or injection.
func isSafeName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsAny(name, "/\\") {
		return false
	}
	if strings.Contains(name, "..") {
		return false
	}
	return true
}

// tempFilePath generates a unique temporary file path in the given directory.
func tempFilePath(dir string) (string, error) {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	return filepath.Join(dir, ".cred-"+hex.EncodeToString(buf[:])+".tmp"), nil
}

// containedPath verifies that the resolved path stays under the base directory.
func containedPath(base, target string) (string, error) {
	// Clean the target to resolve . and redundant separators.
	cleaned := filepath.Clean(target)

	// Reject if the cleaned path still references parent directories relative to base.
	rel, err := filepath.Rel(base, cleaned)
	if err != nil {
		return "", fmt.Errorf("computing relative path: %w", err)
	}

	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path %q escapes base directory", target)
	}

	// Resolve the full target path (not just the parent) to catch symlinked
	// intermediate directories. Fall back to parent-only resolution when the
	// target file doesn't exist yet (e.g. during inject).
	resolvedTarget, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Target doesn't exist — resolve the parent instead.
			resolvedDir, dirErr := filepath.EvalSymlinks(filepath.Dir(cleaned))
			if dirErr != nil {
				if errors.Is(dirErr, os.ErrNotExist) {
					return cleaned, nil
				}
				return "", fmt.Errorf("resolving parent symlinks: %w", dirErr)
			}
			resolvedTarget = filepath.Join(resolvedDir, filepath.Base(cleaned))
		} else {
			return "", fmt.Errorf("resolving target symlinks: %w", err)
		}
	}

	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cleaned, nil
		}
		return "", fmt.Errorf("resolving base symlinks: %w", err)
	}

	// Verify the resolved target is under the resolved base using prefix check.
	if !strings.HasPrefix(resolvedTarget+"/", resolvedBase+"/") {
		return "", fmt.Errorf("resolved path %q escapes base %q", resolvedTarget, resolvedBase)
	}

	return cleaned, nil
}

// exclusiveLock acquires an exclusive flock on the given path and returns
// an unlock function.
func exclusiveLock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, filePerm)
	if err != nil {
		return nil, fmt.Errorf("opening lock file: %w", err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("acquiring flock: %w", err)
	}

	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// mkdirAndChown creates a directory and attempts to chown it to the sandbox user.
func mkdirAndChown(path string) error {
	if err := os.MkdirAll(path, dirPerm); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}
	bestEffortChown(path)
	return nil
}

// bestEffortChown attempts to chown a path to the sandbox user, ignoring EPERM.
func bestEffortChown(path string) {
	err := os.Lchown(path, sandboxUID, sandboxGID)
	if err != nil && !errors.Is(err, syscall.EPERM) {
		slog.Debug("unexpected chown error", "path", path, "error", err)
	}
}
