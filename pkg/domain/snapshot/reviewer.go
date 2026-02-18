// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package snapshot

// Reviewer presents changes for interactive review.
type Reviewer interface {
	// Review shows the user all changes and lets them accept/reject each one.
	Review(changes []FileChange) (ReviewResult, error)
}
