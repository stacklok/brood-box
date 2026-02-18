// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package review

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/apiary/pkg/domain/snapshot"
)

func TestInteractiveReviewer_AcceptAll(t *testing.T) {
	t.Parallel()

	changes := []snapshot.FileChange{
		{RelPath: "file1.go", Kind: snapshot.Modified, UnifiedDiff: "diff1"},
		{RelPath: "file2.go", Kind: snapshot.Added, UnifiedDiff: "diff2"},
	}

	input := "y\ny\n"
	r := NewInteractiveReviewer(strings.NewReader(input), &bytes.Buffer{})

	result, err := r.Review(changes)
	require.NoError(t, err)
	assert.Len(t, result.Accepted, 2)
	assert.Empty(t, result.Rejected)
}

func TestInteractiveReviewer_RejectAll(t *testing.T) {
	t.Parallel()

	changes := []snapshot.FileChange{
		{RelPath: "file1.go", Kind: snapshot.Modified},
		{RelPath: "file2.go", Kind: snapshot.Deleted},
	}

	input := "n\nn\n"
	r := NewInteractiveReviewer(strings.NewReader(input), &bytes.Buffer{})

	result, err := r.Review(changes)
	require.NoError(t, err)
	assert.Empty(t, result.Accepted)
	assert.Len(t, result.Rejected, 2)
}

func TestInteractiveReviewer_EmptyInputRejects(t *testing.T) {
	t.Parallel()

	changes := []snapshot.FileChange{
		{RelPath: "file1.go", Kind: snapshot.Modified},
	}

	// Just pressing Enter (empty input) defaults to reject.
	input := "\n"
	r := NewInteractiveReviewer(strings.NewReader(input), &bytes.Buffer{})

	result, err := r.Review(changes)
	require.NoError(t, err)
	assert.Empty(t, result.Accepted)
	assert.Len(t, result.Rejected, 1)
}

func TestInteractiveReviewer_MixedDecisions(t *testing.T) {
	t.Parallel()

	changes := []snapshot.FileChange{
		{RelPath: "accept.go", Kind: snapshot.Modified},
		{RelPath: "reject.go", Kind: snapshot.Added},
		{RelPath: "also-reject.go", Kind: snapshot.Deleted},
	}

	input := "yes\nno\nn\n"
	r := NewInteractiveReviewer(strings.NewReader(input), &bytes.Buffer{})

	result, err := r.Review(changes)
	require.NoError(t, err)
	assert.Len(t, result.Accepted, 1)
	assert.Len(t, result.Rejected, 2)
	assert.Equal(t, "accept.go", result.Accepted[0].RelPath)
}

func TestInteractiveReviewer_EOF(t *testing.T) {
	t.Parallel()

	changes := []snapshot.FileChange{
		{RelPath: "file.go", Kind: snapshot.Modified},
	}

	// Empty input = EOF.
	r := NewInteractiveReviewer(strings.NewReader(""), &bytes.Buffer{})

	result, err := r.Review(changes)
	require.NoError(t, err)
	assert.Empty(t, result.Accepted)
	assert.Len(t, result.Rejected, 1)
}

func TestInteractiveReviewer_InvalidInputRetries(t *testing.T) {
	t.Parallel()

	changes := []snapshot.FileChange{
		{RelPath: "file.go", Kind: snapshot.Modified},
	}

	// Invalid input, then valid.
	input := "x\nmaybe\ny\n"
	var out bytes.Buffer
	r := NewInteractiveReviewer(strings.NewReader(input), &out)

	result, err := r.Review(changes)
	require.NoError(t, err)
	assert.Len(t, result.Accepted, 1)
	assert.Contains(t, out.String(), "Invalid input")
}

