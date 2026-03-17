# User Guide

Brood Box runs coding agents (Claude Code, Codex, OpenCode) inside
hardware-isolated microVMs powered by [libkrun](https://github.com/containers/libkrun).
Your workspace is mounted into the VM, API keys are forwarded automatically,
and you get a live interactive terminal session.

## Quick Start

```bash
# Run Claude Code in the current directory
bbox claude-code

# Run Codex with more resources
bbox codex --cpus 4 --memory 4096

# Run OpenCode on a specific project
bbox opencode --workspace /path/to/project
```

## Prerequisites

- **Linux** with KVM support (`/dev/kvm` must be accessible), or **macOS** with Hypervisor.framework (Apple Silicon)

Release binaries are self-contained and do not require `libkrun-devel` or any system libraries.

Verify KVM access (Linux):

```bash
ls -la /dev/kvm
# If missing: sudo modprobe kvm && sudo modprobe kvm_intel  # or kvm_amd
# If permission denied: sudo usermod -aG kvm $USER  (then re-login)
```

## Built-in Agents

| Agent | Image | Command | Forwarded Env Vars |
|-------|-------|---------|--------------------|
| `claude-code` | `ghcr.io/stacklok/brood-box/claude-code:latest` | `claude` | `ANTHROPIC_API_KEY`, `CLAUDE_*` |
| `codex` | `ghcr.io/stacklok/brood-box/codex:latest` | `codex` | `OPENAI_API_KEY`, `CODEX_*` |
| `opencode` | `ghcr.io/stacklok/brood-box/opencode:latest` | `opencode` | `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `OPENROUTER_API_KEY`, `OPENCODE_*` |

All agents default to the `permissive` egress profile.

List agents:

```bash
bbox list
```

## CLI Reference

```
bbox <agent-name> [flags] [-- <agent-args...>]
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--cpus` | Agent default (2) | Number of vCPUs for the VM |
| `--memory` | Agent default (2048) | RAM in MiB |
| `--workspace` | Current directory | Host directory mounted as `/workspace` |
| `--ssh-port` | Auto-pick | Host port forwarded to guest SSH (port 22) |
| `--config` | `~/.config/broodbox/config.yaml` | Config file path |
| `--image` | Agent default | Override the OCI image reference |
| `--no-review` | `false` | Disable snapshot isolation, mount workspace directly |
| `--exclude` | (none) | Additional gitignore-style exclude patterns (repeatable) |
| `--egress-profile` | Agent default (`permissive`) | Egress restriction level: `permissive`, `standard`, `locked` |
| `--allow-host` | (none) | Additional allowed egress DNS hostname[:port] — no IP addresses (repeatable) |
| `--no-mcp` | `false` | Disable MCP tool proxy |
| `--mcp-group` | `default` | ToolHive group to discover MCP servers from |
| `--mcp-port` | `4483` | Port for MCP proxy on VM gateway |
| `--mcp-config` | (none) | Path to MCP config YAML (Cedar policies and aggregation settings) |
| `--mcp-authz-profile` | `full-access` | MCP authorization profile: `full-access`, `observe`, `safe-tools`, `custom` |
| `--no-git-token` | `false` | Disable forwarding GITHUB_TOKEN/GH_TOKEN into the VM |
| `--no-git-ssh-agent` | `false` | Disable SSH agent forwarding into the VM |
| `--no-firmware-download` | `false` | Disable firmware download (use system libkrunfw only) |
| `--log-file` | `~/.config/broodbox/vms/<vm>/broodbox.log` | Override log file path |
| `--debug` | `false` | Enable debug-level logging to file |

Pass agent-specific arguments after `--` so they are not parsed by Brood Box:

```bash
bbox claude-code -- --help
```

### Subcommands

| Command | Description |
|---------|-------------|
| `list` | List all available agents |

## What Happens When You Run It

1. **Resolve agent** -- Looks up the agent name in the built-in registry
   (and any custom agents from your config file).
2. **Load config** -- Reads `~/.config/broodbox/config.yaml` and
   per-workspace `.broodbox.yaml`, merges overrides with built-in
   agent defaults and CLI flags.
3. **Collect environment** -- Matches your host environment variables against
   the agent's forwarding patterns (e.g., `ANTHROPIC_API_KEY`, `CLAUDE_*`)
   and collects them for injection into the VM.
4. **Create snapshot** -- If review is enabled (the default), creates a
   copy-on-write snapshot of your workspace. The agent works on the
   snapshot, not your real files.
5. **Boot VM** -- Pulls the OCI image, extracts it into a rootfs, injects
   SSH keys + init binary + env file, and starts a libkrun microVM.
6. **Wait for SSH** -- Polls the VM until the embedded SSH server is ready.
7. **Interactive session** -- Opens a PTY-forwarded SSH session into the VM
   with your terminal size, sources the env file, `cd /workspace`, and
   `exec`s the agent command.
8. **Stop VM** -- When the agent exits (or you press Ctrl+C), the VM is
   gracefully shut down.
9. **Diff** -- Compares the snapshot against the original workspace using
   SHA-256 hashes to detect added, modified, and deleted files.
10. **Review** -- Presents each changed file interactively so you can
    accept or reject individual changes.
11. **Flush** -- Copies accepted changes back to your real workspace,
    re-verifying hashes before writing.
12. **Cleanup** -- Removes the snapshot directory and ephemeral SSH keys.

## Configuration

Create `~/.config/broodbox/config.yaml` to customize defaults,
override built-in agents, or define custom agents.

```yaml
# Global resource defaults (applied when agent doesn't specify its own)
defaults:
  cpus: 4
  memory: 4096
  egress_profile: "permissive"

# Workspace snapshot review settings
review:
  enabled: true
  exclude_patterns:
    - "*.log"
    - "build/"

# Egress networking
network:
  allow_hosts:
    - name: "internal-api.example.com"
      ports: [443]

# MCP tool proxy (discovers servers from ToolHive)
mcp:
  enabled: true
  group: "default"
  port: 4483
  # Optional inline MCP config (Cedar policies and aggregation)
  # config:
  #   authz:
  #     policies:
  #       - 'permit(principal, action, resource);'
  authz:
    profile: "full-access"  # observe, safe-tools, custom

# Git identity and auth forwarding
git:
  forward_token: true       # Forward GITHUB_TOKEN/GH_TOKEN (default: true)
  forward_ssh_agent: true   # Forward SSH agent for git+ssh (default: true)

# Host runtime dependencies
runtime:
  firmware_download: true   # Download libkrunfw at runtime (default: true)

# Per-agent overrides and custom agents
agents:
  # Override built-in agent settings
  claude-code:
    env_forward:
      - ANTHROPIC_API_KEY
      - "CLAUDE_*"
      - GITHUB_TOKEN       # Forward additional vars
      - SSH_AUTH_SOCK

  # Define a custom agent
  my-custom-agent:
    image: ghcr.io/me/my-agent:latest
    command: ["my-agent", "--interactive"]
    env_forward:
      - MY_API_KEY
    cpus: 2
    memory: 1024
```

### Config Resolution Order

For each setting, the first non-zero value wins:

1. CLI flags (`--cpus`, `--memory`, `--image`)
2. Config file agent overrides (`agents.<name>.cpus`)
3. Built-in agent defaults
4. Config file global defaults (`defaults.cpus`)

### Environment Variable Patterns

Patterns in `env_forward` support:

- **Exact match**: `ANTHROPIC_API_KEY` -- forwards only that variable
- **Glob suffix**: `CLAUDE_*` -- forwards any variable starting with `CLAUDE_`

Variables are injected into `/etc/sandbox-env` inside the VM and sourced
before the agent starts. Values are shell-escaped for safety.

## Workspace Snapshot Isolation

By default, Brood Box creates a copy-on-write (COW) snapshot of your
workspace before the agent starts. The agent works on the snapshot, and
after it finishes you review changes per-file before they touch your real
workspace.

### How It Works

```
Your workspace ──COW copy──▶ Snapshot directory
                                    │
                              Agent works here
                                    │
                              VM stopped
                                    │
                              Diff (SHA-256)
                                    │
                              Review per-file
                                    │
                        Accepted changes ──▶ Your workspace
```

### Disabling Review

Pass `--no-review` to mount the workspace directly into the VM with no
snapshot isolation:

```bash
bbox claude-code --no-review
```

### Exclude Patterns

Certain files are automatically excluded from the snapshot:

**Security patterns** (non-overridable — always excluded):
- `.env*`, `*.pem`, `*.key`, `.ssh/`, `.aws/`, `.gcp/`, `credentials.json`
- `.broodbox.yaml`, `.kube/config`, `.gnupg/`, and more

**Performance patterns** (overridable -- can be negated in `.broodboxignore`):
- `node_modules/`, `vendor/`, `.git/objects/`, `__pycache__/`, `target/`,
  `build/`, `dist/`, `.venv/`, `.tox/`

Add extra patterns via the CLI:

```bash
bbox claude-code --exclude "*.log" --exclude "tmp/"
```

### `.broodboxignore`

Create a `.broodboxignore` file in your workspace root (gitignore syntax)
to exclude additional paths:

```gitignore
# Exclude large data files
data/
*.csv

# Re-include a performance-excluded directory
!vendor/
```

Security patterns cannot be negated — attempts are logged as warnings.

### Per-Workspace Config

Create `.broodbox.yaml` in your workspace root to set per-project
defaults:

```yaml
defaults:
  cpus: 4
  memory: 4096
review:
  exclude_patterns:
    - "*.log"
```

Note: `review.enabled` is **ignored** in per-workspace config for security
(use `--no-review` explicitly).

## Egress Firewall

Each agent comes with DNS-aware egress policies. Three profiles are available:

| Profile | What it allows |
|---|---|
| `permissive` (default) | All outbound traffic, no restrictions |
| `standard` | LLM provider + common dev infrastructure (GitHub, npm, PyPI, Go proxy, Docker Hub, GHCR, Sentry) |
| `locked` | LLM provider only (e.g. `api.anthropic.com` for Claude Code) |

```bash
# Lock egress to the LLM provider only
bbox claude-code --egress-profile locked

# Add specific hosts beyond the profile (DNS hostnames only, no IP addresses)
bbox claude-code --allow-host "my-registry.example.com:443"
```

The egress firewall is DNS-based: it intercepts DNS queries to enforce allow-lists.
Only hostnames are accepted — IP addresses are rejected because they bypass DNS
resolution and would silently never match. Wildcards must be the entire leftmost
label only (e.g. `*.example.com`).

Additional hosts can also be configured globally or per-workspace:

```yaml
# In config.yaml
network:
  allow_hosts:
    - name: "internal-api.example.com"
      ports: [443]
```

## MCP Tool Proxy

When [ToolHive](https://github.com/stacklok/toolhive) is running, Brood Box
automatically discovers MCP servers and proxies them into the VM so the
agent can use external tools.

Brood Box does this by running an embedded instance of ToolHive's
[Virtual MCP Server](https://github.com/stacklok/toolhive) (vMCP) — the
same component that aggregates and authorizes MCP traffic in ToolHive's
Kubernetes operator. The vMCP instance runs in-process on the host, discovers
backends from ToolHive groups, and exposes a single MCP endpoint inside the
VM. This gives the guest agent access to all the tools in the group through
one connection, with authorization enforced on the host side before requests
reach any backend.

The `--mcp-config` flag accepts an MCP config YAML file with a simple,
brood-box-native format. It supports two sections: `authz` for Cedar
authorization policies, and `aggregation` for tool conflict resolution
when multiple backends expose tools with the same name.

```bash
# Disable MCP proxy
bbox claude-code --no-mcp

# Use a specific ToolHive group
bbox claude-code --mcp-group "coding-tools"

# Override the proxy port
bbox claude-code --mcp-port 5000
```

MCP config is injected into the guest in the agent-specific format
(Claude Code, Codex, and OpenCode each use different config formats).

### MCP Authorization Profiles

By default, the agent has full access to all MCP operations. You can
restrict what the agent can do with authorization profiles:

| Profile | What the agent can do |
|---|---|
| `full-access` (default) | All MCP operations — no restrictions |
| `observe` | List and read tools, prompts, and resources — cannot call tools |
| `safe-tools` | Observe + call tools annotated as read-only or non-destructive and closed-world |
| `custom` | Operator-defined Cedar policies from MCP config YAML |

```bash
# Agent can only list and read MCP capabilities
bbox claude-code --mcp-authz-profile observe

# Agent can call safe tools (read-only or non-destructive + closed-world)
bbox claude-code --mcp-authz-profile safe-tools

# Use custom Cedar policies from an MCP config file
bbox claude-code --mcp-authz-profile custom --mcp-config /path/to/mcp-config.yaml
```

Or set it in the global config file:

```yaml
mcp:
  authz:
    profile: observe
```

The `safe-tools` profile uses MCP tool annotations to decide what to
allow. Tools with `readOnlyHint: true` are permitted. Tools with both
`destructiveHint: false` and `openWorldHint: false` are also permitted.
Tools without annotations are denied by default (Cedar default-deny).

The `custom` profile reads [Cedar](https://www.cedarpolicy.com/) policies
from the MCP config YAML's `authz.policies` section. Use it when the
built-in profiles don't match your needs — for example, allowing a specific
set of tools by name, or combining annotation-based rules with explicit
tool allow-lists. When an MCP config with policies is provided, the
`custom` profile is inferred automatically (no need to pass
`--mcp-authz-profile custom` explicitly).

Cedar is default-deny: if no `permit` policy matches a request, the request
is denied. Each policy is evaluated independently; a request is allowed if
**any** permit matches and **no** forbid matches.

#### Cedar vocabulary

Policies reference three entity types that map to MCP protocol concepts:

| Entity | Cedar syntax | Description |
|---|---|---|
| **Actions** | `Action::"list_tools"`, `Action::"call_tool"`, `Action::"list_prompts"`, `Action::"get_prompt"`, `Action::"list_resources"`, `Action::"read_resource"` | MCP operations the agent can perform |
| **Resources** | `Tool::"tool_name"`, `Prompt::"prompt_name"`, `Resource::"resource_uri"` | Specific tools, prompts, or resources being accessed |
| **Attributes** | `resource.readOnlyHint`, `resource.destructiveHint`, `resource.openWorldHint` | MCP tool annotations (booleans, may be absent) |

Protocol-level methods (`initialize`, `ping`, notifications) are always
allowed regardless of policies.

> **Important**: Tool annotations are optional — many MCP servers only set
> some of them. Always guard attribute access with `resource has <attr> &&`
> to avoid Cedar evaluation errors on tools that omit annotations.

#### Examples

**Allow listing + only specific tools by name:**

```yaml
# mcp-config.yaml
authz:
  policies:
    # Let the agent discover what's available
    - 'permit(principal, action == Action::"list_tools", resource);'
    - 'permit(principal, action == Action::"list_prompts", resource);'
    - 'permit(principal, action == Action::"list_resources", resource);'
    # Allow only these specific tools
    - 'permit(principal, action == Action::"call_tool", resource == Tool::"search_code");'
    - 'permit(principal, action == Action::"call_tool", resource == Tool::"get_file_contents");'
```

**Start from safe-tools and add a specific destructive tool:**

```yaml
authz:
  policies:
    # Observe (list + read)
    - 'permit(principal, action == Action::"list_tools", resource);'
    - 'permit(principal, action == Action::"list_prompts", resource);'
    - 'permit(principal, action == Action::"list_resources", resource);'
    - 'permit(principal, action == Action::"get_prompt", resource);'
    - 'permit(principal, action == Action::"read_resource", resource);'
    # Safe tools (same as built-in safe-tools profile)
    - |
      permit(principal, action == Action::"call_tool", resource)
        when { resource has readOnlyHint && resource.readOnlyHint == true };
    - |
      permit(principal, action == Action::"call_tool", resource)
        when { resource has destructiveHint && resource.destructiveHint == false
            && resource has openWorldHint && resource.openWorldHint == false };
    # Plus: allow create_pull_request even though it's destructive
    - 'permit(principal, action == Action::"call_tool", resource == Tool::"create_pull_request");'
```

**Block a specific tool while allowing everything else:**

```yaml
authz:
  policies:
    # Allow all operations
    - 'permit(principal, action, resource);'
    # But explicitly deny this one tool (forbid overrides permit)
    - 'forbid(principal, action == Action::"call_tool", resource == Tool::"delete_repository");'
```

#### Usage

```bash
bbox claude-code --mcp-config ./mcp-config.yaml
```

**Security**: Per-workspace `.broodbox.yaml` can only tighten the authz
profile (e.g. from `safe-tools` to `observe`), never widen it. The
`custom` profile cannot be set from workspace config — this prevents a
repository from supplying its own Cedar policies that could escalate
permissions.

## Git Integration

By default, Brood Box forwards your git identity (user.name and user.email),
GITHUB_TOKEN/GH_TOKEN, and SSH agent into the VM so git operations work
seamlessly.

```bash
# Disable token forwarding
bbox claude-code --no-git-token

# Disable SSH agent forwarding
bbox claude-code --no-git-ssh-agent
```

When snapshot isolation is enabled, the `.git/config` is sanitized to
remove sensitive values (credentials, tokens) before copying.

## Signals and Cleanup

- **Ctrl+C (SIGINT)** and **SIGTERM** trigger graceful shutdown:
  the SSH session is terminated, the VM is stopped, and temp files are cleaned up.
- **Terminal resize** (SIGWINCH) is forwarded to the VM session automatically.
- Ephemeral SSH keys are generated per session and deleted on exit.

## Building Guest Images

Brood Box runs agents inside OCI images that boot as microVMs. Pre-built
images are available from GHCR, but you can also build them locally.

### Prerequisites

- [Podman](https://podman.io/) (or Docker)

### Build All Images

```bash
task image-all
```

This builds the base image first, then all three agent images in parallel:

| Image | Contents |
|-------|----------|
| `ghcr.io/stacklok/brood-box/base:latest` | Wolfi + sshd, bash, git, coreutils |
| `ghcr.io/stacklok/brood-box/claude-code:latest` | Base + Claude Code binary |
| `ghcr.io/stacklok/brood-box/codex:latest` | Base + Codex binary |
| `ghcr.io/stacklok/brood-box/opencode:latest` | Base + OpenCode binary |

### Build Individual Images

```bash
task image-base          # Base image only
task image-claude-code   # Claude Code (builds base if needed)
task image-codex         # Codex (builds base if needed)
task image-opencode      # OpenCode (builds base if needed)
```

### Push to GHCR

```bash
# Login first
podman login ghcr.io

# Build and push all images
task image-push
```

## Troubleshooting

### "agent not found: <name>"

The agent name doesn't match any built-in or custom agent. Run
`bbox list` to see available agents. Custom agents need an
`image` field in the config file.

### VM fails to start

Check that:
- `/dev/kvm` exists and is accessible
- `go-microvm-runner` is built and in your PATH (or use `task build-dev`)
- The OCI image exists and is pullable

### SSH connection refused

The guest may still be booting. Brood Box waits for SSH automatically,
but if the image's init system is slow, the timeout may be exceeded.
Check the VM console log for errors.

### Environment variables not available in the VM

Verify the variable is set in your host shell and matches a pattern
in the agent's `env_forward` list. Use `env | grep ANTHROPIC` to check.
