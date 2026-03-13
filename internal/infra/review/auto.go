// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package review

import (
	"log/slog"

	"github.com/stacklok/brood-box/pkg/domain/snapshot"
)

// Ensure AutoAcceptReviewer implements snapshot.Reviewer at compile time.
var _ snapshot.Reviewer = (*AutoAcceptReviewer)(nil)

// AutoAcceptReviewer is a snapshot.Reviewer that accepts all changes without
// prompting. It logs the number of auto-accepted files for observability.
type AutoAcceptReviewer struct {
	logger *slog.Logger
}

// NewAutoAcceptReviewer creates a reviewer that auto-accepts all changes.
func NewAutoAcceptReviewer(logger *slog.Logger) *AutoAcceptReviewer {
	return &AutoAcceptReviewer{logger: logger}
}

// Review accepts every change without user interaction.
func (a *AutoAcceptReviewer) Review(changes []snapshot.FileChange) (snapshot.ReviewResult, error) {
	a.logger.Info("auto-accepting workspace changes", "count", len(changes))
	return snapshot.ReviewResult{Accepted: changes}, nil
}
