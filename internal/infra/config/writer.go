// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// defaultConfigTemplate is a fully commented YAML template documenting every
// Config field. All values are commented out so the file is safe to ship as-is.
var defaultConfigTemplate = `# Brood Box configuration
# All values below are commented out and show their defaults.
# Uncomment and modify the settings you want to change.

# Default VM resource limits applied to all agents unless overridden.
# defaults:
#   # Number of vCPUs (0 = agent default).
#   cpus: 0
#
#   # RAM for the VM. Accepts human-readable values like "4g" or "512m".
#   # Bare integers are treated as MiB. Zero uses the agent default.
#   memory: 0
#
#   # Size of /tmp tmpfs inside the VM. Accepts human-readable values
#   # like "512m" or "2g". Zero uses the go-microvm default (256 MiB).
#   tmp_size: 0
#
#   # Default egress restriction level: permissive, standard, locked.
#   # "permissive" allows all outbound traffic (no restrictions).
#   # "standard" allows the agent's LLM provider plus common dev infra.
#   # "locked" allows only the agent's LLM provider.
#   egress_profile: ""

# Workspace isolation mode.
# "snapshot" (default) exposes the workspace via a copy-on-write snapshot;
#   the agent never touches your real files until the flush step.
# "direct" mounts the workspace read-write inside the VM with no snapshot,
#   no review, no undo, and git credential sanitization is skipped.
#   Equivalent to --workspace-mode=direct on the CLI. Per-workspace
#   .broodbox.yaml CANNOT set "direct" (only the operator can).
# workspace:
#   mode: "snapshot"

# Workspace snapshot review behavior. Only applies in snapshot mode.
# Use review.enabled or --review to interactively approve or reject
# each changed file.
# review:
#   # Enable interactive per-file review of workspace changes.
#   # NOTE: This setting is ignored in per-workspace .broodbox.yaml
#   # for security — use --review flag or this global config only.
#   enabled: false
#
#   # Additional gitignore-style patterns to exclude from the workspace
#   # snapshot diff. Security patterns (.env*, *.pem, etc.) are always
#   # excluded and cannot be overridden.
#   exclude_patterns: []
#   # exclude_patterns:
#   #   - "*.log"
#   #   - "tmp/"

# Egress networking configuration.
# network:
#   # Additional egress hosts to allow beyond the profile defaults.
#   # Each entry specifies a DNS hostname (no IP addresses).
#   allow_hosts: []
#   # allow_hosts:
#   #   - name: custom-api.example.com
#   #     ports: [443]
#   #     protocol: 6  # TCP

# OCI image pulling behavior.
# image:
#   # Image pull policy: always, background, if-not-present, never.
#   # "always" — always check the registry for a new digest before starting;
#   #   still uses the digest-based cache so unchanged images are not re-extracted.
#   # "background" — use the cached image instantly, check the registry in the
#   #   background; a newer image is cached for the next run (default).
#   # "if-not-present" — use the cache if available, otherwise pull.
#   # "never" — use the cache only; fail if the image is not cached.
#   #   Useful for airgapped/offline environments and CI.
#   pull: "background"

# MCP (Model Context Protocol) proxy configuration.
# The MCP proxy discovers servers from ToolHive and makes them
# available to the agent inside the VM.
# mcp:
#   # Enable or disable the MCP proxy (default: true).
#   enabled: true
#
#   # ToolHive group to discover MCP servers from.
#   group: "default"
#
#   # TCP port for the MCP proxy on the VM gateway.
#   port: 4483
#
#   # Authorization profile: full-access, observe, safe-tools, custom.
#   # "full-access" — no restrictions (default).
#   # "observe" — list + read tools/prompts/resources only.
#   # "safe-tools" — observe + non-destructive closed-world tools.
#   # "custom" — Cedar policies from mcp.config or --mcp-config.
#   # authz:
#   #   profile: "full-access"
#
#   # Inline MCP config for Cedar authorization policies and tool
#   # aggregation settings. Can also be loaded from a file via --mcp-config.
#   # NOTE: mcp.config is NOT merged from per-workspace .broodbox.yaml
#   # for security — untrusted repos must not inject Cedar policies.
#   # config:
#   #   authz:
#   #     policies:
#   #       - 'permit(principal, action == Action::"list_tools", resource);'
#   #   aggregation:
#   #     conflict_resolution: "prefix"  # prefix, priority, or manual
#   #     prefix_format: ""
#   #     priority_order: []
#   #     exclude_all_tools: false
#   #     tools: []

# Git identity and authentication forwarding into the sandbox VM.
# git:
#   # Forward GITHUB_TOKEN/GH_TOKEN into the VM (default: true).
#   forward_token: true
#
#   # Enable SSH agent forwarding into the VM (default: true).
#   forward_ssh_agent: true

# Credential persistence between sessions.
# auth:
#   # Save agent credentials between sessions (default: true).
#   save_credentials: true
#
#   # Seed agent credentials from host (e.g. macOS Keychain) into the
#   # VM before the agent starts (default: false).
#   seed_host_credentials: false

# Agent settings injection into the VM.
# Copies host agent settings (rules, skills, themes, etc.) into the guest.
# Equivalent of --no-settings when set to enabled: false.
# settings_import:
#   # Enable or disable all settings injection (default: true).
#   # Set to false to disable (same as --no-settings flag).
#   enabled: true
#
#   # Control which categories of settings are imported.
#   # Each category defaults to true when omitted or null.
#   # categories:
#   #   settings: true
#   #   instructions: true
#   #   rules: true
#   #   agents: true
#   #   skills: true
#   #   commands: true
#   #   tools: true
#   #   plugins: true
#   #   themes: true

# Host runtime dependency configuration.
# runtime:
#   # Download libkrunfw firmware at runtime (default: true).
#   # Set to false to use system-installed libkrunfw only.
#   firmware_download: true

# Per-agent configuration overrides. Keys are agent names
# (e.g. claude-code, codex, opencode, or custom agent names).
# agents:
#   # claude-code:
#   #   # Override the OCI image reference.
#   #   image: "ghcr.io/stacklok/brood-box/claude-code:latest"
#   #
#   #   # Override the entrypoint command.
#   #   command: ["claude-code"]
#   #
#   #   # Override environment variable forwarding patterns.
#   #   env_forward:
#   #     - ANTHROPIC_API_KEY
#   #     - "CLAUDE_*"
#   #
#   #   # Override vCPU count for this agent.
#   #   cpus: 0
#   #
#   #   # Override RAM for this agent (e.g. "4g", "512m").
#   #   memory: 0
#   #
#   #   # Override /tmp tmpfs size for this agent (e.g. "512m", "2g").
#   #   tmp_size: 0
#   #
#   #   # Override egress profile for this agent.
#   #   # Can only tighten (not widen) the agent's built-in profile.
#   #   egress_profile: ""
#   #
#   #   # Additional egress hosts for this agent.
#   #   allow_hosts: []
#   #
#   #   # Override MCP proxy settings for this agent.
#   #   # Only enabled (gate) and authz (tighten-only) are supported.
#   #   mcp:
#   #     enabled: true
#   #     authz:
#   #       profile: "full-access"
#   #
#   #   # Override settings injection for this agent.
#   #   # Can only tighten (disable), not enable if globally disabled.
#   #   settings_import:
#   #     enabled: true
#   #     categories:
#   #       rules: true
#   #       skills: true
`

// WriteDefault writes the default config template to the given path.
// Parent directories are created with mode 0o700. The file is written
// with mode 0o600. Returns an error if the file already exists and
// force is false.
func WriteDefault(path string, force bool) error {
	if !force {
		_, err := os.Stat(path)
		if err == nil {
			return fmt.Errorf("config file already exists: %s (use --force to overwrite)", path)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("checking config file: %w", err)
		}
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(defaultConfigTemplate), 0o600); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	// WriteFile only sets permissions on creation. When overwriting an
	// existing file (--force) the old permissions remain. Explicitly
	// chmod to ensure 0600 regardless of prior state.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("setting config file permissions: %w", err)
	}

	return nil
}
