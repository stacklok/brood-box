// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/spf13/cobra"

	infraconfig "github.com/stacklok/brood-box/internal/infra/config"
	"github.com/stacklok/brood-box/internal/infra/configfile"
	infraocimage "github.com/stacklok/brood-box/internal/infra/ocimage"
	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
)

// agentsImportCmd is `bbox agents import <file|image>`: read a standalone agent
// manifest — from a local YAML file or a self-describing OCI image — validate
// it, and append the agent to the global config (reuse the exact path `agents
// add` takes so behavior, receipts, and safety match).
func agentsImportCmd() *cobra.Command {
	var (
		cfgPath      string
		nameOverride string
		force        bool
		jsonOut      bool
	)
	cmd := &cobra.Command{
		Use:   "import <manifest|image>",
		Short: "Import a custom agent from a manifest file or OCI image into the global config",
		Long: `Reads a standalone agent manifest, validates it with the same checks the
loader uses, and appends it to the global config
(~/.config/broodbox/config.yaml, or the path given by --config). Existing
comments/formatting in the config are preserved; the added agent block is
written as normalized YAML.

A source is treated as an OCI image when it is not an existing local file AND
it looks like an image reference an operator would type. Specifically, the
source must contain a ` + "`/`" + ` (registry+repo, e.g.
ghcr.io/acme/aider-bbox:latest) or an ` + "`@sha256:`" + ` digest. A bare
library image like ` + "`ubuntu:24.04`" + ` (no slash) is treated as a local
file path — use the fully-qualified ` + "`docker.io/library/ubuntu:24.04`" + `
to import it. For an image, the manifest is extracted from an embedded
agent.yaml declared by the ` + "`org.stacklok.broodbox.agent`" + ` config label,
or from the well-known path ` + "`/usr/share/broodbox/agent.yaml`" + ` when the
label is absent. The imported image ref is pinned to its resolved digest so the
registered agent is reproducible. If the embedded manifest declares a different
repository, a warning is printed and the imported (digest-pinned) ref overrides
it.

Custom agents are GLOBAL-ONLY: this command never writes to a workspace
.broodbox.yaml. Refuses to overwrite a built-in or an existing custom agent
unless --force.

--name overrides the manifest's name (useful when the embedded name collides
or is undescriptive).

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
  bbox agents import ghcr.io/acme/aider-bbox:latest
  bbox agents import ghcr.io/acme/aider-bbox:latest --name aider2 --force`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgentsImport(cmd, args[0], cfgPath, nameOverride, force, jsonOut, defaultImportFetcher)
		},
	}
	cmd.Flags().StringVar(&cfgPath, "config", "", "Config file path (default: ~/.config/broodbox/config.yaml)")
	cmd.Flags().StringVar(&nameOverride, "name", "", "Override the manifest's agent name")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Overwrite a built-in or existing custom agent")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Emit a JSON receipt of the mutation instead of human-readable output")
	return cmd
}

// defaultImportFetcher is the Fetcher used by the real CLI. Tests override it
// (via runAgentsImport's fetcher parameter) to inject an in-memory fake so the
// e2e path is exercised without network.
var defaultImportFetcher infraocimage.Fetcher = infraocimage.NewRemoteFetcher()

// runAgentsImport imports a custom agent from a local manifest file or an OCI
// image. source is classified by isImageRef; when it is an image, fetcher
// pulls the embedded manifest and the digest-pinned image ref. The validate →
// collision-gate → UpsertAgent → receipt tail is shared with the file path.
func runAgentsImport(
	cmd *cobra.Command,
	source, cfgPath, nameOverride string,
	force, jsonOut bool,
	fetcher infraocimage.Fetcher,
) error {
	manifest, sourceLabel, err := loadImportManifest(cmd.Context(), source, fetcher, cmd.ErrOrStderr())
	if err != nil {
		return err
	}

	if nameOverride != "" {
		manifest.Name = nameOverride
	}
	if manifest.Name == "" {
		return fmt.Errorf("manifest %s: top-level %q field is required", sourceLabel, "name")
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

// loadImportManifest loads an AgentManifest from a local file or an OCI image,
// depending on whether source is an existing file or an image reference. For an
// image, the manifest's image field is overridden with the digest-pinned ref
// (with a warning on mismatch written to warn), and sourceLabel describes the
// source for error messages.
func loadImportManifest(
	ctx context.Context,
	source string,
	fetcher infraocimage.Fetcher,
	warn io.Writer,
) (domainconfig.AgentManifest, string, error) {
	if isImageRef(source) {
		manifestBytes, pinnedRef, err := fetcher.FetchAgentManifest(ctx, source)
		if err != nil {
			return domainconfig.AgentManifest{}, source, fmt.Errorf("importing agent from image %q: %w", source, err)
		}
		// Reuse the same size cap + strict decode as the file path so a
		// malformed or oversized embedded manifest fails loudly.
		var m domainconfig.AgentManifest
		if err := configfile.DecodeStrict(manifestBytes, &m); err != nil {
			return domainconfig.AgentManifest{}, source, fmt.Errorf("parsing manifest from image %s: %w", source, err)
		}

		// The imported ref is authoritative: pin to the resolved digest so the
		// registered agent is reproducible. Warn (don't fail) only on a real
		// REPOSITORY mismatch — a same-repo tag→digest pin is the normal case and
		// should be silent.
		if m.Image != "" {
			declaredRepo, parseErr := name.ParseReference(m.Image)
			if parseErr != nil {
				_, _ = fmt.Fprintf(warn, "Warning: image manifest declares a malformed image %q; overriding with imported ref %q\n", m.Image, pinnedRef)
			} else if importedRepo, err := name.ParseReference(source); err == nil &&
				declaredRepo.Context().Name() != importedRepo.Context().Name() {
				_, _ = fmt.Fprintf(warn, "Warning: image manifest declares image %q; overriding with imported ref %q\n", m.Image, pinnedRef)
			}
		}
		m.Image = pinnedRef
		return m, source, nil
	}

	manifest, err := infraconfig.LoadManifest(source)
	if err != nil {
		return domainconfig.AgentManifest{}, source, err
	}
	return manifest, source, nil
}

// isImageRef reports whether source should be treated as an OCI image reference
// rather than a local manifest file. A source is an image when it is NOT an
// existing local file AND it contains a "/" (registry+repo, e.g.
// ghcr.io/acme/x:latest) or an "@sha256:" digest, AND it parses as a valid image
// reference. The slash/digest requirement excludes bare local-looking names
// like "aider.yaml" or "aider" (which go-containerregistry would otherwise
// accept as docker.io library refs) so a typo'd manifest path does not trigger
// a network pull. A nonexistent path that does not qualify falls through to the
// file path, which then produces a precise "no such file" error from
// LoadManifest.
func isImageRef(source string) bool {
	if _, err := os.Stat(source); err == nil {
		// Exists on disk — treat as a file, even if it also looks like a ref.
		return false
	} else if !errors.Is(err, fs.ErrNotExist) {
		// A stat error that isn't "not found" (e.g. permission) is surfaced by
		// the file path, which wraps it readably. Don't claim it's an image.
		return false
	}
	if strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") || strings.HasPrefix(source, "/") {
		// A relative or absolute path — even one that contains "/" — is a
		// file path, not an image reference.
		return false
	}
	if !strings.Contains(source, "/") && !strings.Contains(source, "@sha256:") {
		return false
	}
	if _, err := name.ParseReference(source); err != nil {
		return false
	}
	return true
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
