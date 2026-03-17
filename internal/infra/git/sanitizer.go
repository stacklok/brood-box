// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package git provides infrastructure for sanitizing git configuration
// files before they are exposed inside sandbox VMs.
package git

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/stacklok/brood-box/pkg/domain/workspace"
)

// Ensure ConfigSanitizer implements workspace.SnapshotPostProcessor at compile time.
var _ workspace.SnapshotPostProcessor = (*ConfigSanitizer)(nil)

// allowedSections is the set of git config section names that are safe to
// pass through to the sandbox. Any section not in this list is stripped.
var allowedSections = map[string]bool{
	"core": true, "remote": true, "branch": true, "user": true,
	"merge": true, "diff": true, "fetch": true, "push": true,
	"submodule": true, "color": true, "log": true, "rerere": true,
	"rebase": true, "tag": true, "pack": true, "gc": true,
	"lfs": true, "status": true, "advice": true, "init": true,
}

// ConfigSanitizer reads .git/config from the original workspace,
// sanitizes it by stripping dangerous sections and credentials,
// and writes the sanitized version into the snapshot workspace.
type ConfigSanitizer struct {
	logger *slog.Logger
}

// NewConfigSanitizer creates a new ConfigSanitizer.
func NewConfigSanitizer(logger *slog.Logger) *ConfigSanitizer {
	return &ConfigSanitizer{logger: logger}
}

// Process reads .git/config from originalPath, sanitizes it, and writes
// the result into snapshotPath/.git/config. Supports both normal repos
// (where .git is a directory) and git worktrees (where .git is a file
// pointing to the main repo's gitdir).
func (s *ConfigSanitizer) Process(_ context.Context, originalPath, snapshotPath string) error {
	srcPath, err := resolveGitConfigPath(originalPath)
	if err != nil {
		s.logger.Warn("could not resolve git config path, skipping sanitization",
			"path", originalPath, "error", err)
		return nil
	}
	if srcPath == "" {
		return nil
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("reading git config: %w", err)
	}

	sanitized := SanitizeConfig(string(data))

	dstDir := filepath.Join(snapshotPath, ".git")

	// In worktree snapshots, .git may be a file (the worktree pointer).
	// Remove it so MkdirAll can create the directory.
	if err := removeIfFile(dstDir); err != nil {
		return fmt.Errorf("removing .git file in snapshot: %w", err)
	}

	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("creating .git directory in snapshot: %w", err)
	}

	dstPath := filepath.Join(dstDir, "config")
	if err := os.WriteFile(dstPath, []byte(sanitized), 0o644); err != nil {
		return fmt.Errorf("writing sanitized git config: %w", err)
	}

	// For worktree snapshots, create minimal git directory structure
	// so that git recognizes this as a valid repository.
	if isWorktree(originalPath) {
		if err := initWorktreeGitDir(originalPath, dstDir); err != nil {
			s.logger.Warn("could not initialize worktree git structure",
				"path", originalPath, "error", err)
		}
	}

	return nil
}

// isWorktree returns true if the workspace is a git worktree
// (i.e. .git is a regular file, not a directory).
func isWorktree(workspacePath string) bool {
	info, err := os.Lstat(filepath.Join(workspacePath, ".git"))
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// resolveWorktreeGitDir parses the gitdir path from a worktree's .git file.
func resolveWorktreeGitDir(workspacePath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(workspacePath, ".git"))
	if err != nil {
		return "", fmt.Errorf("reading .git file: %w", err)
	}

	content := strings.TrimSpace(string(data))
	if !strings.HasPrefix(content, "gitdir: ") {
		return "", fmt.Errorf("malformed .git file: missing 'gitdir: ' prefix")
	}

	gitdir := strings.TrimPrefix(content, "gitdir: ")
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(workspacePath, gitdir)
	}
	gitdir = filepath.Clean(gitdir)

	// Defense-in-depth: verify the resolved path looks like a git
	// directory. Without this, a malicious .git file could point to
	// an arbitrary path and leak file contents into the snapshot.
	if _, err := os.Stat(filepath.Join(gitdir, "HEAD")); err != nil {
		return "", fmt.Errorf("resolved gitdir %q does not contain HEAD: %w", gitdir, err)
	}

	return gitdir, nil
}

