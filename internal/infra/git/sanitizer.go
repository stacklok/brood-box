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
//
// `lfs` is deliberately NOT in this list: the
// `[lfs "customtransfer.<name>"]` form carries a `path` key that
// git-lfs executes, and parseSectionName collapses subsections so the
// per-key denylist cannot distinguish `lfs.customtransfer.*.path` from
// a hypothetical benign `lfs.path`. LFS configuration typically lives
// in `~/.gitconfig` (not the repo-local `.git/config` this sanitizer
// processes), so stripping the section from the snapshot does not
// affect real LFS workflows.
var allowedSections = map[string]bool{
	"core": true, "remote": true, "branch": true, "user": true,
	"merge": true, "diff": true, "fetch": true, "push": true,
	"submodule": true, "color": true, "log": true, "rerere": true,
	"rebase": true, "tag": true, "pack": true, "gc": true,
	"status": true, "advice": true, "init": true,
}

// dangerousKeys lists keys that, even within an otherwise-allowed
// section, must be stripped because they can execute arbitrary commands
// or redirect hook/program lookup. Keys are section-scoped and stored
// lowercased (git config keys are case-insensitive).
//
// Background: a malicious `.git/config` in the host workspace would
// otherwise be copied verbatim into the snapshot and honored the next
// time `git` runs inside the VM. `core.sshCommand`, `core.pager`,
// `core.editor`, `core.fsmonitor`, `core.alternateRefsCommand` and the
// per-driver `merge`/`diff` command keys all accept shell command
// strings. `core.hooksPath` redirects git to an attacker-chosen
// hooks directory. `submodule.<name>.update = !cmd` is the
// CVE-2017-1000117 RCE vector.
var dangerousKeys = map[string]map[string]bool{
	"core": {
		"sshcommand":           true,
		"pager":                true,
		"editor":               true,
		"fsmonitor":            true,
		"alternaterefscommand": true,
		"hookspath":            true,
	},
	"merge": {
		"driver": true,
	},
	"diff": {
		"external": true,
		"driver":   true,
		"textconv": true,
		"command":  true,
	},
	"submodule": {
		"update": true,
	},
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
// the result into the correct location in snapshotPath. Supports both
// normal repos (where .git is a directory) and in-workspace worktrees
// (where .git is a file pointing to a gitdir within the workspace).
//
// For external worktrees (where git metadata lives outside the workspace),
// sanitization is skipped because the config is not present in the snapshot.
func (s *ConfigSanitizer) Process(_ context.Context, originalPath, snapshotPath string) (*workspace.PostProcessResult, error) {
	// Find the git config source on the host filesystem.
	srcPath, err := resolveGitConfigPath(originalPath)
	if err != nil {
		s.logger.Warn("could not resolve git config path, skipping sanitization",
			"path", originalPath, "error", err)
		return nil, nil
	}
	if srcPath == "" {
		return nil, nil
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading git config: %w", err)
	}

	sanitized := SanitizeConfig(string(data))

	// Determine where the sanitized config should be written in the snapshot.
	dstPath := s.resolveSnapshotConfigDest(originalPath, snapshotPath)
	if dstPath == "" {
		return nil, nil
	}

	// Ensure parent directory exists. Normally the snapshot creator copies
	// .git/ first, but be defensive for edge cases and tests.
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return nil, fmt.Errorf("creating git config directory in snapshot: %w", err)
	}

	if err := os.WriteFile(dstPath, []byte(sanitized), 0o644); err != nil {
		return nil, fmt.Errorf("writing sanitized git config: %w", err)
	}

	return nil, nil
}

// resolveSnapshotConfigDest determines where to write the sanitized git config
// within the snapshot directory. Returns "" if the destination cannot be
// determined (e.g. external worktree where git metadata is outside the snapshot).
func (s *ConfigSanitizer) resolveSnapshotConfigDest(originalPath, snapshotPath string) string {
	dotGit := filepath.Join(snapshotPath, ".git")

	info, err := os.Lstat(dotGit)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.logger.Warn("unexpected error checking .git in snapshot",
				"path", dotGit, "error", err)
			return ""
		}
		// No .git in snapshot yet. Check what the original has.
		origInfo, origErr := os.Lstat(filepath.Join(originalPath, ".git"))
		if origErr != nil {
			return "" // Not a git repo.
		}
		if origInfo.IsDir() {
			// Normal repo — return the standard path (caller creates dir).
			return filepath.Join(dotGit, "config")
		}
		// Original is a worktree (.git is a file) but it wasn't copied
		// to the snapshot. Can't determine destination.
		s.logger.Warn("worktree .git file not present in snapshot, skipping sanitization")
		return ""
	}

	if info.IsDir() {
		// Normal repo — config is at .git/config.
		return filepath.Join(dotGit, "config")
	}

	// Workspace root is a worktree (.git is a file). The gitdir it points
	// to may be inside or outside the workspace. Try to remap the path
	// from the original workspace into the snapshot.
	dest, err := s.resolveWorktreeSnapshotConfig(originalPath, snapshotPath)
	if err != nil {
		s.logger.Warn("workspace root is a git worktree with external git metadata; "+
			"git config cannot be sanitized in the snapshot. "+
			"Consider running from the main repository root.",
			"path", originalPath, "error", err)
		return ""
	}
	return dest
}

