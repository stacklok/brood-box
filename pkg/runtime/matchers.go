// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"io"
	"log/slog"

	"github.com/stacklok/apiary/internal/infra/exclude"
	"github.com/stacklok/apiary/pkg/domain/snapshot"
)

// BuildSnapshotMatchers loads exclude configuration and returns matchers for
// snapshot creation and diff computation.
func BuildSnapshotMatchers(workspacePath string, cliPatterns []string, logger *slog.Logger) (snapshot.Matcher, snapshot.Matcher, error) {
	log := logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	excludeCfg, err := exclude.LoadExcludeConfig(workspacePath, cliPatterns, log)
	if err != nil {
		return nil, nil, err
	}

	gitignorePatterns, err := exclude.LoadGitignorePatterns(workspacePath, log)
	if err != nil {
		return nil, nil, err
	}

	snapshotMatcher := exclude.NewMatcherFromConfig(excludeCfg)
	diffMatcher := exclude.NewDiffMatcher(excludeCfg, gitignorePatterns)
	return snapshotMatcher, diffMatcher, nil
}
