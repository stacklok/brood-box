// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

// Universal BBOX_* environment variable names injected into every sandbox VM.
// These give an agent (especially a custom/bring-your-own agent with no
// built-in plugin) a stable, documented way to discover its runtime context
// without parsing config or relying on forwarded host variables.
const (
	// EnvBBOXAgentName is the resolved agent name.
	EnvBBOXAgentName = "BBOX_AGENT_NAME"

	// EnvBBOXWorkspace is the guest path to the mounted workspace.
	EnvBBOXWorkspace = "BBOX_WORKSPACE"

	// EnvBBOXHome is the sandbox user's home directory inside the guest.
	EnvBBOXHome = "BBOX_HOME"

	// EnvBBOXSessionID is the unique session identifier for this run.
	EnvBBOXSessionID = "BBOX_SESSION_ID"

	// EnvBBOXGitTokenAvailable is "1" when a GitHub token is available to the
	// guest's git credential helper, "0" otherwise.
	EnvBBOXGitTokenAvailable = "BBOX_GIT_TOKEN_AVAILABLE"

	// EnvBBOXSSHAgentAvailable is "1" when SSH agent forwarding is active,
	// "0" otherwise.
	EnvBBOXSSHAgentAvailable = "BBOX_SSH_AGENT_AVAILABLE"

	// EnvBBOXMCPURL is the base URL of the in-VM MCP proxy. Set only when the
	// MCP proxy is enabled.
	EnvBBOXMCPURL = "BBOX_MCP_URL"

	// EnvBBOXMCPAuthzProfile is the effective MCP authorization profile. Set
	// only when the MCP proxy is enabled.
	EnvBBOXMCPAuthzProfile = "BBOX_MCP_AUTHZ_PROFILE"
)

// UniversalEnvInput carries the primitive inputs needed to build the universal
// BBOX_* environment variables. Keeping it a flat value type keeps
// BuildUniversalEnv pure and trivially table-testable.
type UniversalEnvInput struct {
	// AgentName is the resolved agent name.
	AgentName string

	// Workspace is the guest path to the mounted workspace.
	Workspace string

	// Home is the sandbox user's home directory inside the guest.
	Home string

	// SessionID is the unique session identifier for this run.
	SessionID string

	// GitTokenAvailable reports whether a GitHub token reaches the guest.
	GitTokenAvailable bool

	// SSHAgentAvailable reports whether SSH agent forwarding is active.
	SSHAgentAvailable bool

	// MCPURL is the base URL of the in-VM MCP proxy. Empty means the MCP
	// proxy is disabled, in which case the BBOX_MCP_* keys are omitted.
	MCPURL string

	// MCPAuthzProfile is the effective MCP authorization profile. Only used
	// when MCPURL is non-empty.
	MCPAuthzProfile string
}

// BuildUniversalEnv returns the universal BBOX_* environment variables for a
// sandbox run. It is a pure function: the returned map contains exactly the
// BBOX_* keys and nothing else.
//
// The MCP keys (BBOX_MCP_URL, BBOX_MCP_AUTHZ_PROFILE) are included only when
// in.MCPURL is non-empty, so a disabled MCP proxy produces no MCP env vars.
//
// Callers should apply this map authoritatively — after forwarded host
// variables — so an untrusted host env cannot clobber BBOX_* values.
func BuildUniversalEnv(in UniversalEnvInput) map[string]string {
	env := map[string]string{
		EnvBBOXAgentName:         in.AgentName,
		EnvBBOXWorkspace:         in.Workspace,
		EnvBBOXHome:              in.Home,
		EnvBBOXSessionID:         in.SessionID,
		EnvBBOXGitTokenAvailable: boolEnv(in.GitTokenAvailable),
		EnvBBOXSSHAgentAvailable: boolEnv(in.SSHAgentAvailable),
	}
	if in.MCPURL != "" {
		env[EnvBBOXMCPURL] = in.MCPURL
		env[EnvBBOXMCPAuthzProfile] = in.MCPAuthzProfile
	}
	return env
}

// boolEnv renders a bool as the canonical "1"/"0" env string.
func boolEnv(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
