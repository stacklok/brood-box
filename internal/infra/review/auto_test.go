// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package review

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/brood-box/pkg/domain/snapshot"
)

func TestAutoAcceptReviewer_Review(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		changes []snapshot.FileChange
	}{
		{
			name:    "empty changes",
			changes: nil,
		},
		{
			name: "single change",
			changes: []snapshot.FileChange{
				{RelPath: "main.go", Kind: snapshot.Modified},
			},
		},
		{
			name: "multiple changes",
			changes: []snapshot.FileChange{
				{RelPath: "main.go", Kind: snapshot.Modified},
				{RelPath: "new.go", Kind: snapshot.Added},
				{RelPath: "old.go", Kind: snapshot.Deleted},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := NewAutoAcceptReviewer(slog.Default(), &bytes.Buffer{})
			result, err := r.Review(tt.changes)
			require.NoError(t, err)
			assert.Len(t, result.Accepted, len(tt.changes))
			assert.Empty(t, result.Rejected)
		})
	}
}

func TestAutoAcceptReviewer_Tier1Rejected(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	r := NewAutoAcceptReviewer(slog.Default(), &stderr)

	changes := []snapshot.FileChange{
		{RelPath: ".git/hooks/pre-commit", Kind: snapshot.Added},
		{RelPath: "main.go", Kind: snapshot.Modified},
	}

	result, err := r.Review(changes)
	require.NoError(t, err)

	assert.Len(t, result.Accepted, 1)
	assert.Equal(t, "main.go", result.Accepted[0].RelPath)

	assert.Len(t, result.Rejected, 1)
	assert.Equal(t, ".git/hooks/pre-commit", result.Rejected[0].RelPath)

	output := stderr.String()
	assert.Contains(t, output, "REJECTED")
	assert.Contains(t, output, ".git/hooks/pre-commit")
	assert.Contains(t, output, "Use --review")
}

func TestAutoAcceptReviewer_Tier2Warned(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	r := NewAutoAcceptReviewer(slog.Default(), &stderr)

	changes := []snapshot.FileChange{
		{RelPath: "Makefile", Kind: snapshot.Modified},
		{RelPath: "main.go", Kind: snapshot.Modified},
	}

	result, err := r.Review(changes)
	require.NoError(t, err)

	// Tier 2 is accepted with warning.
	assert.Len(t, result.Accepted, 2)
	assert.Empty(t, result.Rejected)

	output := stderr.String()
	assert.Contains(t, output, "WARNING")
	assert.Contains(t, output, "Makefile")
	assert.Contains(t, output, "Use --review")
}

func TestAutoAcceptReviewer_MixedTiers(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	r := NewAutoAcceptReviewer(slog.Default(), &stderr)

	changes := []snapshot.FileChange{
		{RelPath: ".git/hooks/post-merge", Kind: snapshot.Added},
		{RelPath: ".github/workflows/ci.yml", Kind: snapshot.Modified},
		{RelPath: "main.go", Kind: snapshot.Modified},
		{RelPath: ".envrc", Kind: snapshot.Added},
	}

	result, err := r.Review(changes)
	require.NoError(t, err)

	// Tier 1 (.git/hooks) rejected; Tier 2 (.github/workflows, .envrc) +
	// normal accepted. .envrc is now Tier 2 because direnv gates
	// execution via `direnv allow`, a user-action-required step.
	assert.Len(t, result.Rejected, 1)
	assert.Len(t, result.Accepted, 3)

	output := stderr.String()
	assert.Contains(t, output, "1 file(s) auto-rejected")
	assert.Contains(t, output, "2 file(s) warned")
}

func TestAutoAcceptReviewer_NoSummaryForNormalFiles(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	r := NewAutoAcceptReviewer(slog.Default(), &stderr)

	changes := []snapshot.FileChange{
		{RelPath: "main.go", Kind: snapshot.Modified},
		{RelPath: "util.go", Kind: snapshot.Added},
	}

	result, err := r.Review(changes)
	require.NoError(t, err)

	assert.Len(t, result.Accepted, 2)
	assert.Empty(t, result.Rejected)

	// No security summary printed for normal files only.
	assert.NotContains(t, stderr.String(), "auto-rejected")
	assert.NotContains(t, stderr.String(), "Use --review")
}
