// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/brood-box/pkg/domain/config"
)

func TestResolveProfile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		cfg          *config.MCPAuthzConfig
		wantNil      bool
		wantCount    int
		wantErr      string
		wantContains []string
		wantAbsent   []string
	}{
		{
			name:    "nil config returns nil policies (full-access default)",
			cfg:     nil,
			wantNil: true,
		},
		{
			name:    "empty profile returns nil policies (full-access default)",
			cfg:     &config.MCPAuthzConfig{Profile: ""},
			wantNil: true,
		},
		{
			name:    "full-access returns nil policies",
			cfg:     &config.MCPAuthzConfig{Profile: config.MCPAuthzProfileFullAccess},
			wantNil: true,
		},
		{
			name:      "observe returns 5 list/read permits",
			cfg:       &config.MCPAuthzConfig{Profile: config.MCPAuthzProfileObserve},
			wantCount: 5,
			wantContains: []string{
				`Action::"list_tools"`,
				`Action::"list_prompts"`,
				`Action::"list_resources"`,
				`Action::"get_prompt"`,
				`Action::"read_resource"`,
			},
			wantAbsent: []string{
				`Action::"call_tool"`,
			},
		},
		{
			name:      "safe-tools returns 7 policies (5 observe + 2 annotation-based)",
			cfg:       &config.MCPAuthzConfig{Profile: config.MCPAuthzProfileSafeTools},
			wantCount: 7,
			wantContains: []string{
				`Action::"list_tools"`,
				`Action::"call_tool"`,
				`resource.readOnlyHint == true`,
				`resource.destructiveHint == false && resource.openWorldHint == false`,
			},
		},
		{
			name:    "custom profile returns error (must be resolved by provider)",
			cfg:     &config.MCPAuthzConfig{Profile: config.MCPAuthzProfileCustom},
			wantErr: "custom profile must be resolved from vmcp config",
		},
		{
			name:    "unknown profile returns error",
			cfg:     &config.MCPAuthzConfig{Profile: "custom-thing"},
			wantErr: `unknown MCP authz profile: "custom-thing"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			policies, err := ResolveProfile(tt.cfg)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}

			require.NoError(t, err)

			if tt.wantNil {
				assert.Nil(t, policies)
				return
			}

			assert.Len(t, policies, tt.wantCount)

			joined := strings.Join(policies, "\n")

			for _, want := range tt.wantContains {
				assert.Contains(t, joined, want)
			}
			for _, absent := range tt.wantAbsent {
				assert.NotContains(t, joined, absent)
			}
		})
	}
}

func TestResolveProfileDoesNotMutatePackageVars(t *testing.T) {
	t.Parallel()

	// Capture original lengths.
	origObserve := len(observePolicies)
	origSafe := len(safeToolsPolicies)

	// Call resolve for both profiles.
	_, err := ResolveProfile(&config.MCPAuthzConfig{Profile: config.MCPAuthzProfileObserve})
	require.NoError(t, err)
	_, err = ResolveProfile(&config.MCPAuthzConfig{Profile: config.MCPAuthzProfileSafeTools})
	require.NoError(t, err)

	// Package-level slices must be unchanged.
	assert.Len(t, observePolicies, origObserve)
	assert.Len(t, safeToolsPolicies, origSafe)
}
