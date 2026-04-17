// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package snapshot

import "testing"

func TestClassifyPath(t *testing.T) {
	t.Parallel()

	rules := DefaultSensitivePathRules()

	tests := []struct {
		name      string
		relPath   string
		wantTier  SensitivityTier
		wantSense bool
	}{
		// Tier 1 — auto-exec
		{name: "git hook pre-commit", relPath: ".git/hooks/pre-commit", wantTier: TierAutoExec, wantSense: true},
		{name: "git hook post-merge", relPath: ".git/hooks/post-merge", wantTier: TierAutoExec, wantSense: true},
		{name: "husky hook", relPath: ".husky/pre-commit", wantTier: TierAutoExec, wantSense: true},
		{name: "pre-commit config", relPath: ".pre-commit-config.yaml", wantTier: TierAutoExec, wantSense: true},

		// Tier 2 — CI/build
		{name: "github workflow", relPath: ".github/workflows/ci.yml", wantTier: TierBuildCI, wantSense: true},
		{name: "gitlab ci", relPath: ".gitlab-ci.yml", wantTier: TierBuildCI, wantSense: true},
		{name: "gitlab dir", relPath: ".gitlab/agents/config.yaml", wantTier: TierBuildCI, wantSense: true},
		{name: "circleci", relPath: ".circleci/config.yml", wantTier: TierBuildCI, wantSense: true},
		{name: "jenkinsfile root", relPath: "Jenkinsfile", wantTier: TierBuildCI, wantSense: true},
		{name: "jenkinsfile nested", relPath: "sub/Jenkinsfile", wantTier: TierBuildCI, wantSense: true},
		{name: "travis", relPath: ".travis.yml", wantTier: TierBuildCI, wantSense: true},
		{name: "makefile root", relPath: "Makefile", wantTier: TierBuildCI, wantSense: true},
		{name: "makefile nested", relPath: "sub/Makefile", wantTier: TierBuildCI, wantSense: true},
		{name: "gnumakefile", relPath: "GNUmakefile", wantTier: TierBuildCI, wantSense: true},
		{name: "taskfile yaml", relPath: "Taskfile.yaml", wantTier: TierBuildCI, wantSense: true},
		{name: "taskfile yml", relPath: "Taskfile.yml", wantTier: TierBuildCI, wantSense: true},
		{name: "justfile", relPath: "Justfile", wantTier: TierBuildCI, wantSense: true},
		{name: "gitmodules at root", relPath: ".gitmodules", wantTier: TierBuildCI, wantSense: true},
		{name: "gitmodules nested", relPath: "vendored/.gitmodules", wantTier: TierBuildCI, wantSense: true},
		{name: "gitattributes at root", relPath: ".gitattributes", wantTier: TierBuildCI, wantSense: true},
		{name: "gitattributes nested", relPath: "sub/dir/.gitattributes", wantTier: TierBuildCI, wantSense: true},
		{name: "git info attributes", relPath: ".git/info/attributes", wantTier: TierBuildCI, wantSense: true},
		{name: "git rebase todo", relPath: ".git/rebase-merge/git-rebase-todo", wantTier: TierBuildCI, wantSense: true},
		{name: "vscode tasks root", relPath: ".vscode/tasks.json", wantTier: TierBuildCI, wantSense: true},
		{name: "vscode tasks nested", relPath: "packages/foo/.vscode/tasks.json", wantTier: TierBuildCI, wantSense: true},
		{name: "vscode launch root", relPath: ".vscode/launch.json", wantTier: TierBuildCI, wantSense: true},
		{name: "devcontainer.json at root", relPath: ".devcontainer.json", wantTier: TierBuildCI, wantSense: true},
		{name: "devcontainer dir config", relPath: ".devcontainer/devcontainer.json", wantTier: TierBuildCI, wantSense: true},
		{name: "package.json at root", relPath: "package.json", wantTier: TierBuildCI, wantSense: true},
		{name: "package.json nested monorepo", relPath: "packages/foo/package.json", wantTier: TierBuildCI, wantSense: true},
		{name: "pyproject.toml at root", relPath: "pyproject.toml", wantTier: TierBuildCI, wantSense: true},
		{name: "setup.py at root", relPath: "setup.py", wantTier: TierBuildCI, wantSense: true},
		// direnv walks upward: a subdir .envrc fires on `cd sub/`, so
		// it must still be flagged. Classified TierBuildCI (warn, not
		// auto-reject) because direnv gates execution behind
		// `direnv allow` — a user-action-required exec surface similar
		// to npm install triggering package.json scripts.
		{name: "envrc at root (tier2)", relPath: ".envrc", wantTier: TierBuildCI, wantSense: true},
		{name: "envrc in subdir", relPath: "sub/.envrc", wantTier: TierBuildCI, wantSense: true},

		// Not sensitive
		{name: "normal go file", relPath: "main.go", wantTier: TierNone, wantSense: false},
		{name: "normal nested file", relPath: "pkg/foo/bar.go", wantTier: TierNone, wantSense: false},
		{name: "git config not hook", relPath: ".git/config", wantTier: TierNone, wantSense: false},
		{name: "git info exclude benign", relPath: ".git/info/exclude", wantTier: TierNone, wantSense: false},
		{name: "git packed-refs benign", relPath: ".git/packed-refs", wantTier: TierNone, wantSense: false},
		{name: "git rebase patch file", relPath: ".git/rebase-apply/0001.patch", wantTier: TierNone, wantSense: false},
		{name: "git sequencer todo", relPath: ".git/sequencer/todo", wantTier: TierNone, wantSense: false},
		{name: "travis in subdir", relPath: "sub/.travis.yml", wantTier: TierNone, wantSense: false},
		{name: "pre-commit in subdir", relPath: "sub/.pre-commit-config.yaml", wantTier: TierNone, wantSense: false},
		{name: "github non-workflow", relPath: ".github/CODEOWNERS", wantTier: TierNone, wantSense: false},
		{name: "gitlab-ci in subdir", relPath: "sub/.gitlab-ci.yml", wantTier: TierNone, wantSense: false},
		// Non-exec IDE files: pure configuration state, not flagged to avoid
		// noisy warnings on every routine edit.
		{name: "vscode settings benign", relPath: ".vscode/settings.json", wantTier: TierNone, wantSense: false},
		{name: "vscode extensions benign", relPath: ".vscode/extensions.json", wantTier: TierNone, wantSense: false},
		// .vscode/tasks.json suffix check is separator-aware — filenames
		// ending with ".vscode" must NOT match the suffix rule.
		{name: "false-positive suffix", relPath: "foo.vscode/tasks.json", wantTier: TierNone, wantSense: false},
		// Devcontainer auxiliary files (Dockerfile, setup scripts, etc.)
		// are not flagged; only the config entrypoint is.
		{name: "devcontainer dockerfile benign", relPath: ".devcontainer/Dockerfile", wantTier: TierNone, wantSense: false},
		// package-lock.json / poetry.lock are tool-generated and not
		// relevant to exec surface.
		{name: "package-lock benign", relPath: "package-lock.json", wantTier: TierNone, wantSense: false},
		{name: "poetry lock benign", relPath: "poetry.lock", wantTier: TierNone, wantSense: false},

		// Defense-in-depth: non-canonical paths cleaned before matching
		{name: "non-canonical git hook", relPath: ".git/hooks/../hooks/pre-commit", wantTier: TierAutoExec, wantSense: true},
		{name: "double slash in path", relPath: ".git//hooks/pre-commit", wantTier: TierAutoExec, wantSense: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tier, reason, sensitive := ClassifyPath(tt.relPath, rules)
			if sensitive != tt.wantSense {
				t.Errorf("ClassifyPath(%q): sensitive = %v, want %v", tt.relPath, sensitive, tt.wantSense)
			}
			if tier != tt.wantTier {
				t.Errorf("ClassifyPath(%q): tier = %v, want %v", tt.relPath, tier, tt.wantTier)
			}
			if sensitive && reason == "" {
				t.Errorf("ClassifyPath(%q): sensitive but reason is empty", tt.relPath)
			}
			if !sensitive && reason != "" {
				t.Errorf("ClassifyPath(%q): not sensitive but reason = %q", tt.relPath, reason)
			}
		})
	}
}

func TestSensitivityTierString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		tier SensitivityTier
		want string
	}{
		{TierNone, "none"},
		{TierAutoExec, "auto-exec"},
		{TierBuildCI, "build/ci"},
		{SensitivityTier(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			if got := tt.tier.String(); got != tt.want {
				t.Errorf("SensitivityTier(%d).String() = %q, want %q", tt.tier, got, tt.want)
			}
		})
	}
}
