// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package snapshot

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExcludeConfig_AllPatterns(t *testing.T) {
	t.Parallel()

	cfg := ExcludeConfig{
		SecurityPatterns:    []string{".env*"},
		PerformancePatterns: []string{"node_modules/"},
		FilePatterns:        []string{"*.log"},
		CLIPatterns:         []string{"tmp/"},
	}

	all := cfg.AllPatterns()

	// Order: performance, file, CLI, security
	assert.Equal(t, []string{"node_modules/", "*.log", "tmp/", ".env*"}, all)
}

func TestExcludeConfig_AllPatterns_Empty(t *testing.T) {
	t.Parallel()

	cfg := ExcludeConfig{}
	assert.Empty(t, cfg.AllPatterns())
}

func TestDefaultSecurityPatterns(t *testing.T) {
	t.Parallel()

	patterns := DefaultSecurityPatterns()
	assert.NotEmpty(t, patterns)

	// Verify key security patterns are present.
	expected := []string{
		".env*",
		"*.pem",
		"*.key",
		"*.jks",
		"*.keystore",
		".ssh/",
		".aws/",
		".gcloud/",
		".config/gcloud/",
		"credentials.json",
		".git-credentials",
		".yarnrc.yml",
		".docker/config.json",
		".pgpass",
		".vault-token",
		"*.tfvars",
		".terraform/",
		".config/gh/hosts.yml",
		"age-key.txt",
		"*.age",
		".broodbox.yaml",
	}
	for _, pat := range expected {
		assert.Contains(t, patterns, pat, "missing security pattern: %s", pat)
	}

	// .git/config is NOT a security pattern — it's copied into the snapshot
	// and sanitized by the ConfigSanitizer post-processor.
	assert.NotContains(t, patterns, ".git/config",
		".git/config should not be a security exclude — it is sanitized by post-processor")
}

func TestDefaultDiffSecurityPatterns(t *testing.T) {
	t.Parallel()

	patterns := DefaultDiffSecurityPatterns()
	assert.NotEmpty(t, patterns)

	// .git/config is excluded — preserves the original unsanitized config.
	assert.Contains(t, patterns, ".git/config",
		"diff security patterns must exclude .git/config")
	// .git/hooks/ is excluded — defense-in-depth against hook injection.
	assert.Contains(t, patterns, ".git/hooks/",
		"diff security patterns must exclude .git/hooks/")
	// .git itself should NOT be broadly excluded — objects, refs, HEAD sync back.
	assert.NotContains(t, patterns, ".git",
		".git should not be broadly excluded from diff")
}

func TestDefaultPerformancePatterns(t *testing.T) {
	t.Parallel()

	patterns := DefaultPerformancePatterns()
	assert.NotEmpty(t, patterns)

	expected := []string{
		"node_modules/",
		"vendor/",
		"__pycache__/",
	}
	for _, pat := range expected {
		assert.Contains(t, patterns, pat, "missing performance pattern: %s", pat)
	}
}
