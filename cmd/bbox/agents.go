// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/stacklok/brood-box/pkg/clients"
	domainagent "github.com/stacklok/brood-box/pkg/domain/agent"
	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
	"github.com/stacklok/brood-box/pkg/domain/egress"
	"github.com/stacklok/brood-box/pkg/domain/settings"
)

// agentsCmd is the `bbox agents` command group: list, inspect, doctor.
func agentsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agents",
		Short: "Inspect and validate available agents",
	}
	cmd.AddCommand(agentsListCmd())
	cmd.AddCommand(agentsInspectCmd())
	cmd.AddCommand(agentsDoctorCmd())
	return cmd
}

func agentsListCmd() *cobra.Command {
	var cfgPath string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available agents (built-in and custom)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runAgentsList(cmd, cfgPath)
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", "Config file path (default: ~/.config/broodbox/config.yaml)")
	return cmd
}

// runAgentsList resolves the registry (built-ins + custom agents from config)
// and prints each agent's name and image. Shared by `bbox list` and
// `bbox agents list`.
func runAgentsList(cmd *cobra.Command, cfgPath string) error {
	ws, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting current directory: %w", err)
	}
	resolved, err := buildResolvedRegistry(cfgPath, ws, slog.Default(), io.Discard)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	for _, e := range resolved.registry.List() {
		_, _ = fmt.Fprintf(out, "%-15s %s\n", e.Agent.Name, e.Agent.Image)
	}
	return nil
}

func agentsInspectCmd() *cobra.Command {
	var cfgPath string
	cmd := &cobra.Command{
		Use:   "inspect <name>",
		Short: "Show an agent's resolved configuration and field sources",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentsInspect(cmd, cfgPath, args[0])
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", "Config file path (default: ~/.config/broodbox/config.yaml)")
	return cmd
}

func runAgentsInspect(cmd *cobra.Command, cfgPath, agentName string) error {
	ws, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting current directory: %w", err)
	}
	resolved, err := buildResolvedRegistry(cfgPath, ws, slog.Default(), io.Discard)
	if err != nil {
		return err
	}

	entry, err := resolved.registry.Get(agentName)
	if err != nil {
		// Not registered. If it is a custom agent declared in the merged config
		// (skipped during registration because it failed validation), build a
		// best-effort view from the override so the user can still see what they
		// declared. Direct them to `agents doctor` for the failure detail.
		override, declared := resolved.merged.Agents[agentName]
		if !declared {
			return fmt.Errorf("unknown agent %q", agentName)
		}
		ag, mapErr := domainconfig.AgentFromOverride(agentName, override, resolved.merged.Defaults)
		if mapErr != nil {
			return fmt.Errorf("agent %q is misconfigured: %w (run 'bbox agents doctor %s' for details)", agentName, mapErr, agentName)
		}
		out := cmd.OutOrStdout()
		_, _ = fmt.Fprintf(out, "Warning: agent %q is not registered (failed validation) — run 'bbox agents doctor %s' for details\n\n", agentName, agentName)
		renderInspect(out, ag, resolved, agentName)
		return nil
	}

	out := cmd.OutOrStdout()
	renderInspect(out, entry.Agent, resolved, agentName)
	return nil
}

