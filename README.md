# apiary

Run coding agents in hardware-isolated microVMs. Review every change before it touches your workspace.

[![License: Apache-2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![Go Report Card](https://goreportcard.com/badge/github.com/stacklok/apiary)](https://goreportcard.com/report/github.com/stacklok/apiary)

<!-- TODO: Add a terminal recording / GIF demo here showing the full workflow -->

## Why?

Coding agents are powerful, but they need access to your workspace, your API keys,
and the ability to run arbitrary code. That's a lot of trust to hand over.

Containers help, but they share the host kernel. One escape and you're done.

Enter **apiary**. It boots a lightweight microVM (via [libkrun](https://github.com/containers/libkrun) and KVM),
mounts a copy-on-write snapshot of your workspace, forwards only the secrets you
specify, and lets you review every file change before it lands. Hardware isolation
with the feel of a local terminal.

```bash
apiary claude-code
```

And that's it. You get a full interactive session with Claude Code running inside a
VM. When the agent exits, you review the diff and accept or reject each file.

## Features

- **Hardware-isolated microVMs** -- KVM-backed VMs via libkrun, not just containers
- **Workspace snapshot & review** -- COW snapshot so the agent never touches your real files; interactive per-file review with unified diffs when it's done
- **Multi-agent support** -- Claude Code, Codex, and OpenCode out of the box, plus custom agents via config
- **DNS-aware egress firewall** -- Three profiles (permissive, standard, locked) control what the VM can reach
- **MCP tool proxy** -- Automatically discovers and proxies [ToolHive](https://github.com/stacklok/toolhive) MCP servers into the VM
- **Git integration** -- Forwards tokens and SSH agent for git operations inside the VM
- **Ephemeral security** -- Per-session SSH keys, localhost-only connections, non-overridable security patterns for sensitive files
- **Zero persistent state** -- Each session is fully ephemeral; nothing lingers after cleanup

## Quick Start

### Prerequisites

- Linux with KVM support (`/dev/kvm` must be accessible), or macOS with Hypervisor.framework (Apple Silicon)
- [Go 1.25.7+](https://go.dev/dl/)
- [Task](https://taskfile.dev/) (task runner)
- [GitHub CLI (`gh`)](https://cli.github.com/) (for downloading pre-built runtime artifacts)
- An API key for your agent (e.g. `ANTHROPIC_API_KEY` for Claude Code)

### Build

```bash
task build-dev
```

This downloads pre-built propolis runtime artifacts and embeds them into a
self-contained `apiary` binary (pure Go, no CGO). No system `libkrun-devel`
needed. The binary lands in `bin/`.

### Run

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
apiary claude-code
```

The workflow:

1. Creates a COW snapshot of your current directory
2. Boots a microVM with the Claude Code image
3. Drops you into an interactive terminal session
4. When you exit, shows a per-file diff review
5. Accepted changes are flushed back to your workspace

## Usage

```bash
# Run with a specific agent
apiary claude-code
apiary codex
apiary opencode

# Override resources
apiary claude-code --cpus 4 --memory 4096

# Use a different workspace
apiary claude-code --workspace /path/to/project

# Disable snapshot isolation (mount workspace directly)
apiary claude-code --no-review

# Exclude files from snapshot
apiary claude-code --exclude "*.log" --exclude "tmp/"

# Lock down egress to LLM provider only
apiary claude-code --egress-profile locked

# Allow additional egress hosts (DNS hostnames only, no IP addresses)
apiary claude-code --allow-host "internal-api.example.com:443"

# Disable MCP proxy
apiary claude-code --no-mcp

# Use a specific ToolHive group for MCP servers
apiary claude-code --mcp-group "coding-tools"

# Pass agent-specific arguments (after --)
apiary claude-code -- --help

# List available agents
apiary list
```

## Configuration

Apiary uses a three-level config system: CLI flags > per-workspace > global. CLI flags always win.

### Global config

`~/.config/apiary/config.yaml`:

```yaml
defaults:
  cpus: 4
  memory: 4096
  egress_profile: "standard"

review:
  enabled: true
  exclude_patterns:
    - "*.log"
    - "build/"

mcp:
  enabled: true
  group: "default"
  port: 4483

git:
  forward_token: true
  forward_ssh_agent: true

agents:
  claude-code:
    env_forward:
      - ANTHROPIC_API_KEY
      - CLAUDE_*
      - GITHUB_TOKEN
```

### Per-workspace config

`.apiary.yaml` in your project root:

```yaml
defaults:
  cpus: 8
  memory: 8192

review:
  exclude_patterns:
    - "data/"
```

Note that `review.enabled` is **ignored** in per-workspace config for security.
An untrusted repo can't disable review on your behalf.

Similarly, `egress_profile` in per-workspace config cannot widen the global profile.

### Exclude patterns

`.apiaryignore` in your project root uses gitignore syntax:

```gitignore
# Exclude build artifacts
build/
dist/

# But include the config
!dist/config.json
```

Security-sensitive patterns (`.env*`, `*.pem`, `.ssh/`, `.aws/`, etc.) are **always excluded** and cannot be negated.

## Egress Firewall

Each agent comes with DNS-aware egress policies. Three profiles are available:

| Profile | What it allows |
|---|---|
| `permissive` | All outbound traffic, no restrictions |
| `standard` (default) | LLM provider + common dev infrastructure (GitHub, npm, PyPI, Go proxy, Docker Hub, GHCR) |
| `locked` | LLM provider only (e.g. `api.anthropic.com` for Claude Code) |

```bash
# Lock it down
apiary claude-code --egress-profile locked

# Or open it up
apiary claude-code --egress-profile permissive

# Add specific hosts to standard profile (DNS hostnames only, no IP addresses)
apiary claude-code --allow-host "my-registry.example.com:443"
```

## Supported Agents

| Agent | Command | Image | Default Resources |
|---|---|---|---|
| Claude Code | `apiary claude-code` | `ghcr.io/stacklok/apiary/claude-code` | 2 vCPUs, 2 GiB RAM |
| Codex | `apiary codex` | `ghcr.io/stacklok/apiary/codex` | 2 vCPUs, 2 GiB RAM |
| OpenCode | `apiary opencode` | `ghcr.io/stacklok/apiary/opencode` | 2 vCPUs, 2 GiB RAM |

You can also define custom agents in your config:

```yaml
agents:
  my-agent:
    image: "ghcr.io/my-org/my-agent:latest"
    command: ["my-agent-binary"]
    cpus: 4
    memory: 4096
    env_forward:
      - MY_API_KEY
      - MY_AGENT_*
```

## How It Works

```
apiary claude-code
      │
      ▼
  Create COW snapshot of workspace
      │
      ▼
  Pull OCI image, extract rootfs, inject init binary + SSH keys
      │
      ▼
  Boot microVM (libkrun/KVM) with virtio-fs workspace mount
      │
      ▼
  Guest boots (apiary-init as PID 1):
    → Mount filesystems, configure networking
    → Start embedded SSH server
    → Wait for connection
      │
      ▼
  Interactive SSH session:
    source /etc/sandbox-env && cd /workspace && exec claude
      │
      ▼
  Agent exits → VM stopped
      │
      ▼
  SHA-256 diff → Interactive per-file review → Flush accepted changes
      │
      ▼
  Cleanup snapshot
```

The guest VM runs a custom Go init binary (`apiary-init`) as PID 1. No shell scripts,
no external sshd, no iproute2. Everything the guest needs is compiled into a single
binary that handles boot, networking, workspace mounting, and an embedded SSH server.

The workspace snapshot uses FICLONE on Linux for near-instant copy-on-write cloning.
When the agent is done, a SHA-256 based differ detects changes, and the review UI
shows unified diffs for each file. Accepted changes are flushed back with hash
re-verification to prevent TOCTOU attacks. The VM is explicitly stopped before
review begins, so the agent can't modify files during your review.

## Security Model

Apiary's isolation is built on several layers:

- **KVM hardware virtualization** -- The agent runs in a real VM, not a container with a shared kernel
- **Ephemeral SSH keys** -- ECDSA P-256 keys generated per session, destroyed on exit
- **Localhost-only networking** -- SSH port forwards bind to 127.0.0.1 only
- **Non-overridable security patterns** -- Files like `.env`, `*.pem`, `.ssh/`, `.aws/` are always excluded from snapshots, even if `.apiaryignore` tries to negate them
- **Shell-escaped environment injection** -- All forwarded values are single-quote escaped
- **VM stopped before review** -- Prevents the agent from modifying files while you're reviewing
- **Hash verification on flush** -- Files are re-hashed between diff and flush to catch any modifications
- **Permission stripping** -- setuid, setgid, and sticky bits are stripped when flushing changes
- **Path traversal protection** -- Symlinks are validated in-bounds before copying
- **Per-workspace config restrictions** -- `review.enabled` and egress widening are ignored in `.apiary.yaml`

## Building from Source

```bash
# Build self-contained apiary (downloads + embeds propolis runtime)
task build-dev

# Build apiary only (pure Go, no CGO, needs propolis-runner on PATH)
task build

# Build apiary + propolis-runner from system libkrun (requires libkrun-devel)
task build-dev-system

# Build guest init binary
task build-init

# Run tests
task test

# Lint
task lint

# Format + lint + test
task verify

# Build guest VM images (requires podman)
task image-all
```

Note: Always use `task` for building, testing, and linting. The Taskfile sets
critical flags, ldflags, and environment variables that raw `go` commands miss.

## Contributing

Contributions are welcome! Please open an issue to discuss your idea before submitting a PR.

The project follows strict DDD (Domain-Driven Design) layered architecture.
See [CLAUDE.md](CLAUDE.md) for architecture details and coding conventions.

## License

[Apache-2.0](LICENSE)

Copyright 2025 [Stacklok, Inc.](https://stacklok.com)
