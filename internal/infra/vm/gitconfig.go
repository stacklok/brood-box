// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/stacklok/propolis/image"

	domaingit "github.com/stacklok/apiary/pkg/domain/git"
)

// InjectGitConfig returns a RootFS hook that writes git configuration
// and credential helper scripts into the guest rootfs.
func InjectGitConfig(identity domaingit.Identity, hasGitToken bool) func(string, *image.OCIConfig) error {
	return func(rootfsPath string, _ *image.OCIConfig) error {
		// No-op if nothing to inject.
		if !identity.IsComplete() && !hasGitToken {
			return nil
		}

		// Write credential helper script (if token available).
		if hasGitToken {
			if err := writeCredentialHelper(rootfsPath); err != nil {
				return err
			}
		}

		// Write .gitconfig.
		return writeGitConfig(rootfsPath, identity, hasGitToken)
	}
}

// writeCredentialHelper writes the git-credential-apiary shell script
// to /usr/local/bin/ inside the guest rootfs.
func writeCredentialHelper(rootfsPath string) error {
	binDir := filepath.Join(rootfsPath, "usr", "local", "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("creating bin dir: %w", err)
	}

	script := `#!/bin/sh
# Git credential helper for apiary - reads GITHUB_TOKEN/GH_TOKEN at runtime.
# Scoped to github.com hosts only.

case "$1" in
    get)
        # Read stdin to find the host.
        host=""
        while IFS='=' read -r key value; do
            case "$key" in
                host) host="$value" ;;
            esac
        done

        # Only respond for github.com.
        case "$host" in
            github.com)
                token="${GITHUB_TOKEN:-${GH_TOKEN:-}}"
                if [ -n "$token" ]; then
                    printf 'protocol=https\nhost=github.com\nusername=x-access-token\npassword=%s\n' "$token"
                fi
                ;;
        esac
        ;;
esac
`
	helperPath := filepath.Join(binDir, "git-credential-apiary")
	if err := os.WriteFile(helperPath, []byte(script), 0o755); err != nil {
		return fmt.Errorf("writing credential helper: %w", err)
	}
	return nil
}

// writeGitConfig writes a .gitconfig file into the sandbox user's home
// directory inside the guest rootfs.
func writeGitConfig(rootfsPath string, identity domaingit.Identity, hasGitToken bool) error {
	homeDir := filepath.Join(rootfsPath, sandboxHome)
	if err := mkdirAndChown(homeDir); err != nil {
		return fmt.Errorf("creating sandbox home: %w", err)
	}

	var b strings.Builder

	if identity.IsComplete() {
		name := sanitizeGitValue(identity.Name)
		email := sanitizeGitValue(identity.Email)
		if name != "" && email != "" {
			b.WriteString("[user]\n")
			b.WriteString("\tname = " + name + "\n")
			b.WriteString("\temail = " + email + "\n")
		}
	}

	if hasGitToken {
		b.WriteString("[credential]\n")
		b.WriteString("\thelper = /usr/local/bin/git-credential-apiary\n")
	}

	if b.Len() == 0 {
		return nil
	}

	gitconfigPath := filepath.Join(homeDir, ".gitconfig")
	if err := os.WriteFile(gitconfigPath, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("writing .gitconfig: %w", err)
	}
	return os.Chown(gitconfigPath, sandboxUID, sandboxGID)
}

// sanitizeGitValue strips control characters (newlines, tabs, null bytes)
// from a string to prevent injection of arbitrary git config sections.
// Returns empty string if the entire value is control characters.
func sanitizeGitValue(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1 // drop
		}
		return r
	}, s)
}