// renderInspect prints the resolved agent fields with provenance. Security:
// environment VALUES are never printed — only variable names/patterns and a
// present/missing indicator for required vars.
func renderInspect(w io.Writer, ag domainagent.Agent, resolved *resolvedRegistry, agentName string) {
	_, isBuiltin := builtinNames()[agentName]
	src := func() string {
		if isBuiltin {
			return "built-in"
		}
		return "global"
	}

	override := resolved.merged.Agents[agentName]

	_, _ = fmt.Fprintf(w, "Name:        %s\n", ag.Name)
	_, _ = fmt.Fprintf(w, "Image:       %s  [%s]\n", ag.Image, fieldSourceImage(agentName, resolved, isBuiltin))
	_, _ = fmt.Fprintf(w, "Command:     %s  [%s]\n", strings.Join(ag.Command, " "), src())
	if override.Description != "" {
		_, _ = fmt.Fprintf(w, "Description: %s\n", override.Description)
	}

	// Env: names/patterns only.
	_, _ = fmt.Fprintf(w, "Env forward: %s\n", joinOrNone(ag.EnvForward))
	_, _ = fmt.Fprintf(w, "Default env: %s\n", joinOrNone(sortedKeys(ag.DefaultEnv)))
	if len(override.EnvRequired) > 0 {
		_, _ = fmt.Fprintf(w, "Required env:\n")
		for _, name := range override.EnvRequired {
			_, present := os.LookupEnv(name)
			status := "MISSING"
			if present {
				status = "present"
			}
			_, _ = fmt.Fprintf(w, "  %-30s %s\n", name, status)
		}
	}

	// Resources.
	_, _ = fmt.Fprintf(w, "Resources:   %d CPUs, %s memory, %s /tmp\n",
		ag.DefaultCPUs, ag.DefaultMemory, ag.DefaultTmpSize)

	// Egress.
	_, _ = fmt.Fprintf(w, "Egress:      profile=%s\n", ag.DefaultEgressProfile)
	for _, profile := range sortedProfiles(ag.EgressHosts) {
		names := make([]string, 0, len(ag.EgressHosts[profile]))
		for _, h := range ag.EgressHosts[profile] {
			names = append(names, h.Name)
		}
		_, _ = fmt.Fprintf(w, "  %-12s %s\n", string(profile)+":", strings.Join(names, ", "))
	}

	// MCP.
	mode := ""
	authz := ""
	enabled := "inherit"
	mcpDisabled := false
	if override.MCP != nil {
		mode = override.MCP.Mode
		if override.MCP.Enabled != nil {
			if *override.MCP.Enabled {
				enabled = "true"
			} else {
				enabled = "false"
				mcpDisabled = true
			}
		}
		if override.MCP.Authz != nil {
			authz = override.MCP.Authz.Profile
		}
	}
	// A custom agent with MCP enabled and no explicit authz runs under the
	// safe-tools default applied at the composition root (built-ins keep
	// full-access). Surface that effective default here so inspect reflects
	// the profile the agent will actually run with.
	if authz == "" && !isBuiltin && !mcpDisabled {
		authz = domainconfig.DefaultCustomAgentMCPAuthzProfile + " (default)"
	}
	_, _ = fmt.Fprintf(w, "MCP:         enabled=%s mode=%q authz=%q\n", enabled, mode, authz)

	// Credential paths.
	_, _ = fmt.Fprintf(w, "Credentials: %s\n", joinOrNone(ag.CredentialPaths))

	// Settings mappings.
	if ag.SettingsManifest != nil && len(ag.SettingsManifest.Entries) > 0 {
		_, _ = fmt.Fprintf(w, "Settings:\n")
		for _, e := range ag.SettingsManifest.Entries {
			_, _ = fmt.Fprintf(w, "  [%s] %s -> %s (%s%s%s)\n",
				e.Category, e.HostPath, e.GuestPath, kindString(e.Kind),
				formatSuffix(e.Format), optionalSuffix(e.Optional))
		}
	} else {
		_, _ = fmt.Fprintf(w, "Settings:    (none)\n")
	}
}

func agentsDoctorCmd() *cobra.Command {
	var cfgPath string
	cmd := &cobra.Command{
		Use:   "doctor <name>",
		Short: "Validate an agent's configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentsDoctor(cmd, cfgPath, args[0])
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", "Config file path (default: ~/.config/broodbox/config.yaml)")
	return cmd
}

func runAgentsDoctor(cmd *cobra.Command, cfgPath, agentName string) error {
	ws, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getting current directory: %w", err)
	}
	resolved, err := buildResolvedRegistry(cfgPath, ws, slog.Default(), io.Discard)
	if err != nil {
		return err
	}

	// A misconfigured custom agent is SKIPPED during registration (registerCustomAgents
	// warns and drops it), so it is absent from the registry. Diagnosing exactly
	// that case is the whole point of doctor — so do not hard-gate on the registry.
	// Only reject names that are neither registered nor declared in the merged config.
	_, regErr := resolved.registry.Get(agentName)
	_, declared := resolved.merged.Agents[agentName]
	if regErr != nil && !declared {
		return fmt.Errorf("unknown agent %q", agentName)
	}

	override := resolved.merged.Agents[agentName]
	localAddedAgent := didLocalAddAgent(resolved, agentName)
	localAddedCreds := didLocalAddCredentials(resolved, agentName)
	_, isBuiltin := builtinNames()[agentName]

	results, ok := runDoctor(agentName, override, isBuiltin, os.LookupEnv, imageRefValidator, localAddedAgent, localAddedCreds)

	out := cmd.OutOrStdout()
	for _, r := range results {
		status := "PASS"
		if !r.ok {
			status = "FAIL"
		}
		_, _ = fmt.Fprintf(out, "[%s] %s\n", status, r.message)
	}
	if !ok {
		return errors.New("agent configuration has problems (see above)")
	}
	return nil
}

// checkResult is a single doctor check outcome.
type checkResult struct {
	ok      bool
	message string
}

