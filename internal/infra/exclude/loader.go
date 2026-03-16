// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package exclude

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/stacklok/brood-box/pkg/domain/snapshot"
)

// sandboxIgnoreFile is the name of the per-workspace exclude config file.
const sandboxIgnoreFile = ".broodboxignore"

// LoadExcludeConfig reads .broodboxignore from the workspace root (if present),
// merges it with built-in defaults and CLI patterns, and returns the combined
// ExcludeConfig.
//
// Negation lines in .broodboxignore that would re-include security patterns
// are stripped and a warning is logged.
func LoadExcludeConfig(workspacePath string, cliPatterns []string, logger *slog.Logger) (snapshot.ExcludeConfig, error) {
	securityPatterns := snapshot.DefaultSecurityPatterns()
	perfPatterns := snapshot.DefaultPerformancePatterns()

	// Read .broodboxignore (missing file = no error).
	ignoreFile := filepath.Join(workspacePath, sandboxIgnoreFile)
	filePatterns, err := readIgnoreFile(ignoreFile, securityPatterns, logger)
	if err != nil {
		return snapshot.ExcludeConfig{}, fmt.Errorf("reading %s: %w", sandboxIgnoreFile, err)
	}

	return snapshot.ExcludeConfig{
		SecurityPatterns:     securityPatterns,
		DiffSecurityPatterns: snapshot.DefaultDiffSecurityPatterns(),
		PerformancePatterns:  perfPatterns,
		FilePatterns:         filePatterns,
		CLIPatterns:          cliPatterns,
	}, nil
}

// LoadGitignorePatterns reads .gitignore from the workspace root and returns
// the patterns. These are loaded separately from the exclude config because
// gitignored files should be present in the snapshot (the agent may need them)
// but excluded from the diff (changes to build artifacts shouldn't appear in review).
func LoadGitignorePatterns(workspacePath string, logger *slog.Logger) ([]string, error) {
	gitignorePath := filepath.Join(workspacePath, ".gitignore")
	patterns, err := readIgnoreFile(gitignorePath, nil, logger)
	if err != nil {
		return nil, fmt.Errorf("reading .gitignore: %w", err)
	}
	return patterns, nil
}

// NewDiffMatcher creates a Matcher that combines the snapshot exclude config
// with additional gitignore patterns. Used for diff computation where gitignored
// files should be skipped.
func NewDiffMatcher(cfg snapshot.ExcludeConfig, gitignorePatterns []string) snapshot.Matcher {
	var userAndPerf []string
	userAndPerf = append(userAndPerf, cfg.PerformancePatterns...)
	userAndPerf = append(userAndPerf, cfg.FilePatterns...)
	userAndPerf = append(userAndPerf, cfg.CLIPatterns...)
	userAndPerf = append(userAndPerf, gitignorePatterns...)

	allSecurity := make([]string, 0, len(cfg.SecurityPatterns)+len(cfg.DiffSecurityPatterns))
	allSecurity = append(allSecurity, cfg.SecurityPatterns...)
	allSecurity = append(allSecurity, cfg.DiffSecurityPatterns...)

	return NewMatcher(userAndPerf, allSecurity)
}

// readIgnoreFile reads a .broodboxignore file and returns the cleaned patterns.
// Negation patterns that would re-include security-sensitive files are stripped.
func readIgnoreFile(path string, securityPatterns []string, logger *slog.Logger) ([]string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	securitySet := make(map[string]bool, len(securityPatterns))
	for _, p := range securityPatterns {
		securitySet[p] = true
	}

	var patterns []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines and comments.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Check for negation of security patterns.
		if strings.HasPrefix(line, "!") {
			negated := line[1:]
			if securitySet[negated] {
				logger.Warn("ignoring negation of security pattern in .broodboxignore",
					"pattern", line,
					"reason", "security patterns cannot be overridden",
				)
				continue
			}
		}

		patterns = append(patterns, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning %s: %w", path, err)
	}

	return patterns, nil
}

// NewMatcherFromConfig creates a two-tier Matcher from an ExcludeConfig.
func NewMatcherFromConfig(cfg snapshot.ExcludeConfig) snapshot.Matcher {
	// User-overridable tier: performance + file + CLI patterns.
	var userAndPerf []string
	userAndPerf = append(userAndPerf, cfg.PerformancePatterns...)
	userAndPerf = append(userAndPerf, cfg.FilePatterns...)
	userAndPerf = append(userAndPerf, cfg.CLIPatterns...)

	return NewMatcher(userAndPerf, cfg.SecurityPatterns)
}
