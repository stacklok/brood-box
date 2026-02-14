// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package snapshot

// ExcludeConfig holds all exclude patterns organized by source.
type ExcludeConfig struct {
	// SecurityPatterns are non-overridable built-in patterns.
	// These cannot be negated by user patterns.
	SecurityPatterns []string

	// PerformancePatterns are built-in patterns that can be
	// overridden via negation in .sandboxignore.
	PerformancePatterns []string

	// FilePatterns are user patterns from .sandboxignore.
	FilePatterns []string

	// CLIPatterns are patterns provided via --exclude flags.
	CLIPatterns []string
}

// AllPatterns returns all exclude patterns in evaluation order:
// performance (overridable), file, CLI, then security (non-overridable).
func (c ExcludeConfig) AllPatterns() []string {
	total := len(c.PerformancePatterns) + len(c.FilePatterns) +
		len(c.CLIPatterns) + len(c.SecurityPatterns)
	all := make([]string, 0, total)
	all = append(all, c.PerformancePatterns...)
	all = append(all, c.FilePatterns...)
	all = append(all, c.CLIPatterns...)
	all = append(all, c.SecurityPatterns...)
	return all
}

// DefaultSecurityPatterns returns built-in security-sensitive patterns
// that are NEVER overridable by user configuration.
func DefaultSecurityPatterns() []string {
	return []string{
		".env*",
		"*.pem",
		"*.key",
		"*.p12",
		"*.pfx",
		"id_rsa*",
		"id_ed25519*",
		"id_ecdsa*",
		".ssh/",
		".aws/",
		".gcp/",
		".azure/",
		"credentials.json",
		".netrc",
		".npmrc",
		".pypirc",
		".docker/config.json",
		".git/config",
		".kube/config",
		".gnupg/",
		".pgpass",
		".vault-token",
		"*.tfstate*",
		"*.tfvars",
		".config/gh/hosts.yml",
	}
}

// DefaultPerformancePatterns returns built-in performance patterns
// that CAN be overridden via negation in .sandboxignore.
func DefaultPerformancePatterns() []string {
	return []string{
		"node_modules/",
		"vendor/",
		".git/objects/",
		"__pycache__/",
		"target/",
		"build/",
		"dist/",
		".venv/",
		".tox/",
	}
}