// runDoctor performs all agent health checks against the resolved override. It
// is pure (the environment and image-ref lookups are injected) so it can be
// exercised directly in tests without touching the real config tree. It
// returns the per-check results and an overall ok flag (false if any FAIL).
//
// The custom-agent identity checks (name/image/command/paths/egress via
// ValidateCustomAgent) only apply to custom (bring-your-own) agents declared
// in config. Built-in agents carry their image/command in the registry entry,
// not in the override, so feeding them through ValidateCustomAgent would
// spuriously fail on the empty Image. For built-ins we skip those checks and
// only run the override-applicable checks (required env, blocked local
// additions).
//
// lookupEnv mirrors os.LookupEnv; imageValidator mirrors imageRefValidator.
// localAddedAgent / localAddedCredentials report whether the workspace-local
// config attempted the (now-blocked) unsafe additions, surfaced as FAILs.
func runDoctor(
	agentName string,
	override domainconfig.AgentOverride,
	isBuiltin bool,
	lookupEnv func(string) (string, bool),
	imageValidator func(string) error,
	localAddedAgent bool,
	localAddedCredentials bool,
) ([]checkResult, bool) {
	var results []checkResult
	add := func(ok bool, msg string) {
		results = append(results, checkResult{ok: ok, message: msg})
	}

	if isBuiltin {
		// Built-in agents are valid registered agents whose identity comes from
		// their plugin/registry entry, not from config. There is no custom-agent
		// definition to validate.
		add(true, "built-in agent: identity provided by the registry (no custom config to validate)")
	} else if err := domainconfig.ValidateCustomAgent(agentName, override, imageValidator); err != nil {
		// The bulk of validation is the same pure check the loader runs.
		add(false, fmt.Sprintf("config validation: %s", err))
	} else {
		add(true, "config validation: name, image, command, paths, egress all valid")
	}

	// Required env presence (names only — values never read into output).
	for _, name := range override.EnvRequired {
		_, present := lookupEnv(name)
		if present {
			add(true, fmt.Sprintf("required env %s present", name))
		} else {
			add(false, fmt.Sprintf("required env %s is missing", name))
		}
	}

	// Security #7 surfacing.
	if localAddedAgent {
		add(false, "workspace .broodbox.yaml attempted to add this agent (ignored)")
	}
	if localAddedCredentials {
		add(false, "workspace .broodbox.yaml attempted to add credential paths (ignored)")
	}

	ok := true
	for _, r := range results {
		if !r.ok {
			ok = false
		}
	}
	return results, ok
}

// fieldSource returns a provenance label by comparing the layered values for a
// field. The layers are checked most-specific first: a non-zero CLI value wins,
// then workspace-local, then global, otherwise built-in. The caller passes
// whether each layer set the field (the comparison is field-specific).
func fieldSource(setByCLI, setByWorkspace, setByGlobal bool) string {
	switch {
	case setByCLI:
		return "CLI"
	case setByWorkspace:
		return "workspace"
	case setByGlobal:
		return "global"
	default:
		return "built-in"
	}
}

// fieldSourceImage attributes the image field for the inspect output.
func fieldSourceImage(agentName string, resolved *resolvedRegistry, isBuiltin bool) string {
	setByGlobal := false
	if o, ok := resolved.global.Agents[agentName]; ok && o.Image != "" {
		setByGlobal = true
	}
	setByWorkspace := false
	if resolved.local != nil {
		if o, ok := resolved.local.Agents[agentName]; ok && o.Image != "" {
			setByWorkspace = true
		}
	}
	if !setByGlobal && !setByWorkspace && isBuiltin {
		return "built-in"
	}
	return fieldSource(false, setByWorkspace, setByGlobal)
}

// didLocalAddAgent reports whether the workspace-local config declared an
// agent key that is absent from the global config (a blocked attempt to add a
// new custom agent).
func didLocalAddAgent(resolved *resolvedRegistry, agentName string) bool {
	if resolved.local == nil {
		return false
	}
	_, inLocal := resolved.local.Agents[agentName]
	if !inLocal {
		return false
	}
	_, inGlobal := resolved.global.Agents[agentName]
	_, isBuiltin := builtinNames()[agentName]
	return !inGlobal && !isBuiltin
}

// didLocalAddCredentials reports whether the workspace-local config declared
// credential paths for an agent (a blocked attempt — local credentials are
// always ignored during merge).
func didLocalAddCredentials(resolved *resolvedRegistry, agentName string) bool {
	if resolved.local == nil {
		return false
	}
	o, ok := resolved.local.Agents[agentName]
	if !ok {
		return false
	}
	return o.Credentials != nil && len(o.Credentials.Persist) > 0
}

// builtinNames returns the set of built-in agent names so the inspect/doctor
// provenance logic can tell built-in agents apart from custom ones.
func builtinNames() map[string]struct{} {
	names := make(map[string]struct{})
	for _, e := range clients.Builtins(slog.Default()) {
		names[e.Agent.Name] = struct{}{}
	}
	return names
}

// --- small rendering helpers ---

func joinOrNone(items []string) string {
	if len(items) == 0 {
		return "(none)"
	}
	return strings.Join(items, ", ")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedProfiles(m map[egress.ProfileName][]egress.Host) []egress.ProfileName {
	profiles := make([]egress.ProfileName, 0, len(m))
	for p := range m {
		profiles = append(profiles, p)
	}
	sort.Slice(profiles, func(i, j int) bool { return profiles[i] < profiles[j] })
	return profiles
}

func kindString(k settings.EntryKind) string {
	switch k {
	case settings.KindDirectory:
		return "directory"
	case settings.KindMergeFile:
		return "merge-file"
	default:
		return "file"
	}
}

func formatSuffix(format string) string {
	if format == "" {
		return ""
	}
	return ", " + format
}

func optionalSuffix(optional bool) string {
	if optional {
		return ", optional"
	}
	return ""
}