// initWorktreeGitDir creates the minimal git directory structure
// (HEAD, objects/, refs/) needed for git to recognize a worktree
// snapshot as a valid repository.
func initWorktreeGitDir(originalPath, dstDir string) error {
	// Create objects/ and refs/ directories.
	for _, sub := range []string{"objects", "refs"} {
		if err := os.MkdirAll(filepath.Join(dstDir, sub), 0o755); err != nil {
			return fmt.Errorf("creating %s directory: %w", sub, err)
		}
	}

	// Resolve the worktree's gitdir to read its HEAD.
	gitdir, err := resolveWorktreeGitDir(originalPath)
	if err != nil {
		return fmt.Errorf("resolving worktree gitdir: %w", err)
	}

	headContent, err := os.ReadFile(filepath.Join(gitdir, "HEAD"))
	if err != nil {
		// Fallback if HEAD is unreadable.
		headContent = []byte("ref: refs/heads/main\n")
	}

	head := strings.TrimSpace(string(headContent))

	// If HEAD is not a well-formed symbolic ref, use a safe fallback.
	// A raw SHA with empty objects/ causes git errors; a symbolic ref
	// to a missing branch works fine (same as git init). We require
	// "ref: refs/" (not just "ref: ") to prevent leaking content from
	// non-git files that happen to start with "ref: ".
	if !strings.HasPrefix(head, "ref: refs/") {
		head = "ref: refs/heads/main"
	}

	if err := os.WriteFile(filepath.Join(dstDir, "HEAD"), []byte(head+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing HEAD: %w", err)
	}

	return nil
}

// resolveGitConfigPath returns the path to the git config file for the
// given workspace. It handles both normal repos (.git is a directory)
// and worktrees (.git is a file with a gitdir pointer).
//
// Returns ("", nil) if there is no .git entry (not a git repo).
func resolveGitConfigPath(workspacePath string) (string, error) {
	dotGitPath := filepath.Join(workspacePath, ".git")

	data, err := os.ReadFile(dotGitPath)
	if err == nil {
		// .git is a file — this is a worktree.
		return resolveWorktreeConfigPath(workspacePath, data)
	}

	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}

	if errors.Is(err, syscall.EISDIR) {
		// .git is a directory — normal repo.
		return filepath.Join(dotGitPath, "config"), nil
	}

	return "", fmt.Errorf("reading .git: %w", err)
}

// resolveWorktreeConfigPath parses the worktree .git file content and
// resolves through the commondir file to find the shared git config.
func resolveWorktreeConfigPath(workspacePath string, dotGitData []byte) (string, error) {
	content := strings.TrimSpace(string(dotGitData))
	if !strings.HasPrefix(content, "gitdir: ") {
		return "", fmt.Errorf("malformed .git file: missing 'gitdir: ' prefix")
	}

	gitdir := strings.TrimPrefix(content, "gitdir: ")
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(workspacePath, gitdir)
	}
	gitdir = filepath.Clean(gitdir)

	// Try to read the commondir file to find the shared .git directory.
	commondirData, err := os.ReadFile(filepath.Join(gitdir, "commondir"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// No commondir — fall back to gitdir/config (submodules, etc.).
			return filepath.Join(gitdir, "config"), nil
		}
		return "", fmt.Errorf("reading commondir: %w", err)
	}

	commondir := strings.TrimSpace(string(commondirData))
	if !filepath.IsAbs(commondir) {
		commondir = filepath.Join(gitdir, commondir)
	}
	commondir = filepath.Clean(commondir)

	// Defense-in-depth: verify the resolved path looks like a git directory.
	if _, err := os.Stat(filepath.Join(commondir, "HEAD")); err != nil {
		return "", fmt.Errorf("resolved commondir %q does not contain HEAD: %w", commondir, err)
	}

	return filepath.Join(commondir, "config"), nil
}

