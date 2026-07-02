// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	infraconfig "github.com/stacklok/brood-box/internal/infra/config"
	domainagent "github.com/stacklok/brood-box/pkg/domain/agent"
	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
)

// agentReceipt is the machine-readable record emitted by `bbox agents add
// --json` and `bbox agents doctor --json`. It is designed to be safe to paste
// into an issue: environment variables appear by NAME only, never by value,
// and no secret material is ever included.
type agentReceipt struct {
	// Command is the subcommand that produced the receipt ("agents add" or
	// "agents doctor").
	Command string `json:"command"`
	// ConfigPath is the global config file the command read or wrote.
	ConfigPath string `json:"config_path"`
	// ValidatorVersion identifies the custom-agent validation ruleset used.
	ValidatorVersion string `json:"validator_version"`
	// OK is the overall result (validation/doctor passed).
	OK bool `json:"ok"`
	// Agent is the safe summary of the agent's declared configuration.
	Agent receiptAgent `json:"agent"`
	// Write is present only for `agents add`; it records the file mutation.
	Write *receiptWrite `json:"write,omitempty"`
	// Checks is present only for `agents doctor`; it mirrors the printed checks.
	Checks []receiptCheck `json:"checks,omitempty"`
	// NonEffects enumerates the things this command explicitly did NOT do, so a
	// reviewer can confirm the safety envelope without re-deriving it.
	NonEffects []string `json:"non_effects"`
}

// receiptAgent is the safe, value-free summary of an agent's configuration.
type receiptAgent struct {
	Name            string       `json:"name"`
	Type            string       `json:"type"` // "custom" or "built-in"
	Image           string       `json:"image,omitempty"`
	Command         []string     `json:"command,omitempty"`
	Description     string       `json:"description,omitempty"`
	EgressProfile   string       `json:"egress_profile,omitempty"`
	MCPEnabled      *bool        `json:"mcp_enabled,omitempty"`
	MCPMode         string       `json:"mcp_mode,omitempty"`
	MCPAuthzProfile string       `json:"mcp_authz_profile,omitempty"`
	EnvForward      []string     `json:"env_forward,omitempty"`  // names/patterns only
	EnvRequired     []receiptEnv `json:"env_required,omitempty"` // names + presence only
	CredentialPaths []string     `json:"credential_paths,omitempty"`
	SettingsEntries int          `json:"settings_entries"`
}

// receiptEnv reports a required env var by name and whether it is present on
// the host. The value is never read into the receipt.
type receiptEnv struct {
	Name    string `json:"name"`
	Present bool   `json:"present"`
}

// receiptWrite records the config-file mutation for `agents add`.
type receiptWrite struct {
	Created         bool               `json:"created"`
	Replaced        bool               `json:"replaced"`
	CommentHandling string             `json:"comment_handling"`
	Fingerprint     receiptFingerprint `json:"fingerprint"`
}

// receiptFingerprint captures the before/after config hashes.
type receiptFingerprint struct {
	Algorithm string `json:"algorithm"`
	Before    string `json:"before,omitempty"` // empty when the file was created
	After     string `json:"after"`
}

// receiptCheck mirrors a single doctor check.
type receiptCheck struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// buildReceiptAgent builds the safe agent summary from the declared override,
// filling gaps from the resolved domain agent when available (built-ins carry
// their identity in the registry, not the override). lookupEnv is injected so
// the builder stays pure and testable; it reads only presence, never values.
func buildReceiptAgent(
	name string,
	override domainconfig.AgentOverride,
	isBuiltin bool,
	resolved *domainagent.Agent,
	lookupEnv func(string) (string, bool),
) receiptAgent {
	ra := receiptAgent{
		Name:        name,
		Type:        agentType(isBuiltin),
		Image:       override.Image,
		Command:     override.Command,
		Description: override.Description,
		EnvForward:  override.EnvForward,
	}
	if ra.Image == "" && resolved != nil {
		ra.Image = resolved.Image
	}
	if len(ra.Command) == 0 && resolved != nil {
		ra.Command = resolved.Command
	}
	if len(ra.EnvForward) == 0 && resolved != nil {
		ra.EnvForward = resolved.EnvForward
	}

	// Egress profile: explicit override wins; else the custom default for a
	// custom agent; else the resolved built-in profile.
	switch {
	case override.EgressProfile != "":
		ra.EgressProfile = override.EgressProfile
	case !isBuiltin:
		ra.EgressProfile = string(domainconfig.DefaultCustomAgentEgressProfile)
	case resolved != nil:
		ra.EgressProfile = string(resolved.DefaultEgressProfile)
	}

	// MCP: mirror the fields declared on the override, applying the custom-agent
	// safe-tools default the composition root would apply at run time.
	mcpDisabled := false
	if override.MCP != nil {
		ra.MCPMode = override.MCP.Mode
		if override.MCP.Enabled != nil {
			enabled := *override.MCP.Enabled
			ra.MCPEnabled = &enabled
			mcpDisabled = !enabled
		}
		if override.MCP.Authz != nil {
			ra.MCPAuthzProfile = override.MCP.Authz.Profile
		}
	}
	if ra.MCPAuthzProfile == "" && !isBuiltin && !mcpDisabled && override.MCP != nil {
		ra.MCPAuthzProfile = domainconfig.DefaultCustomAgentMCPAuthzProfile
	}

	for _, envName := range override.EnvRequired {
		_, present := lookupEnv(envName)
		ra.EnvRequired = append(ra.EnvRequired, receiptEnv{Name: envName, Present: present})
	}

	if override.Credentials != nil {
		ra.CredentialPaths = override.Credentials.Persist
	} else if resolved != nil {
		ra.CredentialPaths = resolved.CredentialPaths
	}

	ra.SettingsEntries = len(override.Settings)
	if ra.SettingsEntries == 0 && resolved != nil && resolved.SettingsManifest != nil {
		ra.SettingsEntries = len(resolved.SettingsManifest.Entries)
	}

	return ra
}

