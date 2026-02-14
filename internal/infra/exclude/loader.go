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

	"github.com/stacklok/sandbox-agent/internal/domain/snapshot"
)

// sandboxIgnoreFile is the name of the per-workspace exclude config file.
const sandboxIgnoreFile = ".sandboxignore"

// LoadExcludeConfig reads .sandboxignore from the workspace root (if present),
// merges it with built-in defaults and CLI patterns, and returns the combined
// ExcludeConfig.
//
// Negation lines in .sandboxignore that would re-include security patterns
// are stripped and a warning is logged.
func LoadExcludeConfig(workspacePath string, cliPatterns []string, logger *slog.Logger) (snapshot.ExcludeConfig, error) {
	securityPatterns := snapshot.DefaultSecurityPatterns()
	perfPatterns := snapshot.DefaultPerformancePatterns()

	// Read .sandboxignore (missing file = no error).
	ignoreFile := filepath.Join(workspacePath, sandboxIgnoreFile)
	filePatterns, err := readIgnoreFile(ignoreFile, securityPatterns, logger)
	if err != nil {
		return snapshot.ExcludeConfig{}, fmt.Errorf("reading %s: %w", sandboxIgnoreFile, err)
	}

	return snapshot.ExcludeConfig{
		SecurityPatterns:    securityPatterns,
		PerformancePatterns: perfPatterns,
		FilePatterns:        filePatterns,
		CLIPatterns:         cliPatterns,
	}, nil
}

// readIgnoreFile reads a .sandboxignore file and returns the cleaned patterns.
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
				logger.Warn("ignoring negation of security pattern in .sandboxignore",
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
func NewMatcherFromConfig(cfg snapshot.ExcludeConfig) Matcher {
	// User-overridable tier: performance + file + CLI patterns.
	var userAndPerf []string
	userAndPerf = append(userAndPerf, cfg.PerformancePatterns...)
	userAndPerf = append(userAndPerf, cfg.FilePatterns...)
	userAndPerf = append(userAndPerf, cfg.CLIPatterns...)

	return NewMatcher(userAndPerf, cfg.SecurityPatterns)
}
