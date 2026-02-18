// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domaingit "github.com/stacklok/apiary/pkg/domain/git"
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
