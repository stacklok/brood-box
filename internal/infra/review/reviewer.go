// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package review provides interactive per-file review and flushing of
// workspace snapshot changes.
package review

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/stacklok/apiary/pkg/domain/snapshot"
)

// Ensure InteractiveReviewer implements snapshot.Reviewer at compile time.
var _ snapshot.Reviewer = (*InteractiveReviewer)(nil)

// decision represents a user's review choice including bulk operations.
type decision int

const (
	decisionAccept decision = iota
	decisionReject
	decisionAcceptAll
	decisionRejectAll
)

// InteractiveReviewer implements Reviewer with terminal I/O.
type InteractiveReviewer struct {
	in  io.Reader
	out io.Writer
}

// NewInteractiveReviewer creates a reviewer that reads from in and writes to out.
func NewInteractiveReviewer(in io.Reader, out io.Writer) *InteractiveReviewer {
	return &InteractiveReviewer{in: in, out: out}
}

// kindIndicator returns a styled prefix character for a file change kind.
func kindIndicator(kind snapshot.ChangeKind) string {
	switch kind {
	case snapshot.Added:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render("+")
	case snapshot.Modified:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render("~")
	case snapshot.Deleted:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render("-")
	default:
		return "?"
	}
}

// renderDiff wraps a unified diff in a markdown code block and renders it
// with glamour for syntax-highlighted output.
func renderDiff(renderer *glamour.TermRenderer, diff string) string {
	md := "```diff\n" + diff + "\n```\n"
	out, err := renderer.Render(md)
	if err != nil {
		// Fall back to plain text on render failure.
		return diff
	}
	return out
}

// Review walks through each change, shows the diff, and prompts the user.
func (r *InteractiveReviewer) Review(changes []snapshot.FileChange) (snapshot.ReviewResult, error) {
	var result snapshot.ReviewResult

	renderer, err := glamour.NewTermRenderer(glamour.WithAutoStyle())
	if err != nil {
		// Fall back to non-styled rendering if glamour fails.
		renderer = nil
	}

	headerStyle := lipgloss.NewStyle().Bold(true)
	ruleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	// Show summary.
	_, _ = fmt.Fprintf(r.out, "\n%s\n", ruleStyle.Render("━━━ Workspace Review ━━━"))
	_, _ = fmt.Fprintf(r.out, "%s\n\n", headerStyle.Render(
		fmt.Sprintf("%d file(s) changed:", len(changes))))
	for _, ch := range changes {
		_, _ = fmt.Fprintf(r.out, "  %s %s\n", kindIndicator(ch.Kind), ch.RelPath)
	}
	_, _ = fmt.Fprintf(r.out, "\n")

	scanner := bufio.NewScanner(r.in)
	// Use a custom split function that handles \r, \n, and \r\n.
	// After an SSH raw-mode session the terminal may send bare \r
	// instead of \n, causing the default ScanLines to hang.
	scanner.Split(scanLinesAny)

	for i, ch := range changes {
		_, _ = fmt.Fprintf(r.out, "%s\n",
			ruleStyle.Render(fmt.Sprintf("── Change %d/%d: [%s] %s ──",
				i+1, len(changes), ch.Kind, ch.RelPath)))

		if ch.UnifiedDiff != "" {
			if renderer != nil {
				_, _ = fmt.Fprint(r.out, renderDiff(renderer, ch.UnifiedDiff))
			} else {
				_, _ = fmt.Fprintf(r.out, "%s\n", ch.UnifiedDiff)
			}
		}

		d := r.prompt(scanner, ch.RelPath)
		switch d {
		case decisionAccept:
			result.Accepted = append(result.Accepted, ch)
		case decisionReject:
			result.Rejected = append(result.Rejected, ch)
		case decisionAcceptAll:
			result.Accepted = append(result.Accepted, changes[i:]...)
			_, _ = fmt.Fprintf(r.out, "  Accepted remaining %d file(s)\n", len(changes)-i)
			return r.printSummary(result, ruleStyle), nil
		case decisionRejectAll:
			result.Rejected = append(result.Rejected, changes[i:]...)
			_, _ = fmt.Fprintf(r.out, "  Rejected remaining %d file(s)\n", len(changes)-i)
			return r.printSummary(result, ruleStyle), nil
		}
	}

	return r.printSummary(result, ruleStyle), nil
}

func (r *InteractiveReviewer) printSummary(result snapshot.ReviewResult, ruleStyle lipgloss.Style) snapshot.ReviewResult {
	accepted := lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render(
		fmt.Sprintf("%d accepted", len(result.Accepted)))
	rejected := lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(
		fmt.Sprintf("%d rejected", len(result.Rejected)))

	_, _ = fmt.Fprintf(r.out, "\n%s\n",
		ruleStyle.Render(fmt.Sprintf("━━━ Review complete: %s, %s ━━━", accepted, rejected)))

	return result
}

// prompt asks the user for a decision on a single file change.
func (r *InteractiveReviewer) prompt(scanner *bufio.Scanner, relPath string) decision {
	promptStyle := lipgloss.NewStyle().Bold(true)

	for {
		_, _ = fmt.Fprintf(r.out, "%s ",
			promptStyle.Render(
				fmt.Sprintf("Apply %s? [y]es / [n]o / accept [a]ll / reject [A]ll:", relPath)))

		if !scanner.Scan() {
			// EOF — treat as reject.
			return decisionReject
		}

		input := strings.TrimSpace(scanner.Text())
		// Check case-sensitive 'A' (reject-all) before lowercasing.
		if input == "A" {
			return decisionRejectAll
		}
		switch strings.ToLower(input) {
		case "y", "yes":
			return decisionAccept
		case "n", "no", "":
			return decisionReject
		case "a":
			return decisionAcceptAll
		default:
			_, _ = fmt.Fprintf(r.out, "Invalid input. Please enter y, n, a, or A.\n")
		}
	}
}

// scanLinesAny is a bufio.SplitFunc that splits on \n, \r\n, or bare \r.
// This handles terminals that send \r (carriage return) instead of \n after
// a raw-mode SSH session where the terminal may not be fully restored.
func scanLinesAny(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}

	// Look for \r\n first (Windows-style).
	if i := bytes.Index(data, []byte("\r\n")); i >= 0 {
		return i + 2, data[:i], nil
	}

	// Look for bare \n (Unix-style).
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i], nil
	}

	// Look for bare \r (raw terminal mode).
	if i := bytes.IndexByte(data, '\r'); i >= 0 {
		return i + 1, data[:i], nil
	}

	// At EOF, deliver remaining data as a line.
	if atEOF {
		return len(data), data, nil
	}

	// Request more data.
	return 0, nil, nil
}
