// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package git

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	domaingit "github.com/stacklok/brood-box/pkg/domain/git"
)

// Compile-time interface check.
var _ domaingit.IdentityProvider = (*HostIdentityProvider)(nil)

// HostIdentityProvider reads the git user identity from the host's
// ~/.gitconfig file.
type HostIdentityProvider struct {
	homeDir string
}

// NewHostIdentityProvider creates a provider that reads ~/.gitconfig.
// Pass "" for homeDir to use os.UserHomeDir().
func NewHostIdentityProvider(homeDir string) *HostIdentityProvider {
	return &HostIdentityProvider{homeDir: homeDir}
}

// GetIdentity returns the git user identity and URL rewrite rules from
// the host's ~/.gitconfig. Returns a zero Identity (not an error) if
// no .gitconfig exists.
func (p *HostIdentityProvider) GetIdentity() (domaingit.Identity, error) {
	home := p.homeDir
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return domaingit.Identity{}, fmt.Errorf("resolving home dir: %w", err)
		}
	}

	data, err := os.ReadFile(filepath.Join(home, ".gitconfig"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return domaingit.Identity{}, nil
		}
		return domaingit.Identity{}, fmt.Errorf("reading .gitconfig: %w", err)
	}

	id := parseUserIdentity(string(data))
	id.URLRewrites = parseURLRewrites(string(data))
	return id, nil
}

// parseUserIdentity extracts the user.name and user.email values from
// a git config file's [user] section.
func parseUserIdentity(content string) domaingit.Identity {
	var id domaingit.Identity
	inUserSection := false

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Check for section header.
		if strings.HasPrefix(trimmed, "[") {
			// Reuse parseSectionName from sanitizer (same package).
			section := parseSectionName(trimmed)
			inUserSection = (section == "user")
			continue
		}

		if !inUserSection {
			continue
		}

		// Extract key = value.
		key, value, ok := parseKeyValue(trimmed)
		if !ok {
			continue
		}

		switch strings.ToLower(key) {
		case "name":
			id.Name = value
		case "email":
			id.Email = value
		}
	}

	return id
}

// parseURLRewrites extracts [url "base"].insteadOf rules from a git
// config file. These rules tell git to replace URL prefixes when
// connecting to remotes (e.g. rewriting HTTPS to SSH for authentication).
func parseURLRewrites(content string) []domaingit.URLRewrite {
	var rewrites []domaingit.URLRewrite
	var currentBase string

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "[") {
			section := parseSectionName(trimmed)
			if section == "url" {
				currentBase = parseSubsection(trimmed)
			} else {
				currentBase = ""
			}
			continue
		}

		if currentBase == "" {
			continue
		}

		key, value, ok := parseKeyValue(trimmed)
		if !ok {
			continue
		}

		if strings.ToLower(key) == "insteadof" && value != "" {
			rewrites = append(rewrites, domaingit.URLRewrite{
				Base:      currentBase,
				InsteadOf: value,
			})
		}
	}

	return rewrites
}

// parseKeyValue splits a git config line into key and value.
// Returns false for comments, blank lines, and lines without =.
func parseKeyValue(line string) (key, value string, ok bool) {
	if line == "" || line[0] == '#' || line[0] == ';' {
		return "", "", false
	}
	idx := strings.IndexByte(line, '=')
	if idx < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:]), true
}
