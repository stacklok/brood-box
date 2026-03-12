// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package review

import (
	"log/slog"
	"testing"

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
			r := NewAutoAcceptReviewer(slog.Default())
			result, err := r.Review(tt.changes)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(result.Accepted) != len(tt.changes) {
				t.Errorf("expected %d accepted, got %d", len(tt.changes), len(result.Accepted))
			}
			if len(result.Rejected) != 0 {
				t.Errorf("expected 0 rejected, got %d", len(result.Rejected))
			}
		})
	}
}
