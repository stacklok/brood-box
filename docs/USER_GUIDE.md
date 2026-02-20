# User Guide

apiary runs coding agents (Claude Code, Codex, OpenCode) inside
hardware-isolated microVMs powered by [libkrun](https://github.com/containers/libkrun).
Your workspace is mounted into the VM, API keys are forwarded automatically,
and you get a live interactive terminal session.

## Quick Start

```bash
# Run Claude Code in the current directory
apiary claude-code

# Run Codex with more resources
apiary codex --cpus 4 --memory 4096

# Run OpenCode on a specific project
apiary opencode --workspace /path/to/project
```

## Prerequisites

- **Linux** with KVM support (`/dev/kvm` must be accessible)
- **libkrun-devel** installed (for building propolis-runner)
- **propolis** checked out at `../propolis` relative to apiary

Verify KVM access:

```bash
ls -la /dev/kvm
# If missing: sudo modprobe kvm && sudo modprobe kvm_intel  # or kvm_amd
# If permission denied: sudo usermod -aG kvm $USER  (then re-login)
```

## Built-in Agents

| Agent | Image | Command | Forwarded Env Vars |
|-------|-------|---------|--------------------|
| `claude-code` | `ghcr.io/stacklok/apiary/claude-code:latest` | `claude` | `ANTHROPIC_API_KEY`, `CLAUDE_*` |
| `codex` | `ghcr.io/stacklok/apiary/codex:latest` | `codex` | `OPENAI_API_KEY`, `CODEX_*` |
| `opencode` | `ghcr.io/stacklok/apiary/opencode:latest` | `opencode` | `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `OPENROUTER_API_KEY`, `OPENCODE_*` |

List agents:

```bash
apiary list
```

## CLI Reference

```
apiary <agent-name> [flags] [-- <agent-args...>]
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--cpus` | Agent default (2) | Number of vCPUs for the VM |
| `--memory` | Agent default (2048) | RAM in MiB |
| `--workspace` | Current directory | Host directory mounted as `/workspace` |
| `--ssh-port` | Auto-pick | Host port forwarded to guest SSH (port 22) |
| `--config` | `~/.config/apiary/config.yaml` | Config file path |
| `--image` | Agent default | Override the OCI image reference |
| `--no-review` | `false` | Disable snapshot isolation, mount workspace directly |
| `--exclude` | (none) | Additional gitignore-style exclude patterns (repeatable) |
| `--debug` | `false` | Enable debug logging |

Pass agent-specific arguments after `--` so they are not parsed by apiary:

```bash
apiary claude-code -- --help
```

### Subcommands

| Command | Description |
|---------|-------------|
| `list` | List all available agents |
| `completion` | Generate shell completion scripts |

## What Happens When You Run It

1. **Resolve agent** -- Looks up the agent name in the built-in registry
   (and any custom agents from your config file).
2. **Load config** -- Reads `~/.config/apiary/config.yaml` and
   per-workspace `.apiary.yaml`, merges overrides with built-in
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

Create `~/.config/apiary/config.yaml` to customize defaults,
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

## Workspace Snapshot Isolation

By default, apiary creates a copy-on-write (COW) snapshot of your
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
apiary claude-code --no-review
```

### Exclude Patterns

Certain files are automatically excluded from the snapshot:

**Security patterns** (non-overridable — always excluded):
- `.env*`, `*.pem`, `*.key`, `.ssh/`, `.aws/`, `.gcp/`, `credentials.json`
- `.apiary.yaml`, `.kube/config`, `.gnupg/`, and more

**Performance patterns** (overridable — can be negated in `.apiaryignore`):
- `node_modules/`, `vendor/`, `.git/objects/`, `__pycache__/`, `target/`,
  `build/`, `dist/`, `.venv/`, `.tox/`

Add extra patterns via the CLI:

```bash
apiary claude-code --exclude "*.log" --exclude "tmp/"
```

### `.apiaryignore`

Create a `.apiaryignore` file in your workspace root (gitignore syntax)
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

Create `.apiary.yaml` in your workspace root to set per-project
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

## Signals and Cleanup

- **Ctrl+C (SIGINT)** and **SIGTERM** trigger graceful shutdown:
  the SSH session is terminated, the VM is stopped, and temp files are cleaned up.
- **Terminal resize** (SIGWINCH) is forwarded to the VM session automatically.
- Ephemeral SSH keys are generated per session and deleted on exit.

## Building Guest Images

apiary runs agents inside OCI images that boot as microVMs. Pre-built
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
| `ghcr.io/stacklok/apiary/base:latest` | Wolfi + sshd, bash, git, coreutils |
| `ghcr.io/stacklok/apiary/claude-code:latest` | Base + Claude Code binary |
| `ghcr.io/stacklok/apiary/codex:latest` | Base + Codex binary |
| `ghcr.io/stacklok/apiary/opencode:latest` | Base + OpenCode binary |

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
`apiary list` to see available agents. Custom agents need an
`image` field in the config file.

### VM fails to start

Check that:
- `/dev/kvm` exists and is accessible
- `propolis-runner` is built and in your PATH (or use `task build-dev`)
- The OCI image exists and is pullable

### SSH connection refused

The guest may still be booting. apiary waits for SSH automatically,
but if the image's init system is slow, the timeout may be exceeded.
Check the VM console log for errors.

### Environment variables not available in the VM

Verify the variable is set in your host shell and matches a pattern
in the agent's `env_forward` list. Use `env | grep ANTHROPIC` to check.
