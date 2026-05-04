# Brood Box

> **Warning**
> This project is **EXPERIMENTAL**. APIs, CLI flags, config format, and behavior may change without notice between releases. Use at your own risk and please report issues.

Run coding agents in hardware-isolated microVMs. Review every change before it touches your workspace.

[![License: Apache-2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![CI](https://github.com/stacklok/brood-box/actions/workflows/ci.yaml/badge.svg)](https://github.com/stacklok/brood-box/actions/workflows/ci.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/stacklok/brood-box)](https://goreportcard.com/report/github.com/stacklok/brood-box)
[![Repostatus: Experimental](https://www.repostatus.org/badges/latest/experimental.svg)](https://www.repostatus.org/#experimental)

<!-- TODO: Add a terminal recording / GIF demo here showing the full workflow -->

## Table of Contents

- [Why?](#why)
- [Features](#features)
- [Quick Start](#quick-start)
- [Usage](#usage)
- [Configuration](#configuration)
- [Egress Firewall](#egress-firewall)
- [Supported Agents](#supported-agents)
- [How It Works](#how-it-works)
- [Security Model](#security-model)
- [Documentation](#documentation)
- [Building from Source](#building-from-source)
- [Contributing](#contributing)
- [License](#license)

## Why?

Coding agents are powerful, but they need access to your workspace, your API keys,
and the ability to run arbitrary code. That's a lot of trust to hand over.

Containers help, but they share the host kernel. One escape and you're done.

Enter **Brood Box**. It boots a lightweight microVM (via [libkrun](https://github.com/containers/libkrun) and KVM),
mounts a copy-on-write snapshot of your workspace, forwards only the secrets you
specify, and lets you review every file change before it lands. Hardware isolation
with the feel of a local terminal.

```bash
bbox claude-code
```

And that's it. You get a full interactive session with Claude Code running inside a
VM. When the agent exits, you review the diff and accept or reject each file.

## Features

- **Hardware-isolated microVMs** -- KVM (Linux) and Hypervisor.framework (macOS) backed VMs via libkrun, not just containers
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
- [Go 1.26+](https://go.dev/dl/)
- [Task](https://taskfile.dev/) (task runner)
- [GitHub CLI (`gh`)](https://cli.github.com/) (for downloading pre-built runtime artifacts)
- An API key for your agent (e.g. `ANTHROPIC_API_KEY` for Claude Code)

### Install from Release

Download a pre-built binary from [GitHub Releases](https://github.com/stacklok/brood-box/releases):

```bash
# Example for Linux amd64
tar xzf bbox-linux-amd64.tar.gz
sudo mv bbox /usr/local/bin/
```

Release binaries are self-contained and do not require `libkrun-devel` or any system libraries.

### Build from Source

```bash
task build
```

This downloads pre-built go-microvm runtime artifacts and embeds them into a
self-contained `bbox` binary (pure Go, no CGO). No system `libkrun-devel`
needed. The binary lands in `bin/`.

The firmware (`libkrunfw`) is not embedded. It is downloaded at runtime and
cached under `~/.cache/broodbox/firmware/`, with a system fallback if the
download is unavailable.

### Run

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
bbox claude-code
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
bbox claude-code
bbox codex
bbox opencode
bbox gemini

# Override resources
bbox claude-code --cpus 4 --memory 4096

# Use a different workspace
bbox claude-code --workspace /path/to/project

# Enable interactive per-file review (snapshot mode only)
bbox claude-code --review

# Exclude files from snapshot
bbox claude-code --exclude "*.log" --exclude "tmp/"

# Skip snapshot isolation entirely: the agent writes directly to your workspace
# (no review, no undo). --yes is required on the first run.
bbox claude-code --workspace-mode=direct --yes

# Lock down egress to LLM provider only
bbox claude-code --egress-profile locked

# Allow additional egress hosts (DNS hostnames only, no IP addresses)
bbox claude-code --allow-host "internal-api.example.com:443"

# Disable MCP proxy
bbox claude-code --no-mcp

# Disable firmware download (use system libkrunfw only)
bbox claude-code --no-firmware-download

# Use a specific ToolHive group for MCP servers
bbox claude-code --mcp-group "coding-tools"

# Pass agent-specific arguments (after --)
bbox claude-code -- --help

# List available agents
bbox list
```

### Workspace modes

By default, bbox runs the agent against a copy-on-write snapshot of your workspace
and flushes changes back when the agent exits. No write lands on your real files
without going through the diff engine. Add `--review` to approve each file
interactively.

For quick, trusted edits where you're driving the agent turn-by-turn and snapshot
overhead isn't worth it, pass `--workspace-mode=direct`. The VM mounts your
workspace read-write and writes land immediately. In direct mode, `--review` and
`--exclude` are rejected (they only apply to snapshots), and git credential
sanitization is skipped. Per-workspace `.broodbox.yaml` cannot enable direct mode;
only the operator can, globally or on the CLI, and `--yes` is required on first
use. Use direct mode when you'd trust the agent with an unsandboxed shell anyway.
Otherwise stay on snapshot mode (the default).

## Configuration

Brood Box uses a three-level config system: CLI flags > per-workspace > global. CLI flags always win.

### Global config

`~/.config/broodbox/config.yaml`:

```yaml
defaults:
  cpus: 4
  memory: 4096
  egress_profile: "permissive"

workspace:
  mode: "snapshot"   # snapshot (default) or direct

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

runtime:
  firmware_download: true

agents:
  claude-code:
    env_forward:
      - ANTHROPIC_API_KEY
      - CLAUDE_*
      - GITHUB_TOKEN
```

### Per-workspace config

`.broodbox.yaml` in your project root:

```yaml
defaults:
  cpus: 8
  memory: 8192

review:
  exclude_patterns:
    - "data/"
```

Note that `review.enabled` is **ignored** in per-workspace config for security.
An untrusted repo cannot disable review on your behalf.

`workspace.mode: direct` from per-workspace config is also ignored. An untrusted
repo cannot turn off snapshot isolation. Setting `workspace.mode: snapshot`
in `.broodbox.yaml` is allowed (tighten-only: a repo can force snapshot even if
the global config enables direct).

Similarly, `egress_profile` in per-workspace config cannot widen the global profile.

### Exclude patterns

`.broodboxignore` in your project root uses gitignore syntax:

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
| `permissive` (default) | All outbound traffic, no restrictions |
| `standard` | LLM provider + common dev infrastructure (GitHub, npm, PyPI, Go proxy, Docker Hub, GHCR) |
| `locked` | LLM provider only (e.g. `api.anthropic.com` for Claude Code) |

```bash
# Lock it down
bbox claude-code --egress-profile locked

# Or open it up
bbox claude-code --egress-profile permissive

# Add specific hosts to standard profile (DNS hostnames only, no IP addresses)
bbox claude-code --allow-host "my-registry.example.com:443"
```

## Supported Agents

| Agent | Command | Image | Default Resources |
|---|---|---|---|
| Claude Code | `bbox claude-code` | `ghcr.io/stacklok/brood-box/claude-code` | 2 vCPUs, 4 GiB RAM |
| Codex | `bbox codex` | `ghcr.io/stacklok/brood-box/codex` | 2 vCPUs, 4 GiB RAM |
| OpenCode | `bbox opencode` | `ghcr.io/stacklok/brood-box/opencode` | 2 vCPUs, 4 GiB RAM |
| Hermes | `bbox hermes` | `ghcr.io/stacklok/brood-box/hermes` | 2 vCPUs, 4 GiB RAM |
| Gemini CLI | `bbox gemini` | `ghcr.io/stacklok/brood-box/gemini` | 2 vCPUs, 4 GiB RAM |

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
bbox claude-code
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
  Guest boots (bbox-init as PID 1):
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

The guest VM runs a custom Go init binary (`bbox-init`) as PID 1. No shell scripts,
no external sshd, no iproute2. Everything the guest needs is compiled into a single
binary that handles boot, networking, workspace mounting, and an embedded SSH server.

The workspace snapshot uses FICLONE on Linux and `clonefile(2)` on macOS for near-instant copy-on-write cloning.
When the agent is done, a SHA-256 based differ detects changes, and the review UI
shows unified diffs for each file. Accepted changes are flushed back with hash
re-verification to prevent TOCTOU attacks. The VM is explicitly stopped before
review begins, so the agent can't modify files during your review.

## Security Model

Brood Box's isolation is built on several layers:

- **KVM hardware virtualization** -- The agent runs in a real VM, not a container with a shared kernel
- **Ephemeral SSH keys** -- ECDSA P-256 keys generated per session, destroyed on exit
- **Localhost-only networking** -- SSH port forwards bind to 127.0.0.1 only
- **Non-overridable security patterns** -- Files like `.env`, `*.pem`, `.ssh/`, `.aws/` are always excluded from snapshots, even if `.broodboxignore` tries to negate them
- **Shell-escaped environment injection** -- All forwarded values are single-quote escaped
- **VM stopped before review** -- Prevents the agent from modifying files while you're reviewing
- **Hash verification on flush** -- Files are re-hashed between diff and flush to catch any modifications
- **Permission stripping** -- setuid, setgid, and sticky bits are stripped when flushing changes
- **Path traversal protection** -- Symlinks are validated in-bounds before copying
- **Per-workspace config restrictions** -- `review.enabled` and egress widening are ignored in `.broodbox.yaml`

## Documentation

Detailed documentation lives in the [`docs/`](docs/) directory:

| Document | Description |
|----------|-------------|
| [User Guide](docs/USER_GUIDE.md) | Full CLI reference, configuration, snapshot isolation, egress firewall, MCP proxy, and troubleshooting |
| [Architecture](docs/ARCHITECTURE.md) | DDD layers, dependency injection, VM lifecycle, guest environment, and security model |
| [Development Guide](docs/DEVELOPMENT.md) | Prerequisites, task commands, adding agents, writing tests, and code conventions |
| [macOS Support](docs/MACOS.md) | Apple Silicon setup, building with Homebrew libkrun, and macOS-specific troubleshooting |

## Building from Source

```bash
# Build self-contained bbox (downloads + embeds go-microvm runtime)
task build

# Build bbox + go-microvm-runner from system libkrun (requires libkrun-devel)
task build-dev-system

# Build guest init binary
task build-init

# Run tests
task test

# Lint
task lint

# Format + lint + test
task verify

# Build guest VM images (requires docker or podman)
task image-all
```

Always use `task` for building, testing, and linting. The Taskfile sets
critical flags, ldflags, and environment variables that raw `go` commands miss.
See the [Development Guide](docs/DEVELOPMENT.md) for the full command reference.

## Contributing

Contributions are welcome! Please open an issue to discuss your idea before submitting a PR.

The project follows strict DDD (Domain-Driven Design) layered architecture:

- **[Architecture overview](docs/ARCHITECTURE.md)** for understanding the layers and design decisions
- **[Development guide](docs/DEVELOPMENT.md)** for setting up your environment, running tests, and code conventions

## License

[Apache-2.0](LICENSE)

Copyright 2025 [Stacklok, Inc.](https://stacklok.com)
