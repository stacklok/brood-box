// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/bytesize"
	"github.com/stacklok/brood-box/pkg/domain/egress"
	"github.com/stacklok/brood-box/pkg/domain/settings"
)

const (
	// DefaultCustomAgentEgressProfile is the egress profile applied to a
	// custom (bring-your-own) agent when the override does not set one.
	// Custom agents are restricted by default, so they must also declare
	// egress_hosts for this profile (or override it to "permissive").
	DefaultCustomAgentEgressProfile = egress.ProfileStandard

	// DefaultCustomAgentMCPAuthzProfile is the MCP authorization profile
	// applied to a custom agent when MCP is enabled and no explicit profile
	// is set. Custom agents default to "safe-tools" (built-ins keep
	// "full-access").
	DefaultCustomAgentMCPAuthzProfile = MCPAuthzProfileSafeTools

	// DefaultCustomAgentCPUs is the CPU floor applied to a custom agent when
	// neither the override nor the global defaults specify a value. Mirrors the
	// built-in agents' DefaultCPUs so a copy-pasted custom agent boots instead
	// of being passed 0 vCPUs (which the runner rejects).
	DefaultCustomAgentCPUs = 2

	// DefaultCustomAgentMemory is the memory floor (in MiB) applied to a custom
	// agent when neither the override nor the global defaults specify a value.
	// Mirrors the built-in agents' DefaultMemory.
	DefaultCustomAgentMemory bytesize.ByteSize = 4096

	// CustomAgentValidatorVersion identifies the ruleset that
	// ValidateCustomAgent enforces. It is surfaced in the machine-readable
	// receipt emitted by `bbox agents add`/`doctor` so a recorded result can be
	// tied to the exact checks that produced it. Bump it whenever the checks in
	// ValidateCustomAgent change in a way that could alter a pass/fail outcome.
	CustomAgentValidatorVersion = "1"
)

// AgentFromOverride builds a fully-populated agent.Agent from a custom-agent
// override. It is a pure function (no I/O) so it can be table-tested and
// reused by both the CLI registration path and the `bbox agents` commands.
//
// Resource defaults (CPUs, Memory, TmpSize) follow the same precedence as
// config.Merge: the override value wins when non-zero, otherwise the global
// default applies. EnvForward is copied verbatim (an empty list stays empty,
// per the custom-agent default of forwarding nothing). EgressProfile defaults
// to DefaultCustomAgentEgressProfile when unset. Settings and CredentialPaths
// are only populated when the override declares them.
//
// MCP authorization is not resolved here. The safe-tools default for custom
// agents (DefaultCustomAgentMCPAuthzProfile) is applied at the composition
// root (cmd/bbox/main.go) where the MCP provider is wired, because authz is
// resolved against CLI flags, global config, inferred custom policies, and the
// tighten-only per-agent override — none of which are available in this pure
// mapping function.
func AgentFromOverride(name string, o AgentOverride, defaults DefaultsConfig) (agent.Agent, error) {
	result := agent.Agent{
		Name:        name,
		Image:       o.Image,
		Command:     append([]string(nil), o.Command...),
		EnvForward:  append([]string(nil), o.EnvForward...),
		DefaultEnv:  copyStringMap(o.DefaultEnv),
		DefaultCPUs: o.CPUs,
	}

	// Resources: override > global default > zero (go-microvm/agent default).
	if o.CPUs == 0 && defaults.CPUs > 0 {
		result.DefaultCPUs = defaults.CPUs
	}
	if o.Memory > 0 {
		result.DefaultMemory = o.Memory
	} else if defaults.Memory > 0 {
		result.DefaultMemory = defaults.Memory
	}
	if o.TmpSize > 0 {
		result.DefaultTmpSize = o.TmpSize
	} else if defaults.TmpSize > 0 {
		result.DefaultTmpSize = defaults.TmpSize
	}

	// Safe minimum fallback: when neither the override nor the global defaults
	// provide CPUs/Memory, apply the built-in floor so the agent can actually
	// boot. Passing 0 to the runner is rejected ("num_vcpus must be > 0" /
	// "ram_mib must be > 0").
	if result.DefaultCPUs == 0 {
		result.DefaultCPUs = DefaultCustomAgentCPUs
	}
	if result.DefaultMemory == 0 {
		result.DefaultMemory = DefaultCustomAgentMemory
	}

	// Clamp to configured maximums, mirroring config.Merge.
	result.DefaultCPUs, result.DefaultMemory = clampResources(result.DefaultCPUs, result.DefaultMemory)
	result.DefaultTmpSize = clampTmpSize(result.DefaultTmpSize)

	// Egress profile: default to "standard" for custom agents when unset.
	if o.EgressProfile != "" {
		result.DefaultEgressProfile = egress.ProfileName(o.EgressProfile)
	} else {
		result.DefaultEgressProfile = DefaultCustomAgentEgressProfile
	}

	// Egress hosts per profile.
	if len(o.EgressHosts) > 0 {
		hosts := make(map[egress.ProfileName][]egress.Host, len(o.EgressHosts))
		for profile, entries := range o.EgressHosts {
			converted, err := ToEgressHosts(entries)
			if err != nil {
				return agent.Agent{}, fmt.Errorf("egress_hosts[%q]: %w", profile, err)
			}
			hosts[egress.ProfileName(profile)] = converted
		}
		result.EgressHosts = hosts
	}

	// Credential paths (off unless declared).
	if o.Credentials != nil && len(o.Credentials.Persist) > 0 {
		result.CredentialPaths = append([]string(nil), o.Credentials.Persist...)
	}

	// Settings manifest (off unless declared).
	if len(o.Settings) > 0 {
		manifest, err := settingsManifestFromConfig(o.Settings)
		if err != nil {
			return agent.Agent{}, err
		}
		result.SettingsManifest = manifest
	}

	return result, nil
}

