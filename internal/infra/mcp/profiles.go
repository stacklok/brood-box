// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"fmt"
	"strings"

	"github.com/stacklok/brood-box/pkg/domain/config"
)

// observePolicies are the Cedar permit policies for the observe profile.
// They allow listing and reading MCP capabilities but not calling tools.
var observePolicies = []string{
	`permit(principal, action == Action::"list_tools", resource);`,
	`permit(principal, action == Action::"list_prompts", resource);`,
	`permit(principal, action == Action::"list_resources", resource);`,
	`permit(principal, action == Action::"get_prompt", resource);`,
	`permit(principal, action == Action::"read_resource", resource);`,
}

// safeToolsPolicies extend observe with annotation-based tool call permits.
// Tools with readOnlyHint=true are allowed. Tools that are both non-destructive
// (destructiveHint=false) and closed-world (openWorldHint=false) are also allowed.
// Missing annotations cause the when clause to fail, resulting in Cedar's
// default-deny — matching MCP spec conservative defaults without custom code.
var safeToolsPolicies = []string{
	// All observe permits.
	`permit(principal, action == Action::"list_tools", resource);`,
	`permit(principal, action == Action::"list_prompts", resource);`,
	`permit(principal, action == Action::"list_resources", resource);`,
	`permit(principal, action == Action::"get_prompt", resource);`,
	`permit(principal, action == Action::"read_resource", resource);`,
	// Allow read-only tools.
	`permit(principal, action == Action::"call_tool", resource) when { resource.readOnlyHint == true };`,
	// Allow non-destructive AND closed-world tools.
	`permit(principal, action == Action::"call_tool", resource) when { resource.destructiveHint == false && resource.openWorldHint == false };`,
}

// ResolveProfile returns Cedar policy strings for the given authz config.
// A nil return means no authz middleware should be applied (full-access).
// The "custom" profile is not handled here — callers must resolve custom
// policies from the vmcp config YAML before calling this function.
func ResolveProfile(cfg *config.MCPAuthzConfig) ([]string, error) {
	if cfg == nil || cfg.Profile == "" || cfg.Profile == config.MCPAuthzProfileFullAccess {
		return nil, nil
	}

	switch cfg.Profile {
	case config.MCPAuthzProfileObserve:
		policies := make([]string, len(observePolicies))
		copy(policies, observePolicies)
		return policies, nil
	case config.MCPAuthzProfileSafeTools:
		policies := make([]string, len(safeToolsPolicies))
		copy(policies, safeToolsPolicies)
		return policies, nil
	case config.MCPAuthzProfileCustom:
		// Custom is resolved by the provider from the vmcp config YAML.
		// Return an error here because the caller should handle custom
		// before reaching this point.
		return nil, fmt.Errorf("custom profile must be resolved from vmcp config (--mcp-config incomingAuth.authz.policies)")
	default:
		return nil, fmt.Errorf("unknown MCP authz profile: %q (valid: %s)",
			cfg.Profile, strings.Join(config.ValidMCPAuthzProfiles(), ", "))
	}
}
