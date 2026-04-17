// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package review

import (
	"fmt"
	"io"
	"log/slog"

	"github.com/stacklok/brood-box/pkg/domain/snapshot"
)

// Ensure AutoAcceptReviewer implements snapshot.Reviewer at compile time.
var _ snapshot.Reviewer = (*AutoAcceptReviewer)(nil)

// AutoAcceptReviewer is a snapshot.Reviewer that accepts all changes without
// prompting. Security-sensitive paths are handled according to their tier:
// Tier 1 (auto-exec) changes are rejected, Tier 2 (build/CI) changes are
// accepted with a warning.
type AutoAcceptReviewer struct {
	logger *slog.Logger
	stderr io.Writer
	rules  []snapshot.SensitivePathRule
}

// NewAutoAcceptReviewer creates a reviewer that auto-accepts changes, with
// security-sensitive path classification. Tier 1 paths are rejected and
// Tier 2 paths are accepted with warnings, both printed to stderr.
func NewAutoAcceptReviewer(logger *slog.Logger, stderr io.Writer) *AutoAcceptReviewer {
	return &AutoAcceptReviewer{
		logger: logger,
		stderr: stderr,
		rules:  snapshot.DefaultSensitivePathRules(),
	}
}

// Review classifies each change by sensitivity tier and accepts or rejects accordingly.
func (a *AutoAcceptReviewer) Review(changes []snapshot.FileChange) (snapshot.ReviewResult, error) {
	var result snapshot.ReviewResult
	var rejected, warned int

	for _, ch := range changes {
		tier, reason, sensitive := snapshot.ClassifyPath(ch.RelPath, a.rules)
		if !sensitive {
			result.Accepted = append(result.Accepted, ch)
			continue
		}

		switch tier {
		case snapshot.TierAutoExec:
			result.Rejected = append(result.Rejected, ch)
			rejected++
			_, _ = fmt.Fprintf(a.stderr,
				"REJECTED: %s — %s (re-run with --review to approve)\n",
				ch.RelPath, reason)
			a.logger.Warn("auto-rejected security-sensitive path",
				"path", ch.RelPath, "tier", tier.String(), "reason", reason)
		case snapshot.TierBuildCI:
			result.Accepted = append(result.Accepted, ch)
			warned++
			_, _ = fmt.Fprintf(a.stderr, "WARNING: %s — %s\n", ch.RelPath, reason)
			a.logger.Warn("accepted security-sensitive path with warning",
				"path", ch.RelPath, "tier", tier.String(), "reason", reason)
		default:
			result.Accepted = append(result.Accepted, ch)
		}
	}

	a.logger.Info("auto-accept review complete",
		"accepted", len(result.Accepted),
		"rejected", len(result.Rejected),
	)

	if rejected > 0 || warned > 0 {
		_, _ = fmt.Fprintf(a.stderr,
			"%d file(s) auto-rejected (security-sensitive), %d file(s) warned. Use --review to review interactively.\n",
			rejected, warned)
	}

	return result, nil
}
