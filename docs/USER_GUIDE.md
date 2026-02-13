# User Guide

sandbox-agent runs coding agents (Claude Code, Codex, OpenCode) inside
hardware-isolated microVMs powered by [libkrun](https://github.com/containers/libkrun).
Your workspace is mounted into the VM, API keys are forwarded automatically,
and you get a live interactive terminal session.

## Quick Start

```bash
# Run Claude Code in the current directory
sandbox-agent claude-code

# Run Codex with more resources
sandbox-agent codex --cpus 4 --memory 4096

# Run OpenCode on a specific project
sandbox-agent opencode --workspace /path/to/project
```

## Prerequisites

- **Linux** with KVM support (`/dev/kvm` must be accessible)
- **libkrun-devel** installed (for building propolis-runner)
- **propolis** checked out at `../propolis` relative to sandbox-agent

Verify KVM access:

```bash
ls -la /dev/kvm
# If missing: sudo modprobe kvm && sudo modprobe kvm_intel  # or kvm_amd
# If permission denied: sudo usermod -aG kvm $USER  (then re-login)
```

## Built-in Agents

| Agent | Image | Command | Forwarded Env Vars |
|-------|-------|---------|--------------------|
| `claude-code` | `ghcr.io/stacklok/sandbox-agent/claude-code:latest` | `claude` | `ANTHROPIC_API_KEY`, `CLAUDE_*` |
| `codex` | `ghcr.io/stacklok/sandbox-agent/codex:latest` | `codex` | `OPENAI_API_KEY`, `CODEX_*` |
| `opencode` | `ghcr.io/stacklok/sandbox-agent/opencode:latest` | `opencode` | `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `OPENROUTER_API_KEY`, `OPENCODE_*` |

List agents:

```bash
sandbox-agent list
```

## CLI Reference

```
sandbox-agent <agent-name> [flags]
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--cpus` | Agent default (2) | Number of vCPUs for the VM |
| `--memory` | Agent default (2048) | RAM in MiB |
| `--workspace` | Current directory | Host directory mounted as `/workspace` |
| `--ssh-port` | Auto-pick | Host port forwarded to guest SSH (port 22) |
| `--config` | `~/.config/sandbox-agent/config.yaml` | Config file path |
| `--image` | Agent default | Override the OCI image reference |

### Subcommands

| Command | Description |
|---------|-------------|
| `list` | List all available agents |
| `completion` | Generate shell completion scripts |

## What Happens When You Run It

1. **Resolve agent** -- Looks up the agent name in the built-in registry
   (and any custom agents from your config file).
2. **Load config** -- Reads `~/.config/sandbox-agent/config.yaml` and merges
   overrides with built-in agent defaults and CLI flags.
3. **Collect environment** -- Matches your host environment variables against
   the agent's forwarding patterns (e.g., `ANTHROPIC_API_KEY`, `CLAUDE_*`)
   and collects them for injection into the VM.
4. **Boot VM** -- Pulls the OCI image, extracts it into a rootfs, injects
   SSH keys + init script + env file, and starts a libkrun microVM.
5. **Wait for SSH** -- Polls the VM until the SSH server is ready.
6. **Interactive session** -- Opens a PTY-forwarded SSH session into the VM
   with your terminal size, sources the env file, `cd /workspace`, and
   `exec`s the agent command.
7. **Cleanup** -- When the agent exits (or you press Ctrl+C), the VM is
   gracefully shut down and ephemeral SSH keys are deleted.

## Configuration

Create `~/.config/sandbox-agent/config.yaml` to customize defaults,
override built-in agents, or define custom agents.

```yaml
# Global resource defaults (applied when agent doesn't specify its own)
defaults:
  cpus: 4
  memory: 4096

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

## Signals and Cleanup

- **Ctrl+C (SIGINT)** and **SIGTERM** trigger graceful shutdown:
  the SSH session is terminated, the VM is stopped, and temp files are cleaned up.
- **Terminal resize** (SIGWINCH) is forwarded to the VM session automatically.
- Ephemeral SSH keys are generated per session and deleted on exit.

## Building Guest Images

sandbox-agent runs agents inside OCI images that boot as microVMs. Pre-built
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
| `ghcr.io/stacklok/sandbox-agent/base:latest` | Wolfi + sshd, bash, git, coreutils |
| `ghcr.io/stacklok/sandbox-agent/claude-code:latest` | Base + Claude Code binary |
| `ghcr.io/stacklok/sandbox-agent/codex:latest` | Base + Codex binary |
| `ghcr.io/stacklok/sandbox-agent/opencode:latest` | Base + OpenCode binary |

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
`sandbox-agent list` to see available agents. Custom agents need an
`image` field in the config file.

### VM fails to start

Check that:
- `/dev/kvm` exists and is accessible
- `propolis-runner` is built and in your PATH (or use `task build-dev`)
- The OCI image exists and is pullable

### SSH connection refused

The guest may still be booting. sandbox-agent waits for SSH automatically,
but if the image's init system is slow, the timeout may be exceeded.
Check the VM console log for errors.

### Environment variables not available in the VM

Verify the variable is set in your host shell and matches a pattern
in the agent's `env_forward` list. Use `env | grep ANTHROPIC` to check.
