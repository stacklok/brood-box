// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	infraconfig "github.com/stacklok/brood-box/internal/infra/config"
	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
)

// agentsImportCmd is `bbox agents import <file>`: read a standalone agent
// manifest, validate it, and append the agent to the global config (reuse the
// exact path `agents add` takes so behavior, receipts, and safety match).
func agentsImportCmd() *cobra.Command {
	var (
		cfgPath string
		force   bool
		jsonOut bool
	)
	cmd := &cobra.Command{
		Use:   "import <manifest>",
		Short: "Import a custom agent from a manifest file into the global config",
		Long: `Reads a standalone agent manifest (a YAML file with a top-level name
plus the same fields as an agents:<name> block), validates it with the same
checks the loader uses, and appends it to the global config
(~/.config/broodbox/config.yaml, or the path given by --config). Existing
comments/formatting in the config are preserved; the added agent block is
written as normalized YAML.

Custom agents are GLOBAL-ONLY: this command never writes to a workspace
.broodbox.yaml. Refuses to overwrite a built-in or an existing custom agent
unless --force.

The manifest format is identical to an agents:<name> entry, with a top-level
name field:

  name: aider
  image: ghcr.io/acme/aider-bbox:latest
  command: ["aider"]
  env_forward: [OPENAI_API_KEY]
  mcp:
    mode: env
  egress_profile: standard
  egress_hosts:
    standard:
      - { name: api.openai.com, ports: [443] }`,
		Example: `  bbox agents import ./broodbox-agent.yaml
  bbox agents import ./aider.yaml --json
  bbox agents import ./aider.yaml --force  # overwrite an existing agent`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentsImport(cmd, args[0], cfgPath, force, jsonOut)
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", "Config file path (default: ~/.config/broodbox/config.yaml)")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Overwrite a built-in or existing custom agent")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit a JSON receipt of the mutation instead of human-readable output")
	return cmd
}

func runAgentsImport(cmd *cobra.Command, manifestPath, cfgPath string, force, jsonOut bool) error {
	manifest, err := infraconfig.LoadManifest(manifestPath)
	if err != nil {
		return err
	}
	if manifest.Name == "" {
		return fmt.Errorf("manifest %s: top-level %q field is required", manifestPath, "name")
	}

	override := manifest.AgentOverride

	// Full custom-agent validation up front — never write an invalid agent.
	if err := domainconfig.ValidateCustomAgent(manifest.Name, override, imageRefValidator); err != nil {
		return fmt.Errorf("invalid agent %q: %w", manifest.Name, err)
	}

	// Built-ins are not stored in the config file, so UpsertAgent cannot detect
	// the collision — gate it here (mirrors `agents add`).
	if _, isBuiltin := builtinNames()[manifest.Name]; isBuiltin && !force {
		return fmt.Errorf("%q is a built-in agent; refusing to overwrite (use --force to write an override)", manifest.Name)
	}

	path := cfgPath
	if path == "" {
		path = infraconfig.NewLoader("").Path()
	}

	res, err := infraconfig.UpsertAgent(path, manifest.Name, override, force)
	if err != nil {
		if errors.Is(err, infraconfig.ErrAgentExists) {
			return fmt.Errorf("agent %q already exists in %s (use --force to overwrite)", manifest.Name, path)
		}
		return err
	}

	receipt := buildAddReceipt(manifest.Name, override, path, res, os.LookupEnv)
	// The receipt Command label reflects the import path so a recorded receipt
	// is distinguishable from an `agents add` receipt.
	receipt.Command = "agents import"
	return emitAddResult(cmd.OutOrStdout(), receipt, jsonOut)
}

// agentsExportCmd is `bbox agents export <name>`: emit a standalone manifest
// for an existing custom agent to stdout. Env VALUES are never emitted —
// DefaultEnv is stripped so only env NAMES/patterns leave the host.
func agentsExportCmd() *cobra.Command {
	var cfgPath string
	cmd := &cobra.Command{
		Use:   "export <name>",
		Short: "Export a custom agent as a standalone manifest to stdout",
		Long: `Writes a standalone agent manifest (the same format ` + "`bbox agents import`" + `
reads) for an existing custom agent to stdout. Built-in agents cannot be
exported — only custom (bring-your-own) agents declared in the global config.

Environment variable VALUES are never written: default_env is stripped from
the export so only env names/patterns leave the host. The exported manifest
re-imports cleanly (export then import is a no-op for the agent fields).`,
		Example: `  bbox agents export aider > aider.broodbox-agent.yaml
  bbox agents export aider --config ~/.config/broodbox/config.yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentsExport(cmd, args[0], cfgPath)
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", "Config file path (default: ~/.config/broodbox/config.yaml)")
	return cmd
}

func runAgentsExport(cmd *cobra.Command, agentName, cfgPath string) error {
	if _, isBuiltin := builtinNames()[agentName]; isBuiltin {
		return fmt.Errorf("%q is a built-in agent; only custom agents can be exported", agentName)
	}

	path := cfgPath
	if path == "" {
		path = infraconfig.NewLoader("").Path()
	}
	loaded, err := infraconfig.NewLoader(path).Load()
	if err != nil {
		return fmt.Errorf("loading config %s: %w", path, err)
	}

	override, declared := loaded.Agents[agentName]
	if !declared {
		return fmt.Errorf("agent %q is not declared in %s (run 'bbox agents list' to see available agents)", agentName, path)
	}

	// Strip default_env: those are host-supplied VALUES, never exported. The
	// rest of the override (env_forward names, env_required names, egress hosts,
	// MCP, credentials, settings) carries no secret material.
	override.DefaultEnv = nil

	manifest := domainconfig.AgentManifest{
		Name:          agentName,
		AgentOverride: override,
	}
	data, err := infraconfig.MarshalManifest(manifest)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprint(cmd.OutOrStdout(), string(data))
	return nil
}
