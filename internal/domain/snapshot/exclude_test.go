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
		".ssh/",
		".aws/",
		"credentials.json",
		".docker/config.json",
		".git/config",
		".pgpass",
		".vault-token",
		"*.tfvars",
		".config/gh/hosts.yml",
	}
	for _, pat := range expected {
		assert.Contains(t, patterns, pat, "missing security pattern: %s", pat)
	}
}

func TestDefaultPerformancePatterns(t *testing.T) {
	t.Parallel()

	patterns := DefaultPerformancePatterns()
	assert.NotEmpty(t, patterns)

	expected := []string{
		"node_modules/",
		"vendor/",
		".git/objects/",
		"__pycache__/",
	}
	for _, pat := range expected {
		assert.Contains(t, patterns, pat, "missing performance pattern: %s", pat)
	}
}