func TestInteractiveReviewer_ShowsSummary(t *testing.T) {
	t.Parallel()

	changes := []snapshot.FileChange{
		{RelPath: "a.go", Kind: snapshot.Added},
		{RelPath: "m.go", Kind: snapshot.Modified},
	}

	input := "y\ny\n"
	var out bytes.Buffer
	r := NewInteractiveReviewer(strings.NewReader(input), &out)

	_, err := r.Review(changes)
	require.NoError(t, err)

	output := out.String()
	assert.Contains(t, output, "Workspace Review")
	assert.Contains(t, output, "2 file(s) changed")
	assert.Contains(t, output, "a.go")
	assert.Contains(t, output, "m.go")
}

func TestInteractiveReviewer_AcceptAllBulk(t *testing.T) {
	t.Parallel()

	changes := []snapshot.FileChange{
		{RelPath: "first.go", Kind: snapshot.Modified},
		{RelPath: "second.go", Kind: snapshot.Added},
		{RelPath: "third.go", Kind: snapshot.Deleted},
	}

	// Accept first individually, then accept-all for remaining.
	input := "y\na\n"
	var out bytes.Buffer
	r := NewInteractiveReviewer(strings.NewReader(input), &out)

	result, err := r.Review(changes)
	require.NoError(t, err)
	assert.Len(t, result.Accepted, 3)
	assert.Empty(t, result.Rejected)
	assert.Contains(t, out.String(), "Accepted remaining 2 file(s)")
}

func TestInteractiveReviewer_RejectAllBulk(t *testing.T) {
	t.Parallel()

	changes := []snapshot.FileChange{
		{RelPath: "first.go", Kind: snapshot.Modified},
		{RelPath: "second.go", Kind: snapshot.Added},
		{RelPath: "third.go", Kind: snapshot.Deleted},
	}

	// Accept first individually, then reject-all for remaining.
	input := "y\nA\n"
	var out bytes.Buffer
	r := NewInteractiveReviewer(strings.NewReader(input), &out)

	result, err := r.Review(changes)
	require.NoError(t, err)
	assert.Len(t, result.Accepted, 1)
	assert.Len(t, result.Rejected, 2)
	assert.Equal(t, "first.go", result.Accepted[0].RelPath)
	assert.Contains(t, out.String(), "Rejected remaining 2 file(s)")
}

func TestInteractiveReviewer_AcceptAllOnFirst(t *testing.T) {
	t.Parallel()

	changes := []snapshot.FileChange{
		{RelPath: "a.go", Kind: snapshot.Added},
		{RelPath: "b.go", Kind: snapshot.Modified},
	}

	// Accept-all on the very first file.
	input := "a\n"
	r := NewInteractiveReviewer(strings.NewReader(input), &bytes.Buffer{})

	result, err := r.Review(changes)
	require.NoError(t, err)
	assert.Len(t, result.Accepted, 2)
	assert.Empty(t, result.Rejected)
}

func TestInteractiveReviewer_RejectAllOnFirst(t *testing.T) {
	t.Parallel()

	changes := []snapshot.FileChange{
		{RelPath: "a.go", Kind: snapshot.Added},
		{RelPath: "b.go", Kind: snapshot.Modified},
	}

	// Reject-all on the very first file.
	input := "A\n"
	r := NewInteractiveReviewer(strings.NewReader(input), &bytes.Buffer{})

	result, err := r.Review(changes)
	require.NoError(t, err)
	assert.Empty(t, result.Accepted)
	assert.Len(t, result.Rejected, 2)
}

func TestInteractiveReviewer_ReviewComplete(t *testing.T) {
	t.Parallel()

	changes := []snapshot.FileChange{
		{RelPath: "a.go", Kind: snapshot.Added},
		{RelPath: "b.go", Kind: snapshot.Modified},
	}

	input := "y\nn\n"
	var out bytes.Buffer
	r := NewInteractiveReviewer(strings.NewReader(input), &out)

	_, err := r.Review(changes)
	require.NoError(t, err)

	output := out.String()
	assert.Contains(t, output, "Review complete")
	assert.Contains(t, output, "1 accepted")
	assert.Contains(t, output, "1 rejected")
}