// settingsManifestFromConfig maps the YAML settings entries into a
// settings.Manifest. Returns an error on an unknown kind.
func settingsManifestFromConfig(entries []AgentSettingsEntryConfig) (*settings.Manifest, error) {
	out := make([]settings.Entry, 0, len(entries))
	for i, e := range entries {
		kind, err := entryKindFromString(e.Kind)
		if err != nil {
			return nil, fmt.Errorf("settings[%d]: %w", i, err)
		}
		guestPath := e.GuestPath
		if guestPath == "" {
			guestPath = e.HostPath
		}
		entry := settings.Entry{
			Category:  e.Category,
			HostPath:  e.HostPath,
			GuestPath: guestPath,
			Kind:      kind,
			Optional:  e.Optional,
			Format:    e.Format,
		}
		if kind == settings.KindMergeFile && len(e.AllowKeys) > 0 {
			entry.Filter = &settings.FieldFilter{
				AllowKeys: append([]string(nil), e.AllowKeys...),
			}
		}
		out = append(out, entry)
	}
	return &settings.Manifest{Entries: out}, nil
}

// entryKindFromString maps a YAML kind string to a settings.EntryKind.
func entryKindFromString(kind string) (settings.EntryKind, error) {
	switch kind {
	case "", "file":
		return settings.KindFile, nil
	case "directory":
		return settings.KindDirectory, nil
	case "merge-file":
		return settings.KindMergeFile, nil
	default:
		return 0, fmt.Errorf("unknown settings kind %q: valid values are file, directory, merge-file", kind)
	}
}

