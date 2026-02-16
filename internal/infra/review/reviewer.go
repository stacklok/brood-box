// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package review provides interactive per-file review and flushing of
// workspace snapshot changes.
package review

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/stacklok/sandbox-agent/internal/domain/snapshot"
)

// Reviewer presents changes for interactive review.
type Reviewer interface {
	// Review shows the user all changes and lets them accept/reject each one.
	Review(changes []snapshot.FileChange) (snapshot.ReviewResult, error)
}

// InteractiveReviewer implements Reviewer with terminal I/O.
type InteractiveReviewer struct {
	in  io.Reader
	out io.Writer
}

// NewInteractiveReviewer creates a reviewer that reads from in and writes to out.
func NewInteractiveReviewer(in io.Reader, out io.Writer) *InteractiveReviewer {
	return &InteractiveReviewer{in: in, out: out}
}

// Review walks through each change, shows the diff, and prompts the user.
func (r *InteractiveReviewer) Review(changes []snapshot.FileChange) (snapshot.ReviewResult, error) {
	var result snapshot.ReviewResult

	// Show summary.
	_, _ = fmt.Fprintf(r.out, "\n=== Workspace Review ===\n")
	_, _ = fmt.Fprintf(r.out, "%d file(s) changed:\n", len(changes))
	for _, ch := range changes {
		_, _ = fmt.Fprintf(r.out, "  [%s] %s\n", ch.Kind, ch.RelPath)
	}
	_, _ = fmt.Fprintf(r.out, "\n")

	scanner := bufio.NewScanner(r.in)

	for i, ch := range changes {
		_, _ = fmt.Fprintf(r.out, "--- Change %d/%d: [%s] %s ---\n", i+1, len(changes), ch.Kind, ch.RelPath)

		if ch.UnifiedDiff != "" {
			_, _ = fmt.Fprintf(r.out, "%s\n", ch.UnifiedDiff)
		}

		decision := r.prompt(scanner, ch.RelPath)
		switch decision {
		case snapshot.Accept:
			result.Accepted = append(result.Accepted, ch)
		case snapshot.Reject, snapshot.Skip:
			result.Rejected = append(result.Rejected, ch)
		}
	}

	_, _ = fmt.Fprintf(r.out, "\n=== Review complete: %d accepted, %d rejected ===\n",
		len(result.Accepted), len(result.Rejected))

	return result, nil
}

// prompt asks the user for a decision on a single file change.
func (r *InteractiveReviewer) prompt(scanner *bufio.Scanner, relPath string) snapshot.ReviewDecision {
	for {
		_, _ = fmt.Fprintf(r.out, "Apply %s? [y]es / [n]o / [s]kip: ", relPath)

		if !scanner.Scan() {
			// EOF — treat as skip.
			return snapshot.Skip
		}

		// Trim \r in addition to whitespace — raw terminal mode may leave
		// carriage returns in the input even after terminal restore.
		input := strings.TrimSpace(strings.TrimRight(strings.ToLower(scanner.Text()), "\r"))
		switch input {
		case "y", "yes":
			return snapshot.Accept
		case "n", "no":
			return snapshot.Reject
		case "s", "skip", "":
			return snapshot.Skip
		default:
			_, _ = fmt.Fprintf(r.out, "Invalid input. Please enter y, n, or s.\n")
		}
	}
}
