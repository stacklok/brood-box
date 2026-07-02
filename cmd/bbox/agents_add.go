// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	infraconfig "github.com/stacklok/brood-box/internal/infra/config"
	domainagent "github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/bytesize"
	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
	"github.com/stacklok/brood-box/pkg/domain/egress"
)

// agentAddFlags collects the `bbox agents add` flag values.
type agentAddFlags struct {
	cfgPath         string
	image           string
	command         []string
	description     string
	envForward      []string
	envRequired     []string
	memory          string
	tmpSize         string
	cpus            uint32
	egressProfile   string
	allowHosts      []string
	mcp             bool
	mcpAuthzProfile string
	force           bool
	jsonOut         bool
}

func agentsAddCmd() *cobra.Command {
	var f agentAddFlags
	cmd := &cobra.Command{
		Use:   "add <name> --image IMAGE --command CMD [flags]",
		Short: "Add a custom (bring-your-own) agent to the global config",
		Long: `Appends a validated custom agent to the global config file
(~/.config/broodbox/config.yaml, or the path given by --config), then runs the
same validation the loader uses and prints the result.

Custom agents are GLOBAL-ONLY: this command never writes to a workspace
.broodbox.yaml. Existing comments/formatting in the config are preserved; the
added agent block is written as normalized YAML.

Custom agents are safe by default: no host env is forwarded unless you pass
--env, and the egress profile defaults to "standard". A non-permissive profile
requires hosts, so declare --allow-host, pass --egress-profile permissive, or
enable --mcp (which routes tool access through the proxy) — otherwise validation
fails.

Refuses to overwrite a built-in or an existing custom agent unless --force.`,
		Example: `  bbox agents add aider --image ghcr.io/acme/aider-bbox:latest --command aider --env OPENAI_API_KEY --mcp
  bbox agents add tool --image ghcr.io/acme/tool:latest --command tool --egress-profile permissive
  bbox agents doctor aider`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentsAdd(cmd, args[0], f)
		},
	}
	cmd.Flags().StringVar(&f.cfgPath, "config", "", "Config file path (default: ~/.config/broodbox/config.yaml)")
	cmd.Flags().StringVar(&f.image, "image", "", "OCI image reference for the agent (required)")
	cmd.Flags().StringArrayVar(&f.command, "command", nil, "Entrypoint command; repeat for multiple args (required)")
	cmd.Flags().StringVar(&f.description, "description", "", "Human-readable description")
	cmd.Flags().StringArrayVar(&f.envForward, "env", nil, "Host env var name or glob to forward into the VM (repeatable)")
	cmd.Flags().StringArrayVar(&f.envRequired, "env-required", nil, "Host env var name that must be present to run (repeatable)")
	cmd.Flags().StringVar(&f.memory, "memory", "", "RAM for the VM, e.g. \"4g\" or \"512m\"")
	cmd.Flags().StringVar(&f.tmpSize, "tmp-size", "", "/tmp tmpfs size, e.g. \"512m\" or \"2g\"")
	cmd.Flags().Uint32Var(&f.cpus, "cpus", 0, "vCPU count")
	cmd.Flags().StringVar(&f.egressProfile, "egress-profile", "", "Egress profile: permissive, standard, locked (default: standard)")
	cmd.Flags().StringArrayVar(&f.allowHosts, "allow-host", nil, "Allowed egress DNS hostname[:port] — no IP addresses (repeatable)")
	cmd.Flags().BoolVar(&f.mcp, "mcp", false, "Enable the MCP proxy (mode=env: agent discovers it via BBOX_MCP_URL)")
	cmd.Flags().StringVar(&f.mcpAuthzProfile, "mcp-authz-profile", "", "MCP authz profile: full-access, observe, safe-tools (default: safe-tools when --mcp)")
	cmd.Flags().BoolVarP(&f.force, "force", "f", false, "Overwrite a built-in or existing custom agent")
	cmd.Flags().BoolVar(&f.jsonOut, "json", false, "Emit a JSON receipt of the mutation instead of human-readable output")
	_ = cmd.MarkFlagRequired("image")
	_ = cmd.MarkFlagRequired("command")
	return cmd
}

