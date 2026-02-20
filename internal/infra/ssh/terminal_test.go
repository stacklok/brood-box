// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package ssh

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildCommand_EscapesArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		command  []string
		expected string
	}{
		{
			name:     "escapes spaces and quotes",
			command:  []string{"opencode", "--flag", "value with spaces", "it's"},
			expected: ". /etc/profile 2>/dev/null || true && . /etc/sandbox-env 2>/dev/null || true && cd /workspace && exec 'opencode' '--flag' 'value with spaces' 'it'\\''s'",
		},
		{
			name:     "escapes shell metacharacters",
			command:  []string{"cmd", "$(rm -rf /)", "; echo nope"},
			expected: ". /etc/profile 2>/dev/null || true && . /etc/sandbox-env 2>/dev/null || true && cd /workspace && exec 'cmd' '$(rm -rf /)' '; echo nope'",
		},
		{
			name:     "escapes empty args",
			command:  []string{"cmd", ""},
			expected: ". /etc/profile 2>/dev/null || true && . /etc/sandbox-env 2>/dev/null || true && cd /workspace && exec 'cmd' ''",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, buildCommand(tt.command))
		})
	}
}
