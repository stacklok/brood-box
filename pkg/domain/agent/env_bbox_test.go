// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildUniversalEnv(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   UniversalEnvInput
		want map[string]string
	}{
		{
			name: "mcp disabled, git and ssh off",
			in: UniversalEnvInput{
				AgentName: "my-agent",
				Workspace: "/workspace",
				Home:      "/home/sandbox",
				SessionID: "abc123",
			},
			want: map[string]string{
				EnvBBOXAgentName:         "my-agent",
				EnvBBOXWorkspace:         "/workspace",
				EnvBBOXHome:              "/home/sandbox",
				EnvBBOXSessionID:         "abc123",
				EnvBBOXGitTokenAvailable: "0",
				EnvBBOXSSHAgentAvailable: "0",
			},
		},
		{
			name: "git and ssh on",
			in: UniversalEnvInput{
				AgentName:         "a",
				Workspace:         "/w",
				Home:              "/h",
				SessionID:         "s",
				GitTokenAvailable: true,
				SSHAgentAvailable: true,
			},
			want: map[string]string{
				EnvBBOXAgentName:         "a",
				EnvBBOXWorkspace:         "/w",
				EnvBBOXHome:              "/h",
				EnvBBOXSessionID:         "s",
				EnvBBOXGitTokenAvailable: "1",
				EnvBBOXSSHAgentAvailable: "1",
			},
		},
		{
			name: "mcp enabled adds url and authz",
			in: UniversalEnvInput{
				AgentName:       "a",
				Workspace:       "/w",
				Home:            "/h",
				SessionID:       "s",
				MCPURL:          "http://192.168.127.1:4483/mcp",
				MCPAuthzProfile: "safe-tools",
			},
			want: map[string]string{
				EnvBBOXAgentName:         "a",
				EnvBBOXWorkspace:         "/w",
				EnvBBOXHome:              "/h",
				EnvBBOXSessionID:         "s",
				EnvBBOXGitTokenAvailable: "0",
				EnvBBOXSSHAgentAvailable: "0",
				EnvBBOXMCPURL:            "http://192.168.127.1:4483/mcp",
				EnvBBOXMCPAuthzProfile:   "safe-tools",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := BuildUniversalEnv(tt.in)
			// Assert exact key set (and nothing else).
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBuildUniversalEnv_NoMCPKeysWhenURLEmpty(t *testing.T) {
	t.Parallel()
	got := BuildUniversalEnv(UniversalEnvInput{
		AgentName:       "a",
		MCPAuthzProfile: "safe-tools", // must be ignored when URL is empty
	})
	_, hasURL := got[EnvBBOXMCPURL]
	_, hasAuthz := got[EnvBBOXMCPAuthzProfile]
	assert.False(t, hasURL, "BBOX_MCP_URL must be absent when MCP is disabled")
	assert.False(t, hasAuthz, "BBOX_MCP_AUTHZ_PROFILE must be absent when MCP is disabled")
}
