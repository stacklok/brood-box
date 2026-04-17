// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package snapshot

import (
	"path/filepath"
	"strings"
)

// SensitivityTier classifies how dangerous a path modification is.
type SensitivityTier int

const (
	// TierNone means the path is not security-sensitive.
	TierNone SensitivityTier = iota

	// TierAutoExec means the path auto-executes on the host without explicit
	// user action (e.g. git hooks, direnv .envrc).
	TierAutoExec

	// TierBuildCI means the path affects CI/CD or build systems and requires
	// explicit user action to execute (e.g. Makefile, GitHub workflows).
	TierBuildCI
)

// String returns a human-readable label for a SensitivityTier.
func (t SensitivityTier) String() string {
	switch t {
	case TierNone:
		return "none"
	case TierAutoExec:
		return "auto-exec"
	case TierBuildCI:
		return "build/ci"
	default:
		return "unknown"
	}
}

// SensitivePathRule defines a matching rule for security-sensitive paths.
type SensitivePathRule struct {
	// Match returns true if relPath is covered by this rule.
	Match func(relPath string) bool

	// Tier indicates the sensitivity level.
	Tier SensitivityTier

	// Reason is a human-readable explanation shown in warnings.
	Reason string
}

// prefixRule matches any path that starts with the given prefix.
func prefixRule(prefix, reason string, tier SensitivityTier) SensitivePathRule {
	return SensitivePathRule{
		Match: func(relPath string) bool {
			return strings.HasPrefix(relPath, prefix)
		},
		Tier:   tier,
		Reason: reason,
	}
}

// basenameRule matches any path whose final component equals name.
func basenameRule(name, reason string, tier SensitivityTier) SensitivePathRule {
	return SensitivePathRule{
		Match: func(relPath string) bool {
			return filepath.Base(relPath) == name
		},
		Tier:   tier,
		Reason: reason,
	}
}

// exactRootRule matches a path only at the workspace root (no directory separators).
func exactRootRule(name, reason string, tier SensitivityTier) SensitivePathRule {
	return SensitivePathRule{
		Match: func(relPath string) bool {
			return relPath == name
		},
		Tier:   tier,
		Reason: reason,
	}
}

// DefaultSensitivePathRules returns the built-in set of sensitive path rules.
func DefaultSensitivePathRules() []SensitivePathRule {
	return []SensitivePathRule{
		// Tier 1 — auto-exec on host (no explicit user action needed)
		prefixRule(".git/hooks/", "git hook — auto-executes on git operations", TierAutoExec),
		prefixRule(".husky/", "husky git hook — auto-executes on git operations", TierAutoExec),
		exactRootRule(".envrc", "direnv config — auto-executes on directory entry", TierAutoExec),
		exactRootRule(".pre-commit-config.yaml", "pre-commit config — auto-executes on git commit", TierAutoExec),

		// Tier 2 — CI/build (requires explicit user action)
		// .gitattributes is classified here (not TierAutoExec) because:
		//   (a) routine developer edits like `*.go text eol=lf` are common
		//       and legitimate — auto-rejecting them would break normal flow.
		//   (b) the RCE vector (filter/diff/merge drivers) requires the
		//       *filter definition* to already exist in the user's
		//       ~/.gitconfig — if the user has never defined such drivers,
		//       the file cannot execute code on its own.
		//   (c) a visible warning on every .gitattributes flush lets the
		//       user notice an unexpected addition before running `git
		//       checkout` or `git archive`.
		// git honors .gitattributes at any directory depth, so match by basename.
		basenameRule(".gitattributes", "git attributes — can reference filter drivers (filter=/diff=/merge=) that execute on git operations", TierBuildCI),
		prefixRule(".github/workflows/", "GitHub Actions workflow", TierBuildCI),
		exactRootRule(".gitlab-ci.yml", "GitLab CI config", TierBuildCI),
		prefixRule(".gitlab/", "GitLab CI config", TierBuildCI),
		prefixRule(".circleci/", "CircleCI config", TierBuildCI),
		basenameRule("Jenkinsfile", "Jenkins pipeline", TierBuildCI),
		exactRootRule(".travis.yml", "Travis CI config", TierBuildCI),
		basenameRule("Makefile", "build system file", TierBuildCI),
		basenameRule("GNUmakefile", "build system file", TierBuildCI),
		basenameRule("Taskfile.yaml", "build system file", TierBuildCI),
		basenameRule("Taskfile.yml", "build system file", TierBuildCI),
		basenameRule("Justfile", "build system file", TierBuildCI),
		// .gitmodules lists submodule URLs fetched on `git submodule update`.
		// Attacker can redirect to a malicious remote. Requires explicit user
		// action so Tier 2, but still worth surfacing.
		basenameRule(".gitmodules", "git submodule config — URLs fetched on submodule update", TierBuildCI),
		// .git/info/attributes has the same filter-driver RCE surface as
		// .gitattributes but is path-specific (only honored by this repo's
		// worktree). Warn so the user notices.
		exactRootRule(".git/info/attributes", "local git attributes — can reference filter drivers that execute on git operations", TierBuildCI),
		// git-rebase-todo contains `exec <cmd>` directives that run shell
		// commands when the user runs `git rebase --continue` on the host.
		// Flushing in-progress rebase state is a legitimate workflow
		// (resume a rebase started in the VM), so warn rather than block.
		exactRootRule(".git/rebase-merge/git-rebase-todo", "rebase todo — `exec` directives run on `git rebase --continue`", TierBuildCI),
	}
}

// ClassifyPath checks whether relPath matches any of the given rules.
// If sensitive, it returns the tier, reason, and true. Otherwise it returns
// zero values and false. The path is cleaned before matching as defense-in-depth.
func ClassifyPath(relPath string, rules []SensitivePathRule) (SensitivityTier, string, bool) {
	relPath = filepath.Clean(relPath)
	for _, rule := range rules {
		if rule.Match(relPath) {
			return rule.Tier, rule.Reason, true
		}
	}
	return 0, "", false
}
