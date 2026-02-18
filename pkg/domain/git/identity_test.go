// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package git

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIdentity_IsComplete(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		identity Identity
		want     bool
	}{
		{
			name:     "both set",
			identity: Identity{Name: "Alice", Email: "alice@example.com"},
			want:     true,
		},
		{
			name:     "missing email",
			identity: Identity{Name: "Alice"},
			want:     false,
		},
		{
			name:     "missing name",
			identity: Identity{Email: "alice@example.com"},
			want:     false,
		},
		{
			name:     "both empty",
			identity: Identity{},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.identity.IsComplete())
		})
	}
}

func TestCommonEnvPatterns(t *testing.T) {
	t.Parallel()

	patterns := CommonEnvPatterns()
	assert.NotEmpty(t, patterns)

	expected := []string{
		"GIT_AUTHOR_NAME",
		"GIT_AUTHOR_EMAIL",
		"GIT_COMMITTER_NAME",
		"GIT_COMMITTER_EMAIL",
		"GITHUB_TOKEN",
		"GH_TOKEN",
	}
	assert.Equal(t, expected, patterns)
}