// resolveWorktreeSnapshotConfig follows the worktree .git file chain to find
// the config path within the snapshot. The .git file contains an absolute host
// path that must be remapped into the snapshot directory tree.
func (s *ConfigSanitizer) resolveWorktreeSnapshotConfig(originalPath, snapshotPath string) (string, error) {
	dotGitData, err := os.ReadFile(filepath.Join(snapshotPath, ".git"))
	if err != nil {
		return "", fmt.Errorf("reading .git file in snapshot: %w", err)
	}

	content := strings.TrimSpace(string(dotGitData))
	if !strings.HasPrefix(content, "gitdir: ") {
		return "", fmt.Errorf("malformed .git file: missing 'gitdir: ' prefix")
	}

	gitdirPath := strings.TrimPrefix(content, "gitdir: ")

	// Remap the gitdir from host paths to snapshot paths.
	var snapshotGitdir string
	if filepath.IsAbs(gitdirPath) {
		// Absolute path — check if it's within the original workspace.
		absOriginal, absErr := filepath.Abs(originalPath)
		if absErr != nil {
			return "", fmt.Errorf("resolving original path: %w", absErr)
		}
		rel, relErr := filepath.Rel(absOriginal, gitdirPath)
		if relErr != nil {
			return "", fmt.Errorf("computing relative path for gitdir %q: %w", gitdirPath, relErr)
		}
		if strings.HasPrefix(rel, "..") {
			return "", fmt.Errorf("gitdir %q is outside workspace %q", gitdirPath, absOriginal)
		}
		snapshotGitdir = filepath.Clean(filepath.Join(snapshotPath, rel))
	} else {
		// Relative path — resolves naturally within the snapshot.
		snapshotGitdir = filepath.Clean(filepath.Join(snapshotPath, gitdirPath))
	}

	// Follow the commondir chain within the snapshot to find the shared config.
	commondirData, err := os.ReadFile(filepath.Join(snapshotGitdir, "commondir"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// No commondir — config is in the gitdir itself (e.g. submodule).
			return filepath.Join(snapshotGitdir, "config"), nil
		}
		return "", fmt.Errorf("reading commondir: %w", err)
	}

	commondir := strings.TrimSpace(string(commondirData))
	if !filepath.IsAbs(commondir) {
		commondir = filepath.Join(snapshotGitdir, commondir)
	}
	commondir = filepath.Clean(commondir)

	// Defense-in-depth: verify the resolved commondir stays within the
	// snapshot. A malicious commondir could point outside and cause the
	// sanitizer to write to an arbitrary path.
	absSnapshot, err := filepath.Abs(snapshotPath)
	if err != nil {
		return "", fmt.Errorf("resolving snapshot path: %w", err)
	}
	absCommondir, err := filepath.Abs(commondir)
	if err != nil {
		return "", fmt.Errorf("resolving commondir path: %w", err)
	}
	rel, err := filepath.Rel(absSnapshot, absCommondir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("commondir %q escapes snapshot %q", commondir, snapshotPath)
	}

	return filepath.Join(commondir, "config"), nil
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
	// swallowContinuation drops continuation lines whose starting key
	// was rejected by filterLine. Without this, a denied key with a
	// trailing backslash (e.g. `sshCommand = ssh \`) would suppress the
	// key line but still emit the attacker-controlled continuation
	// lines as orphan fragments under the allowed section.
	swallowContinuation := false

	for _, line := range lines {
		trimmedRight := strings.TrimRight(line, " \t")
		continuation := strings.HasSuffix(trimmedRight, `\`)

		// Handle continuation lines: they inherit the allow/block state
		// of the line that started the value, plus the per-key swallow
		// flag when the start line was dropped.
		if inContinuation {
			if inAllowedSection && !swallowContinuation {
				out = append(out, line)
			}
			inContinuation = continuation
			if !continuation {
				swallowContinuation = false
			}
			continue
		}

		trimmed := strings.TrimSpace(line)

		// Check for section header.
		if strings.HasPrefix(trimmed, "[") {
			section := parseSectionName(line)
			currentSection = section
			inAllowedSection = allowedSections[section]
			swallowContinuation = false

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
			swallowContinuation = false
			continue
		}

		// Apply key-level filtering within allowed sections.
		if kept, rewritten := filterLine(currentSection, line); kept {
			out = append(out, rewritten)
			swallowContinuation = false
		} else {
			// Line dropped — if it had a trailing backslash, drop its
			// continuation lines too.
			swallowContinuation = continuation
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

	// Strip exec-class keys before section-specific handling. These are
	// unsafe in any otherwise-allowed section they appear in.
	if denied, ok := dangerousKeys[section]; ok && denied[lowerKey] {
		return false, ""
	}

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
