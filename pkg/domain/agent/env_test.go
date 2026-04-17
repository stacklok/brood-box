// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type staticEnvProvider struct {
	vars []string
}

func (s *staticEnvProvider) Environ() []string {
	return s.vars
}

func TestForwardEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		patterns []string
		env      []string
		want     map[string]string
	}{
		{
			name:     "exact match",
			patterns: []string{"ANTHROPIC_API_KEY"},
			env:      []string{"ANTHROPIC_API_KEY=sk-ant-123", "OTHER_VAR=foo"},
			want:     map[string]string{"ANTHROPIC_API_KEY": "sk-ant-123"},
		},
		{
			name:     "glob suffix match",
			patterns: []string{"CLAUDE_*"},
			env: []string{
				"CLAUDE_CODE_DISABLE_AUTOUPDATE=1",
				"CLAUDE_MODEL=opus",
				"OTHER_VAR=bar",
			},
			want: map[string]string{
				"CLAUDE_CODE_DISABLE_AUTOUPDATE": "1",
				"CLAUDE_MODEL":                   "opus",
			},
		},
		{
			name:     "mixed patterns",
			patterns: []string{"ANTHROPIC_API_KEY", "CLAUDE_*"},
			env: []string{
				"ANTHROPIC_API_KEY=sk-ant-123",
				"CLAUDE_MODEL=opus",
				"HOME=/root",
			},
			want: map[string]string{
				"ANTHROPIC_API_KEY": "sk-ant-123",
				"CLAUDE_MODEL":      "opus",
			},
		},
		{
			name:     "no matches returns nil",
			patterns: []string{"MISSING_*"},
			env:      []string{"FOO=bar"},
			want:     nil,
		},
		{
			name:     "empty patterns returns nil",
			patterns: []string{},
			env:      []string{"FOO=bar"},
			want:     nil,
		},
		{
			name:     "nil patterns returns nil",
			patterns: nil,
			env:      []string{"FOO=bar"},
			want:     nil,
		},
		{
			name:     "value with equals sign",
			patterns: []string{"DSN"},
			env:      []string{"DSN=postgres://user:pass@host/db?sslmode=require"},
			want:     map[string]string{"DSN": "postgres://user:pass@host/db?sslmode=require"},
		},
		{
			name:     "malformed env entry skipped",
			patterns: []string{"FOO"},
			env:      []string{"NOEQUALS", "FOO=bar"},
			want:     map[string]string{"FOO": "bar"},
		},
		{
			name:     "empty value is forwarded",
			patterns: []string{"EMPTY"},
			env:      []string{"EMPTY="},
			want:     map[string]string{"EMPTY": ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			provider := &staticEnvProvider{vars: tt.env}
			got := ForwardEnv(tt.patterns, provider)
			if tt.want == nil {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestValidateEnvForwardPatterns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		patterns []string
		wantErr  bool
		errSub   string // substring the error must contain
	}{
		{name: "empty list is fine", patterns: nil, wantErr: false},
		{name: "exact names pass", patterns: []string{"HOME", "USER", "GITHUB_TOKEN"}, wantErr: false},
		{name: "trailing glob passes", patterns: []string{"AWS_*", "CLAUDE_*"}, wantErr: false},
		{name: "mixed exact and glob", patterns: []string{"HOME", "AWS_*"}, wantErr: false},

		{name: "bare star rejected", patterns: []string{"*"}, wantErr: true, errSub: "bare \"*\""},
		{name: "bare star with surrounding whitespace rejected", patterns: []string{"  *  "}, wantErr: true, errSub: "bare \"*\""},
		{name: "empty string rejected", patterns: []string{""}, wantErr: true, errSub: "empty pattern"},
		{name: "whitespace-only rejected", patterns: []string{"   "}, wantErr: true, errSub: "empty pattern"},
		{name: "leading star rejected", patterns: []string{"*_API_KEY"}, wantErr: true, errSub: "leading-star"},
		{name: "bare star mixed with valid patterns still rejects", patterns: []string{"HOME", "*"}, wantErr: true, errSub: "bare \"*\""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateEnvForwardPatterns(tt.patterns)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errSub != "" {
					assert.Contains(t, err.Error(), tt.errSub)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestMatchPattern_DefensiveBypassGuard(t *testing.T) {
	t.Parallel()

	// Even if ValidateEnvForwardPatterns is bypassed (e.g. by a caller
	// constructing patterns programmatically), matchPattern must NOT
	// let a bare "*" or empty pattern swallow every key.
	keys := []string{"HOME", "GITHUB_TOKEN", "SSH_AUTH_SOCK", "AWS_ACCESS_KEY_ID", ""}
	bad := []string{"*", "", "   ", "  *  "}

	for _, pattern := range bad {
		for _, key := range keys {
			assert.False(t, matchPattern(key, pattern),
				"pattern %q must not match key %q", pattern, key)
		}
	}
}

func TestShellEscape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple value",
			input: "hello",
			want:  "'hello'",
		},
		{
			name:  "value with spaces",
			input: "hello world",
			want:  "'hello world'",
		},
		{
			name:  "value with single quote",
			input: "it's",
			want:  "'it'\\''s'",
		},
		{
			name:  "empty value",
			input: "",
			want:  "''",
		},
		{
			name:  "value with special chars",
			input: "foo$bar`baz\"qux",
			want:  "'foo$bar`baz\"qux'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ShellEscape(tt.input))
		})
	}
}
