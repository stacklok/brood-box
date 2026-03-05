// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package snapshot

// ExcludeConfig holds all exclude patterns organized by source.
type ExcludeConfig struct {
	// SecurityPatterns are non-overridable built-in patterns.
	// These cannot be negated by user patterns.
	SecurityPatterns []string

	// PerformancePatterns are built-in patterns that can be
	// overridden via negation in .broodboxignore.
	PerformancePatterns []string

	// FilePatterns are user patterns from .broodboxignore.
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
		// Environment and secret files
		".env*",
		"*.pem",
		"*.key",
		"*.p12",
		"*.pfx",
		"*.jks",
		"*.keystore",
		"*.truststore",
		// SSH keys
		"id_rsa*",
		"id_ed25519*",
		"id_ecdsa*",
		".ssh/",
		// Cloud provider credentials
		".aws/",
		".gcp/",
		".gcloud/",
		".azure/",
		".config/gcloud/",
		// Credential files
		"credentials.json",
		".netrc",
		".npmrc",
		".yarnrc.yml",
		".pypirc",
		".docker/config.json",
		".git/config",
		".kube/config",
		".gnupg/",
		".pgpass",
		".vault-token",
		// Infrastructure secrets
		"*.tfstate*",
		"*.tfvars",
		".terraform/",
		".config/gh/hosts.yml",
		// Encryption keys
		"age-key.txt",
		"*.age",
		// Brood Box config (should not be modified by agents).
		// Duplicated from config.LocalConfigFile to avoid cross-package dependency.
		".broodbox.yaml",
		// Brood Box agent state (saved credentials).
		".config/broodbox/",
	}
}

// DefaultPerformancePatterns returns built-in performance patterns
// that CAN be overridden via negation in .broodboxignore.
func DefaultPerformancePatterns() []string {
	return []string{
		"node_modules/",
		"vendor/",
		"__pycache__/",
		"target/",
		"build/",
		"dist/",
		".venv/",
		".tox/",
	}
}
