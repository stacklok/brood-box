// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

// MCP config-file injection formats supported by the declarative
// mcp.mode:config injector. These are the serialization formats a custom
// (bring-your-own) agent's config file may use.
const (
	MCPInjectFormatJSON  = "json"
	MCPInjectFormatJSONC = "jsonc"
	MCPInjectFormatTOML  = "toml"
	MCPInjectFormatYAML  = "yaml"
)

// IsValidMCPInjectFormat reports whether format is a supported declarative
// MCP config-file injection format.
func IsValidMCPInjectFormat(format string) bool {
	switch format {
	case MCPInjectFormatJSON, MCPInjectFormatJSONC, MCPInjectFormatTOML, MCPInjectFormatYAML:
		return true
	default:
		return false
	}
}

// MCPInjectEntry is a single resolved declarative config-file patch for a
// custom agent running with mcp.mode:config. It is pure domain data: the
// infrastructure injector reads the file at GuestPath (relative to the guest
// home), deep-merges the Merge tree on top (after substituting ${BBOX_*}
// environment references), and writes the result back in Format.
//
// This is the resolved counterpart of the config-layer MCPInjectConfig; the
// mapping (config → agent) lives in pkg/domain/config.AgentFromOverride so the
// agent package stays free of YAML concerns.
type MCPInjectEntry struct {
	// GuestPath is the config file to patch, relative to the sandbox user's
	// home directory inside the guest (e.g. ".config/aider/mcp.json").
	GuestPath string

	// Format is the serialization format: one of the MCPInjectFormat*
	// constants.
	Format string

	// Merge is the config tree deep-merged into the (possibly pre-existing)
	// guest file. String leaves may reference ${BBOX_*} environment variables
	// (notably ${BBOX_MCP_URL}), resolved against the VM's universal env.
	Merge map[string]any
}