// removeIfFile removes the path if it exists and is not a directory.
// No-op if the path does not exist or is already a directory.
func removeIfFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.IsDir() {
		return nil
	}
	return os.Remove(path)
}

// SanitizeConfig parses a git config file and returns a sanitized version
// with dangerous sections, credentials, and sensitive keys stripped.
func SanitizeConfig(input string) string {
	if input == "" {
		return ""
	}

	// Normalize Windows line endings before processing.
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")

	lines := strings.Split(input, "\n")
	var out []string

	var currentSection string
	inAllowedSection := false
	inContinuation := false

	for _, line := range lines {
		trimmedRight := strings.TrimRight(line, " \t")
		continuation := strings.HasSuffix(trimmedRight, `\`)

		// Handle continuation lines: they inherit the allow/block state
		// of the line that started the value.
		if inContinuation {
			if inAllowedSection {
				out = append(out, line)
			}
			inContinuation = continuation
			continue
		}

		trimmed := strings.TrimSpace(line)

		// Check for section header.
		if strings.HasPrefix(trimmed, "[") {
			section := parseSectionName(line)
			currentSection = section
			inAllowedSection = allowedSections[section]

			if !inAllowedSection {
				// Known dangerous or unknown section — skip.
				inContinuation = continuation
				continue
			}

			out = append(out, line)
			inContinuation = continuation
			continue
		}

		// Non-section-header line (key=value, comment, or blank).
		if !inAllowedSection {
			inContinuation = continuation
			continue
		}

		// Apply key-level filtering within allowed sections.
		if kept, rewritten := filterLine(currentSection, line); kept {
			out = append(out, rewritten)
		}

		inContinuation = continuation
	}

	return strings.Join(out, "\n")
}

// filterLine decides whether a key-value line within an allowed section
// should be included, and optionally rewrites it. Returns (keep, line).
func filterLine(section, line string) (bool, string) {
	key := extractKey(line)

	// Comments and blank lines always pass through in allowed sections.
	if key == "" {
		return true, line
	}

	lowerKey := strings.ToLower(key)

	switch section {
	case "user":
		// Only name and email are safe; signingkey etc. are stripped.
		if lowerKey == "name" || lowerKey == "email" {
			return true, line
		}
		return false, ""

	case "remote", "submodule":
		// URL and pushurl values need credential sanitization.
		if lowerKey == "url" || lowerKey == "pushurl" {
			rewritten := rewriteURLLine(line)
			if rewritten == "" {
				return false, ""
			}
			return true, rewritten
		}
		return true, line

	default:
		return true, line
	}
}

// extractKey returns the key name from a git config line.
// Returns "" for comments and blank lines.
func extractKey(line string) string {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || trimmed[0] == '#' || trimmed[0] == ';' {
		return ""
	}
	if idx := strings.IndexByte(trimmed, '='); idx >= 0 {
		return strings.TrimSpace(trimmed[:idx])
	}
	// Bare boolean key (no = sign).
	return strings.TrimSpace(trimmed)
}

// parseSectionName extracts the lowercased section name from a header line
// like `[remote "origin"]`, `[core]`, or `[includeIf "gitdir:/path"]`.
func parseSectionName(line string) string {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 2 || trimmed[0] != '[' {
		return ""
	}

	// Skip the opening bracket.
	inner := trimmed[1:]

	// Find where the section name ends: at space, quote, dot, or closing bracket.
	end := len(inner)
	for i, ch := range inner {
		if ch == ' ' || ch == '"' || ch == '.' || ch == ']' {
			end = i
			break
		}
	}

	return strings.ToLower(inner[:end])
}

// rewriteURLLine sanitizes the URL value in a git config key=value line.
// Returns the rewritten line, or "" if the URL should be dropped entirely.
func rewriteURLLine(line string) string {
	idx := strings.IndexByte(line, '=')
	if idx < 0 {
		return line
	}

	prefix := line[:idx+1]
	value := line[idx+1:]

	trimmedValue := strings.TrimSpace(value)
	sanitized := sanitizeURL(trimmedValue)

	// If sanitized is empty (fail-closed), drop the line entirely.
	if sanitized == "" {
		return ""
	}

	return prefix + " " + sanitized
}

// ContainsCredentials checks whether a git config file at the given path
// contains embedded credentials (userinfo in URLs or [credential] sections).
// Returns false, nil for nonexistent files.
func ContainsCredentials(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("reading git config: %w", err)
	}
	return containsCredentials(string(data)), nil
}

// containsCredentials scans git config content for embedded credentials.
// It detects [credential] section headers, [url] sections with credential
// URLs in subsection strings, and URLs with password components in values.
func containsCredentials(input string) bool {
	if input == "" {
		return false
	}

	// Normalize Windows line endings before processing.
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")

	lines := strings.Split(input, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Skip comments and blank lines.
		if trimmed == "" || trimmed[0] == '#' || trimmed[0] == ';' {
			continue
		}

		// Check section headers.
		if strings.HasPrefix(trimmed, "[") {
			section := parseSectionName(line)
			if section == "credential" {
				return true
			}
			// [url "https://user:token@host/"] insteadOf patterns.
			if section == "url" {
				if sub := parseSubsection(line); sub != "" && urlHasCredentials(sub) {
					return true
				}
			}
			continue
		}

		// Check key=value lines for URLs with embedded passwords.
		if idx := strings.IndexByte(trimmed, '='); idx >= 0 {
			value := strings.TrimSpace(trimmed[idx+1:])
			if urlHasCredentials(value) {
				return true
			}
		}
	}
	return false
}

// urlHasCredentials checks whether a URL string contains a password component.
// Returns false for username-only URLs (e.g. ssh://git@host) since those use
// key-based auth, not embedded secrets. Fails closed: malformed URLs that look
// like they contain credentials are flagged.
func urlHasCredentials(value string) bool {
	u, err := url.Parse(value)
	if err == nil && u.Scheme != "" {
		if u.User != nil {
			_, hasPassword := u.User.Password()
			return hasPassword
		}
		return false
	}

	// Parse failed or no scheme — heuristic check mirroring sanitizeURL's
	// fail-closed logic: scheme + @ + colon suggests embedded credentials.
	hasScheme := strings.Contains(value, "://")
	hasAt := strings.Contains(value, "@")
	return hasScheme && hasAt
}

// parseSubsection extracts the quoted subsection value from a git config
// section header like `[url "https://github.com/"]`. Returns "" if no
// quoted subsection is present.
func parseSubsection(line string) string {
	start := strings.IndexByte(line, '"')
	if start < 0 {
		return ""
	}
	end := strings.LastIndexByte(line, '"')
	if end <= start {
		return ""
	}
	return line[start+1 : end]
}

// sanitizeURL strips embedded credentials from a URL value.
func sanitizeURL(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return trimmed
	}

	u, err := url.Parse(trimmed)
	if err == nil && u.Scheme != "" {
		if u.User != nil {
			_, hasPassword := u.User.Password()
			if hasPassword {
				// Strip embedded credentials (user:token@host).
				u.User = nil
				return u.String()
			}
			// Username-only (e.g. ssh://git@host) — SSH key auth, not a secret.
			return trimmed
		}
		return trimmed
	}

	// Parse failed or no scheme — check for SCP-style git URLs.
	hasAt := strings.Contains(trimmed, "@")
	hasScheme := strings.Contains(trimmed, "://")

	if hasAt && !hasScheme {
		// SCP-style URL like git@github.com:org/repo.git — safe (SSH key auth).
		return trimmed
	}

	if hasScheme && hasAt {
		// Looks like scheme://user:pass@host but failed to parse — fail closed.
		return ""
	}

	return trimmed
}
