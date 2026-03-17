// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domaingit "github.com/stacklok/brood-box/pkg/domain/git"
)

func TestHostIdentityProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  string // gitconfig content; empty means no file
		noFile  bool   // if true, don't write any .gitconfig
		wantID  domaingit.Identity
		wantErr bool
	}{
		{
			name: "Complete",
			config: `[user]
	name = Alice Smith
	email = alice@example.com
`,
			wantID: domaingit.Identity{Name: "Alice Smith", Email: "alice@example.com"},
		},
		{
			name: "PartialName",
			config: `[user]
	name = Bob
`,
			wantID: domaingit.Identity{Name: "Bob"},
		},
		{
			name: "PartialEmail",
			config: `[user]
	email = carol@example.com
`,
			wantID: domaingit.Identity{Email: "carol@example.com"},
		},
		{
			name: "NoUserSection",
			config: `[core]
	autocrlf = input
`,
			wantID: domaingit.Identity{},
		},
		{
			name:   "NoFile",
			noFile: true,
			wantID: domaingit.Identity{},
		},
		{
			name: "CaseSensitiveSection",
			config: `[USER]
	name = Eve
	email = eve@example.com
`,
			wantID: domaingit.Identity{Name: "Eve", Email: "eve@example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			homeDir := t.TempDir()
			if !tt.noFile {
				err := os.WriteFile(filepath.Join(homeDir, ".gitconfig"), []byte(tt.config), 0o644)
				require.NoError(t, err)
			}

			provider := NewHostIdentityProvider(homeDir)
			id, err := provider.GetIdentity()

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantID, id)
		})
	}
}

func TestHostIdentityProvider_URLRewrites(t *testing.T) {
	t.Parallel()

	homeDir := t.TempDir()
	err := os.WriteFile(filepath.Join(homeDir, ".gitconfig"), []byte(`[user]
	name = Alice
	email = alice@example.com
[url "git@github.com:"]
	insteadOf = https://github.com/
[url "git@gitlab.com:"]
	insteadOf = https://gitlab.com/
`), 0o644)
	require.NoError(t, err)

	provider := NewHostIdentityProvider(homeDir)
	id, err := provider.GetIdentity()
	require.NoError(t, err)

	assert.Equal(t, "Alice", id.Name)
	assert.Equal(t, "alice@example.com", id.Email)
	require.Len(t, id.URLRewrites, 2)
	assert.Equal(t, "git@github.com:", id.URLRewrites[0].Base)
	assert.Equal(t, "https://github.com/", id.URLRewrites[0].InsteadOf)
	assert.Equal(t, "git@gitlab.com:", id.URLRewrites[1].Base)
	assert.Equal(t, "https://gitlab.com/", id.URLRewrites[1].InsteadOf)
}

func TestParseURLRewrites(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  []domaingit.URLRewrite
	}{
		{
			name:  "no rewrites",
			input: "[core]\n\tautocrlf = input\n",
			want:  nil,
		},
		{
			name:  "single rewrite",
			input: "[url \"git@github.com:\"]\n\tinsteadOf = https://github.com/\n",
			want: []domaingit.URLRewrite{
				{Base: "git@github.com:", InsteadOf: "https://github.com/"},
			},
		},
		{
			name: "multiple rewrites",
			input: "[url \"git@github.com:\"]\n\tinsteadOf = https://github.com/\n" +
				"[url \"git@gitlab.com:\"]\n\tinsteadOf = https://gitlab.com/\n",
			want: []domaingit.URLRewrite{
				{Base: "git@github.com:", InsteadOf: "https://github.com/"},
				{Base: "git@gitlab.com:", InsteadOf: "https://gitlab.com/"},
			},
		},
		{
			name:  "non-url section ignored",
			input: "[remote \"origin\"]\n\turl = https://github.com/org/repo.git\n",
			want:  nil,
		},
		{
			name:  "case insensitive insteadOf key",
			input: "[url \"git@github.com:\"]\n\tInsteadOf = https://github.com/\n",
			want: []domaingit.URLRewrite{
				{Base: "git@github.com:", InsteadOf: "https://github.com/"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseURLRewrites(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseUserIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		wantID domaingit.Identity
	}{
		{
			name:   "EmptyInput",
			input:  "",
			wantID: domaingit.Identity{},
		},
		{
			name: "StandardFormat",
			input: `[user]
	name = John Doe
	email = john@example.com
`,
			wantID: domaingit.Identity{Name: "John Doe", Email: "john@example.com"},
		},
		{
			name: "NoSpacesAroundEquals",
			input: `[user]
	name=Jane
	email=jane@example.com
`,
			wantID: domaingit.Identity{Name: "Jane", Email: "jane@example.com"},
		},
		{
			name: "MultipleSections",
			input: `[core]
	autocrlf = input
[user]
	name = Multi
	email = multi@example.com
[alias]
	co = checkout
`,
			wantID: domaingit.Identity{Name: "Multi", Email: "multi@example.com"},
		},
		{
			name: "CommentsIgnored",
			input: `[user]
	# This is a comment
	name = Commented
	; Another comment
	email = commented@example.com
`,
			wantID: domaingit.Identity{Name: "Commented", Email: "commented@example.com"},
		},
		{
			name: "CaseInsensitiveKeys",
			input: `[user]
	Name = CaseKey
	EMAIL = casekey@example.com
`,
			wantID: domaingit.Identity{Name: "CaseKey", Email: "casekey@example.com"},
		},
		{
			name: "UserSectionCaseInsensitive",
			input: `[User]
	name = MixedCase
	email = mixed@example.com
`,
			wantID: domaingit.Identity{Name: "MixedCase", Email: "mixed@example.com"},
		},
		{
			name: "OnlyName",
			input: `[user]
	name = NameOnly
`,
			wantID: domaingit.Identity{Name: "NameOnly"},
		},
		{
			name: "OnlyEmail",
			input: `[user]
	email = emailonly@example.com
`,
			wantID: domaingit.Identity{Email: "emailonly@example.com"},
		},
		{
			name: "ExtraKeysIgnored",
			input: `[user]
	name = Extra
	email = extra@example.com
	signingkey = ABC123
`,
			wantID: domaingit.Identity{Name: "Extra", Email: "extra@example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := parseUserIdentity(tt.input)
			assert.Equal(t, tt.wantID, got)
		})
	}
}