func agentType(isBuiltin bool) string {
	if isBuiltin {
		return "built-in"
	}
	return "custom"
}

// buildAddReceipt assembles the receipt for a successful `agents add`.
func buildAddReceipt(
	name string,
	override domainconfig.AgentOverride,
	path string,
	res infraconfig.UpsertResult,
	lookupEnv func(string) (string, bool),
) agentReceipt {
	return agentReceipt{
		Command:          "agents add",
		ConfigPath:       path,
		ValidatorVersion: domainconfig.CustomAgentValidatorVersion,
		OK:               true, // add only reaches here after validation passed
		Agent:            buildReceiptAgent(name, override, false, nil, lookupEnv),
		Write: &receiptWrite{
			Created:         res.Created,
			Replaced:        res.Replaced,
			CommentHandling: "existing comments preserved; added agent block normalized",
			Fingerprint: receiptFingerprint{
				Algorithm: "sha256",
				Before:    res.BeforeSHA256,
				After:     res.AfterSHA256,
			},
		},
		NonEffects: []string{
			"no workspace .broodbox.yaml was read or written",
			"no environment variable values were read or written (env recorded by name only)",
			"no credential paths were added implicitly",
			"config written with owner-only (0600) permissions",
		},
	}
}

// buildDoctorReceipt assembles the receipt for `agents doctor`.
func buildDoctorReceipt(
	name string,
	override domainconfig.AgentOverride,
	isBuiltin bool,
	path string,
	resolved *domainagent.Agent,
	results []checkResult,
	ok bool,
	lookupEnv func(string) (string, bool),
) agentReceipt {
	checks := make([]receiptCheck, 0, len(results))
	for _, r := range results {
		checks = append(checks, receiptCheck{OK: r.ok, Message: r.message})
	}
	return agentReceipt{
		Command:          "agents doctor",
		ConfigPath:       path,
		ValidatorVersion: domainconfig.CustomAgentValidatorVersion,
		OK:               ok,
		Agent:            buildReceiptAgent(name, override, isBuiltin, resolved, lookupEnv),
		Checks:           checks,
		NonEffects: []string{
			"read-only: no config file was modified",
			"no environment variable values were read (env checked by name/presence only)",
			"no workspace .broodbox.yaml was read or written",
		},
	}
}

// emitReceiptJSON writes the receipt as indented JSON.
func emitReceiptJSON(w io.Writer, receipt agentReceipt) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(receipt); err != nil {
		return fmt.Errorf("encoding receipt: %w", err)
	}
	return nil
}

// emitAddResult prints the add receipt either as JSON or as a human summary.
func emitAddResult(w io.Writer, receipt agentReceipt, jsonOut bool) error {
	if jsonOut {
		return emitReceiptJSON(w, receipt)
	}
	verb := "Added"
	if receipt.Write != nil && receipt.Write.Replaced {
		verb = "Updated"
	}
	a := receipt.Agent
	_, _ = fmt.Fprintf(w, "%s custom agent %q in %s\n", verb, a.Name, receipt.ConfigPath)
	_, _ = fmt.Fprintf(w, "  image:   %s\n", a.Image)
	_, _ = fmt.Fprintf(w, "  command: %s\n", strings.Join(a.Command, " "))
	_, _ = fmt.Fprintf(w, "  egress:  %s\n", a.EgressProfile)
	if a.MCPMode != "" || a.MCPEnabled != nil {
		_, _ = fmt.Fprintf(w, "  mcp:     %s\n", mcpSummary(a))
	}
	if len(a.EnvForward) > 0 {
		_, _ = fmt.Fprintf(w, "  env:     %s\n", strings.Join(a.EnvForward, ", "))
	}
	for _, e := range a.EnvRequired {
		status := "MISSING on host"
		if e.Present {
			status = "present"
		}
		_, _ = fmt.Fprintf(w, "  required env %s: %s\n", e.Name, status)
	}
	_, _ = fmt.Fprintf(w, "Validated OK (validator v%s). Run it with: bbox %s\n", receipt.ValidatorVersion, a.Name)
	_, _ = fmt.Fprintf(w, "Verify anytime with: bbox agents doctor %s\n", a.Name)
	return nil
}

// mcpSummary renders the MCP fields of a receipt agent for human output.
func mcpSummary(a receiptAgent) string {
	state := "enabled"
	if a.MCPEnabled != nil && !*a.MCPEnabled {
		state = "disabled"
	}
	parts := []string{state}
	if a.MCPMode != "" {
		parts = append(parts, "mode="+a.MCPMode)
	}
	if a.MCPAuthzProfile != "" {
		parts = append(parts, "authz="+a.MCPAuthzProfile)
	}
	return strings.Join(parts, ", ")
}

// resolvedAgentOrNil returns the registered domain agent for name, or nil when
// it is not registered (e.g. a custom agent that failed validation). It lets
// the receipt builders enrich built-in summaries from the registry.
func resolvedAgentOrNil(resolved *resolvedRegistry, name string) *domainagent.Agent {
	entry, err := resolved.registry.Get(name)
	if err != nil {
		return nil
	}
	ag := entry.Agent
	return &ag
}
