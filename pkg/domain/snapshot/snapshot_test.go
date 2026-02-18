// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package snapshot

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestChangeKind_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		kind ChangeKind
		want string
	}{
		{Added, "added"},
		{Modified, "modified"},
		{Deleted, "deleted"},
		{ChangeKind(99), "unknown(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.kind.String())
		})
	}
}

func TestReviewResult(t *testing.T) {
	t.Parallel()

	accepted := []FileChange{
		{RelPath: "file1.go", Kind: Modified},
	}
	rejected := []FileChange{
		{RelPath: "file2.go", Kind: Added},
	}

	result := ReviewResult{
		Accepted: accepted,
		Rejected: rejected,
	}

	assert.Len(t, result.Accepted, 1)
	assert.Len(t, result.Rejected, 1)
	assert.Equal(t, "file1.go", result.Accepted[0].RelPath)
	assert.Equal(t, "file2.go", result.Rejected[0].RelPath)
}