func runAgentsAdd(cmd *cobra.Command, name string, f agentAddFlags) error {
	// Validate flag values that ValidateCustomAgent does not cover, so the user
	// gets a precise error before anything is written.
	if f.egressProfile != "" && !egress.ProfileName(f.egressProfile).IsValid() {
		return fmt.Errorf("invalid --egress-profile %q: valid values are %v", f.egressProfile, egress.ValidProfiles())
	}
	if f.mcpAuthzProfile != "" && !domainconfig.IsValidMCPAuthzProfile(f.mcpAuthzProfile) {
		return fmt.Errorf("invalid --mcp-authz-profile %q: valid values are %v", f.mcpAuthzProfile, domainconfig.ValidMCPAuthzProfiles())
	}

	override, err := buildAddAgentOverride(f)
	if err != nil {
		return err
	}

	// Full custom-agent validation up front — never write an invalid agent.
	if err := domainconfig.ValidateCustomAgent(name, override, imageRefValidator); err != nil {
		return fmt.Errorf("invalid agent %q: %w", name, err)
	}

	// Built-ins are not stored in the config file, so UpsertAgent cannot detect
	// the collision — gate it here.
	if _, isBuiltin := builtinNames()[name]; isBuiltin && !f.force {
		return fmt.Errorf("%q is a built-in agent; refusing to overwrite (use --force to write an override)", name)
	}

	path := f.cfgPath
	if path == "" {
		path = infraconfig.NewLoader("").Path()
	}

	res, err := infraconfig.UpsertAgent(path, name, override, f.force)
	if err != nil {
		if errors.Is(err, infraconfig.ErrAgentExists) {
			return fmt.Errorf("agent %q already exists in %s (use --force to overwrite)", name, path)
		}
		return err
	}

	receipt := buildAddReceipt(name, override, path, res, os.LookupEnv)
	return emitAddResult(cmd.OutOrStdout(), receipt, f.jsonOut)
}

// buildAddAgentOverride maps the add flags into an AgentOverride. It is pure
// (egress.ParseHostFlag and bytesize.ParseByteSize are pure string parsing) so
// it can be table-tested. It intentionally leaves EgressProfile empty when the
// user did not pass one, so AgentFromOverride applies the custom-agent default
// ("standard"); --allow-host entries are filed under the effective profile so
// ValidateCustomAgent's "non-permissive profile needs hosts" gate is satisfied.
func buildAddAgentOverride(f agentAddFlags) (domainconfig.AgentOverride, error) {
	o := domainconfig.AgentOverride{
		Image:         f.image,
		Description:   f.description,
		Command:       append([]string(nil), f.command...),
		EnvForward:    append([]string(nil), f.envForward...),
		EnvRequired:   append([]string(nil), f.envRequired...),
		EgressProfile: f.egressProfile,
	}

	effectiveProfile := f.egressProfile
	if effectiveProfile == "" {
		effectiveProfile = string(domainconfig.DefaultCustomAgentEgressProfile)
	}
	if len(f.allowHosts) > 0 {
		cfgs := make([]domainconfig.EgressHostConfig, 0, len(f.allowHosts))
		for _, h := range f.allowHosts {
			parsed, err := egress.ParseHostFlag(h)
			if err != nil {
				return domainconfig.AgentOverride{}, fmt.Errorf("--allow-host %q: %w", h, err)
			}
			cfgs = append(cfgs, domainconfig.EgressHostConfig{
				Name: parsed.Name, Ports: parsed.Ports, Protocol: parsed.Protocol,
			})
		}
		o.EgressHosts = map[string][]domainconfig.EgressHostConfig{effectiveProfile: cfgs}
	}

	if f.cpus > 0 {
		o.CPUs = f.cpus
	}
	if f.memory != "" {
		parsed, err := bytesize.ParseByteSize(f.memory)
		if err != nil {
			return domainconfig.AgentOverride{}, fmt.Errorf("--memory: %w", err)
		}
		o.Memory = parsed
	}
	if f.tmpSize != "" {
		parsed, err := bytesize.ParseByteSize(f.tmpSize)
		if err != nil {
			return domainconfig.AgentOverride{}, fmt.Errorf("--tmp-size: %w", err)
		}
		o.TmpSize = parsed
	}

	// --mcp forces mode=env: a custom agent has no config-file injector, so it
	// discovers the proxy only via BBOX_MCP_URL. Enabled is set explicitly so
	// the persisted config states the intent unambiguously.
	if f.mcp {
		enabled := true
		o.MCP = &domainconfig.MCPAgentOverride{
			Enabled: &enabled,
			Mode:    domainconfig.MCPModeEnv,
		}
		if f.mcpAuthzProfile != "" {
			o.MCP.Authz = &domainconfig.MCPAuthzConfig{Profile: f.mcpAuthzProfile}
		}
	}

	return o, nil
}

func agentsInitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init [name]",
		Short: "Print a commented starter agents: stanza to fill in",
		Long: `Prints a fully commented custom-agent stanza to stdout, ready to paste
into your global config (~/.config/broodbox/config.yaml). When a name is given
it is substituted for the example agent key.

This never writes any file — pipe or paste it yourself, or use
'bbox agents add' to append a validated agent non-interactively.`,
		Example: `  bbox agents init
  bbox agents init aider >> ~/.config/broodbox/config.yaml`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
				if err := domainagent.ValidateName(name); err != nil {
					return fmt.Errorf("invalid agent name: %w", err)
				}
			}
			_, _ = io.WriteString(cmd.OutOrStdout(), infraconfig.AgentStarterStanza(name))
			return nil
		},
	}
	return cmd
}
