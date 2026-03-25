// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package ssh

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExitError_SignalHint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      ExitError
		expected string
	}{
		{
			name:     "normal exit no hint",
			err:      ExitError{Code: 1},
			expected: "",
		},
		{
			name:     "SIGKILL via signal field",
			err:      ExitError{Code: 137, Signal: "KILL"},
			expected: "process was forcefully killed (likely out of memory — try increasing VM memory with --memory)",
		},
		{
			name:     "SIGKILL via exit code 137",
			err:      ExitError{Code: 137},
			expected: "process was forcefully killed (likely out of memory — try increasing VM memory with --memory)",
		},
		{
			name:     "SIGSEGV via signal field",
			err:      ExitError{Code: 139, Signal: "SEGV"},
			expected: "process crashed with a segmentation fault",
		},
		{
			name:     "SIGSEGV via exit code 139",
			err:      ExitError{Code: 139},
			expected: "process crashed with a segmentation fault",
		},
		{
			name:     "SIGABRT via signal field",
			err:      ExitError{Code: 134, Signal: "ABRT"},
			expected: "process aborted (assertion failure or fatal error)",
		},
		{
			name:     "SIGTERM is silent",
			err:      ExitError{Code: 143, Signal: "TERM"},
			expected: "",
		},
		{
			name:     "SIGINT is silent",
			err:      ExitError{Code: 130, Signal: "INT"},
			expected: "",
		},
		{
			name:     "exit code 143 without signal field is silent",
			err:      ExitError{Code: 143},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.err.SignalHint())
		})
	}
}

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
