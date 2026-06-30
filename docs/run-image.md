<!-- SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc. -->
<!-- SPDX-License-Identifier: Apache-2.0 -->

# `bbox run-image` — ephemeral one-shot

`bbox run-image IMAGE [flags] -- CMD [args...]` boots an arbitrary OCI image in
a hardware-isolated microVM, runs `CMD` inside it, and persists nothing. It
builds an in-memory agent from the flags (no config-file mutation, no registry
persistence) and runs it through the normal sandbox path — the same path a
built-in or custom agent takes.

The image is the first positional argument. The command to run inside the VM
follows a literal `--` and is **required**.

```bash
# Boot an arbitrary image and run a command
bbox run-image ghcr.io/jbarslox/aider-bbox:latest -- aider

# Restrict egress and forward only one env var
bbox run-image ubuntu:24.04 \
  --env OPENAI_API_KEY \
  --egress-profile standard \
  --allow-host api.openai.com:443 \
  -- python -m http.server

# Enable the MCP proxy (the agent discovers it via BBOX_MCP_URL)
bbox run-image ghcr.io/me/my-tool:latest --mcp -- my-tool
```

## Ephemeral defaults

`run-image` is safer-by-default than a custom agent declared in config:

| Feature | Default | Opt in / tighten |
|---|---|---|
| Credential persistence | **OFF** | (no opt-in — by design ephemeral) |
| Host settings import | **OFF** | (no opt-in — ephemeral) |
| Env forwarding | **EMPTY** (forward nothing) | `--env` (repeatable) |
| Git token forwarding | **OFF** | `--git-token` |
| SSH agent forwarding | **OFF** | `--git-ssh-agent` |
| MCP proxy | **OFF** | `--mcp` |
| Egress | **permissive** (disclosed on stderr) | `--egress-profile standard --allow-host ...` |
| Snapshot isolation | **ON** (same as `bbox`) | `--workspace-mode=direct` |
| Interactive review | OFF | `--review` |

Egress defaults to `permissive` so the bare `bbox run-image IMG -- cmd` example
boots without first declaring egress hosts. A one-line disclosure is printed to
stderr each run. Tighten with `--egress-profile standard` (or `locked`) plus
`--allow-host`.

Unlike the root command (which forwards `GITHUB_TOKEN`/`GH_TOKEN` and the SSH
agent by default and lets `--no-git-token`/`--no-git-ssh-agent` disable them),
`run-image` forwards **neither** into an arbitrary image by default. Opt in with
`--git-token` / `--git-ssh-agent`; a stderr line is printed when either is set.
This is the safer posture for an ephemeral run against an untrusted image.

`--seed-credentials` is rejected: credential persistence is always OFF for an
ephemeral run, so seeding would be a silent no-op.

## MCP

`--mcp` enables the MCP tool proxy and forces `mcp.mode=env`: the proxy is
exposed to the agent only via the universal `BBOX_MCP_URL` environment variable.
There is no config-file injector for an arbitrary image. The default
authorization profile for a run-image agent with MCP enabled is `safe-tools`
(the custom-agent default); override with `--mcp-authz-profile`.

## Name derivation

When `--name` is unset, the agent name (used for the VM name, logs, and
`BBOX_AGENT_NAME`) is derived from the image reference: the repository basename
(last path segment, after stripping any tag/digest), lower-cased, with runs of
non-`[a-z0-9-]` replaced by `-`. Example: `ghcr.io/me/Aider-Bbox:latest` →
`aider-bbox`. Pass `--name` to override.

`--name` may not collide with a built-in agent **or a config-declared custom
agent** (a data-only run-image entry would shadow the built-in's plugin or
silently overwrite the custom agent); choose a distinct name. When the name is
derived (no `--name`) and still collides, pass `--name` to override.

## Minimum image contract

An arbitrary image used with `run-image` must provide:

- A Linux rootfs (the image is extracted as the VM rootfs). `x86_64` or `arm64`
  matching the host.
- The agent command (the `-- CMD`) resolvable on `PATH` inside the image, or an
  absolute path.
- A POSIX shell at `/bin/sh` (used by `bbox-init` for command invocation).
- A non-root sandbox user expected at UID 1000 with home `/home/sandbox`.
  `bbox-init` provisions this; the image should not override it destructively.
  If the image lacks the user, `bbox-init` creates it.
- A standard CA bundle at `/etc/ssl/certs` for outbound TLS.

`bbox-init` is **not** in the image — it is embedded by the VM runtime and runs
as PID 1. The image only provides the userspace/agent tooling.

## Flags

`run-image` accepts a subset of the root command's flags. Notable differences:

- `--env` / `--env-forward` — both are aliases; forward a host env var (exact
  name or glob like `PREFIX_*`), repeatable. Empty by default.
- `--mcp` — enables the MCP proxy (off by default; the opposite of the root
  command, where MCP is on by default and `--no-mcp` disables it).
- `--egress-profile` — defaults to `permissive`.
- `--git-token` / `--git-ssh-agent` — opt-in (default OFF) forwarding of the git
  token / SSH agent; the inverse of the root command's `--no-git-token` /
  `--no-git-ssh-agent`.
- `--seed-credentials` — registered but rejected: incompatible with ephemeral
  runs (credential persistence is always OFF).
- `--image` is **not** exposed (the image is the positional arg).
- `--exec` is **not** exposed (the command is the `--` args).

All resource (`--cpus`, `--memory`, `--tmp-size`), workspace (`--workspace`,
`--workspace-mode`, `--review`, `--exclude`, `--yes`), networking
(`--ssh-port`, `--port`, `--allow-host`), MCP, runtime (`--no-firmware-download`,
`--no-image-cache`, `--pull`), and observability (`--debug`, `--log-file`,
`--trace`, `--timings`) flags work as on the root command.

Global config (`~/.config/broodbox/config.yaml`) is still loaded for defaults
(egress hosts, MCP defaults, resource ceilings); `--config` selects an
alternate path.