// ValidateCustomAgent runs all load-time, pure checks for a custom agent
// declared in config. It never performs I/O. The optional imageRefValidator
// is invoked on the image reference when non-nil — the composition root
// supplies a closure wrapping go-containerregistry's name.ParseReference so
// the domain layer stays free of that dependency.
//
// Checks: valid agent name; non-empty image (and parseable when a validator
// is supplied); non-empty command; valid env_forward patterns; well-formed
// env_required names; safe relative credential and settings paths; valid
// settings kinds/formats; valid egress hostnames; a hosts list for any
// non-permissive effective egress profile; and a supported MCP mode.
func ValidateCustomAgent(name string, o AgentOverride, imageRefValidator func(string) error) error {
	if err := agent.ValidateName(name); err != nil {
		return err
	}
	if o.Image == "" {
		return fmt.Errorf("agent %q: image is required", name)
	}
	if imageRefValidator != nil {
		if err := imageRefValidator(o.Image); err != nil {
			return fmt.Errorf("agent %q: invalid image reference %q: %w", name, o.Image, err)
		}
	}
	if len(o.Command) == 0 {
		return fmt.Errorf("agent %q: command is required", name)
	}
	if err := agent.ValidateEnvForwardPatterns(o.EnvForward); err != nil {
		return fmt.Errorf("agent %q: %w", name, err)
	}
	for i, envName := range o.EnvRequired {
		if strings.TrimSpace(envName) == "" {
			return fmt.Errorf("agent %q: env_required[%d]: empty name", name, i)
		}
		if strings.Contains(envName, "=") {
			return fmt.Errorf("agent %q: env_required[%d]: name %q must not contain '='", name, i, envName)
		}
	}

	// MCP mode.
	if o.MCP != nil {
		if o.MCP.Mode == MCPModeConfig {
			return fmt.Errorf("agent %q: mcp.mode: config not supported in this version", name)
		}
		if !IsValidMCPMode(o.MCP.Mode) {
			return fmt.Errorf("agent %q: mcp.mode %q: valid values are %q", name, o.MCP.Mode, MCPModeEnv)
		}
	}

	// Credential paths must be safe relative paths.
	if o.Credentials != nil {
		for i, p := range o.Credentials.Persist {
			if err := safeRelPath(p); err != nil {
				return fmt.Errorf("agent %q: credentials.persist[%d]: %w", name, i, err)
			}
		}
	}

	// Settings entries: safe paths + valid kind/format.
	for i, e := range o.Settings {
		if err := safeRelPath(e.HostPath); err != nil {
			return fmt.Errorf("agent %q: settings[%d].host_path: %w", name, i, err)
		}
		guestPath := e.GuestPath
		if guestPath == "" {
			guestPath = e.HostPath
		}
		if err := safeRelPath(guestPath); err != nil {
			return fmt.Errorf("agent %q: settings[%d].guest_path: %w", name, i, err)
		}
		if _, err := entryKindFromString(e.Kind); err != nil {
			return fmt.Errorf("agent %q: settings[%d]: %w", name, i, err)
		}
	}

	// Egress hosts: validate every hostname.
	for profile, entries := range o.EgressHosts {
		if _, err := ToEgressHosts(entries); err != nil {
			return fmt.Errorf("agent %q: egress_hosts[%q]: %w", name, profile, err)
		}
	}

	// Effective egress profile must have hosts unless permissive.
	//
	// Exception: when mcp.mode == env, the MCP proxy is the agent's network
	// discovery path (it reaches the tools it needs via BBOX_MCP_URL through
	// the proxy), so the agent may legitimately declare no egress_hosts. The
	// runtime safety net still holds: egress.Resolve (pkg/domain/egress) is
	// the authoritative check at VM start time and rejects a hostless
	// non-permissive profile there, so loosening this load-time gate does not
	// let a hostless standard-config boot.
	effectiveProfile := DefaultCustomAgentEgressProfile
	if o.EgressProfile != "" {
		effectiveProfile = egress.ProfileName(o.EgressProfile)
	}
	mcpModeEnv := o.MCP != nil && o.MCP.Mode == MCPModeEnv
	if effectiveProfile != egress.ProfilePermissive && !mcpModeEnv {
		if len(o.EgressHosts[string(effectiveProfile)]) == 0 {
			return fmt.Errorf(
				"agent %q: egress profile %q requires egress_hosts[%q] (or set egress_profile: permissive)",
				name, effectiveProfile, effectiveProfile)
		}
	}

	return nil
}

// safeRelPath validates that p is a non-empty relative path that does not
// escape the sandbox home directory: no leading "/", no ".." element, and not
// "." after cleaning. This is the single source of truth for path safety on
// custom-agent credential and settings paths.
func safeRelPath(p string) error {
	if p == "" {
		return fmt.Errorf("empty path")
	}
	if strings.HasPrefix(p, "/") {
		return fmt.Errorf("absolute path %q not allowed (must be relative to home)", p)
	}
	cleaned := filepath.Clean(p)
	if cleaned == "." {
		return fmt.Errorf("path %q resolves to the home directory itself", p)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("path %q escapes the home directory", p)
	}
	for _, seg := range strings.Split(filepath.ToSlash(cleaned), "/") {
		if seg == ".." {
			return fmt.Errorf("path %q contains a %q element", p, "..")
		}
	}
	return nil
}

// copyStringMap returns a shallow copy of m, or nil when m is empty.
func copyStringMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
