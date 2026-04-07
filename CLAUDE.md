# brood-box

CLI tool for running coding agents (Claude Code, Codex, OpenCode) inside hardware-isolated microVMs.
Wraps the go-microvm framework with an opinionated CLI.

Module: `github.com/stacklok/brood-box`

## Commands — ALWAYS use `task` (Taskfile.yaml)

**IMPORTANT**: ALWAYS use `task <target>` for building, testing, linting, formatting, and running. NEVER invoke `go build`, `go test`, `golangci-lint`, `go fmt`, `goimports`, `docker build`, or `podman build` directly — the Taskfile wraps these with the correct flags, ldflags, env vars, and dependency ordering. Running raw commands will produce incorrect builds or miss steps.

```bash
task build                  # Build self-contained bbox with embedded go-microvm runtime
task build-init             # Cross-compile bbox-init for guest VM
task build-dev              # Alias for task build (Linux)
task build-dev-darwin       # Alias for task build (macOS)
task build-dev-system       # Build bbox + go-microvm-runner from system libkrun (Linux, requires libkrun-devel)
task build-dev-system-darwin # Build bbox + go-microvm-runner from system libkrun (macOS, requires Homebrew libkrun)
task fetch-runtime          # Download pre-built go-microvm runtime from GitHub Release
task fetch-firmware         # Optional: prefetch go-microvm firmware
task test                   # go test -v -race ./...
task test-coverage          # Run tests with coverage report
task lint                   # golangci-lint run ./...
task lint-fix               # Auto-fix lint issues
task fmt                    # go fmt + goimports
task tidy                   # go mod tidy
task verify                 # fmt + lint + test
task install                # Install bbox to GOPATH/bin
task run                    # Build and run
task clean                  # Remove bin/ and coverage files
task image-base             # Build base guest image
task image-claude-code      # Build claude-code guest image
task image-codex            # Build codex guest image
task image-opencode         # Build opencode guest image
task image-all              # Build all guest images
task image-push             # Push all images to GHCR
```

The only exception is running a single test, where raw `go test` is acceptable:
`go test -v -race -run TestName ./path/to/package`

## Architecture — Strict DDD (Domain-Driven Design)

This project follows DDD layered architecture with dependency injection **strictly and without exception**. Every new type, interface, and function MUST be placed in the correct layer. Violating layer boundaries is a blocking issue — do not merge code that breaks these rules.

### Layers

**Domain** (`pkg/domain/`) — Pure types and interfaces. ZERO I/O, ZERO external dependencies, ZERO side effects. Domain packages define _what_ things are and _what_ operations exist, never _how_ they are performed. Public so external modules can import the shared ubiquitous language:
- `pkg/domain/agent/` — Agent value object, env forwarding
- `pkg/domain/bytesize/` — ByteSize value object for human-readable memory sizes
- `pkg/domain/config/` — Config types, merge logic
- `pkg/domain/vm/` — VMRunner, VM, VMConfig interfaces
- `pkg/domain/session/` — TerminalSession interface
- `pkg/domain/workspace/` — WorkspaceCloner interface, Snapshot type
- `pkg/domain/snapshot/` — FileChange, ExcludeConfig, Matcher, Differ, Reviewer, Flusher
- `pkg/domain/credential/` — Store, FileStore, Seeder interfaces
- `pkg/domain/settings/` — Entry, Manifest, FieldFilter, Injector interface for host-to-guest settings injection
- `pkg/domain/egress/` — DNS-aware egress Policy, Host, ProfileName, Resolve()
- `pkg/domain/git/` — Identity, IdentityProvider interface
- `pkg/domain/hostservice/` — Service, Provider for HTTP services exposed to guest
- `pkg/domain/progress/` — Phase enum, Observer interface for lifecycle reporting

**Application** (`pkg/sandbox/`) — Orchestration only. Depends on domain interfaces, never on infrastructure. Contains no I/O implementations. Public so library consumers can drive the same SandboxRunner API:
- `pkg/sandbox/` — SandboxRunner orchestrator (application service), SandboxConfig SDK contract

**Runtime Factory** (`pkg/runtime/`) — Public helper that wires default infrastructure for SDK consumers. Keeps `internal/infra/` private while providing a supported path to construct `SandboxRunner` with standard implementations:
- `pkg/runtime/` — `NewDefaultSandboxDeps()`, `NewDefaultSandboxRunner()`, exclude matcher builders

**Infrastructure** (`internal/infra/`) — Concrete implementations of domain interfaces. This is the only layer that touches I/O, external libraries, and system calls:
- `internal/infra/vm/` — go-microvm VMRunner implementation, rootfs hooks
- `internal/infra/ssh/` — Interactive PTY terminal session
- `internal/infra/config/` — YAML config loader
- `internal/infra/agent/` — Built-in agent registry
- `internal/infra/mcp/` — VMCPProvider (vmcp proxy), Cedar authz profile resolver
- `internal/infra/exclude/` — Gitignore-compatible exclude pattern loading + two-tier matching
- `internal/infra/workspace/` — COW workspace cloning (FICLONE on Linux, clonefile on macOS, copy fallback)
- `internal/infra/diff/` — SHA-256 based file diff engine
- `internal/infra/review/` — Interactive per-file terminal review, auto-accept reviewer, flusher with hash verification
- `internal/infra/credential/` — FS-based credential store, Claude credential seeder
- `internal/infra/settings/` — FSInjector for host-to-guest settings injection (file copy, dir recursion, merge-file with field filtering, JSONC support)
- `internal/infra/git/` — Host identity provider, `.git/config` credential sanitizer
- `internal/infra/logging/` — Custom slog file handler
- `internal/infra/terminal/` — OS terminal wrapper (raw mode, SIGWINCH)
- `internal/infra/progress/` — Spinner, simple, and log-based progress observers
- `internal/infra/process/` — Process management utilities
- `internal/infra/tracing/` — OpenTelemetry TracerProvider factory (file exporter, sync flush)

**Guest VM** (`internal/guest/` + `cmd/bbox-init/`, Linux only — runs inside the microVM):
- `internal/guest/homefs/` — Writable home directory overlay (overlayfs/tmpfs)
- `cmd/bbox-init/` — Guest PID 1 init binary (compiled Go); boot, mount, network, env, sshd, and reaper logic lives in the go-microvm module under `guest/`

**CLI + Composition Root** (`cmd/`):
- `cmd/bbox/main.go` — Composition root, wires dependencies, Cobra CLI
- `internal/version/` — Version/commit info via ldflags

### DDD Rules (non-negotiable)

- **`pkg/domain/` NEVER imports from `internal/infra/` or `pkg/sandbox/`.** Interfaces live in domain, implementations in infra. No exceptions.
- **`pkg/sandbox/` NEVER imports from `internal/infra/`.** The application layer depends only on domain interfaces; concrete implementations are injected by the composition root (`cmd/`).
- **New interfaces go in `pkg/domain/`**, new implementations go in `internal/infra/`**. If you need a new capability, define the interface in the appropriate domain package first, then implement it in infra.
- **No business logic in `infra/`**. Infrastructure adapts external systems to domain interfaces — it does not make business decisions.
- **No I/O in `domain/`**. Domain types must be testable without mocks, fakes, or network access.

## Conventions

- **SPDX headers required** on every `.go` and `.yaml` file:
  ```
  // SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
  // SPDX-License-Identifier: Apache-2.0
  ```
- Use `log/slog` exclusively — no `fmt.Println` or `log.Printf` in library code.
- Wrap errors with `fmt.Errorf("context: %w", err)` forming readable chains.
- Prefer table-driven tests. Test files go alongside the code they test.
- Imperative mood commit messages, capitalize, no trailing period, limit subject to 50 chars.
- IMPORTANT: Never use `git add -A`. Stage specific files only.

## Workspace Snapshot Isolation

Snapshot isolation is always active: a COW snapshot is created before the VM starts, and changes are flushed back after the agent finishes. Git credential sanitization runs automatically. Use `--review` to interactively approve or reject each changed file; without it, all changes are auto-accepted.

- `--review` — Enable interactive per-file review (snapshot isolation is always active)
- `--exclude "pattern"` — Additional gitignore-style exclude patterns (repeatable)
- `.broodboxignore` — Per-workspace exclude file (gitignore syntax) in workspace root
- `.broodbox.yaml` — Per-workspace config file (merged into global config; `review.enabled` controls interactive review and is **ignored** from workspace config for security)
- Security patterns (`.env*`, `*.pem`, `.ssh/`, `.broodbox.yaml`, etc.) are **non-overridable** — cannot be negated
- Performance patterns (`node_modules/`, `vendor/`, etc.) can be negated in `.broodboxignore`

Global config (`~/.config/broodbox/config.yaml`):
```yaml
review:
  enabled: true          # Enable interactive per-file review
  exclude_patterns:
    - "*.log"
    - "tmp/"
```

Execution order: create snapshot → start VM → terminal → stop VM → diff → review/auto-accept → flush → cleanup.

## MCP Authorization

The MCP proxy supports opt-in authorization profiles that restrict what MCP operations the agent can perform. Default is `full-access` (no restrictions). Authorization uses toolhive's Cedar-based authz middleware with annotation enrichment.

### Profiles

| Profile | Agent can do | Cedar behavior |
|---|---|---|
| `full-access` | Everything (default) | No authz middleware |
| `observe` | List + read tools/prompts/resources | 5 static permit policies for list/read actions |
| `safe-tools` | Above + non-destructive closed-world tools | Above + `when` clauses on `resource.readOnlyHint`, `resource.destructiveHint`, `resource.openWorldHint` |
| `custom` | Operator-defined | Cedar policies from vmcp config YAML (`--mcp-config`) |

### Usage

```bash
bbox claude-code --mcp-authz-profile observe
bbox claude-code --mcp-authz-profile safe-tools
bbox claude-code --mcp-authz-profile custom --mcp-config /path/to/vmcp.yaml
```

Or via global config (`~/.config/broodbox/config.yaml`):
```yaml
mcp:
  authz:
    profile: observe
```

### Custom profile

The `custom` profile delegates to Cedar policies defined in the MCP config YAML's `authz.policies` section. When `--mcp-config` points to a YAML with Cedar policies, the `custom` profile is inferred automatically:

```yaml
# mcp-config.yaml
authz:
  policies:
    - 'permit(principal, action == Action::"list_tools", resource);'
    - 'permit(principal, action == Action::"call_tool", resource == Tool::"search_code");'
```

The same config can be inlined in the global config file:

```yaml
mcp:
  config:
    authz:
      policies:
        - 'permit(principal, action == Action::"list_tools", resource);'
```

### Security constraints

- **Tighten-only merge**: workspace-local `.broodbox.yaml` can only make the profile stricter, never more permissive (same pattern as egress profiles).
- **`custom` is global/CLI only**: workspace-local config cannot set `profile: custom` — it would allow a repository to supply its own Cedar policies.
- Profile constants and strictness ordering live in `pkg/domain/config/` (domain layer).
- Cedar policy resolution lives in `internal/infra/mcp/profiles.go` (infrastructure layer).

## CI/CD

Three GitHub Actions workflows:

- **CI** (`.github/workflows/ci.yaml`) — Runs on pushes to `main` and PRs. Jobs: test, lint, build (matrix: ubuntu + macOS). Also validates image builds (build-only, no push).
- **Images** (`.github/workflows/images.yaml`) — Dedicated image build and push. Triggers: weekly schedule (Monday 06:00 UTC), manual dispatch, and pushes to `main` that touch `images/**`. Pushes all guest images (base, claude-code, codex, opencode) as `:latest` to GHCR.
- **Release** (`.github/workflows/release.yaml`) — Triggered by `v*` tag pushes. Builds `bbox` binaries natively on linux/amd64, linux/arm64, and darwin/arm64 using `task build` (embeds bbox-init + go-microvm runtime). Packages tarballs, generates SHA-256 checksums, and creates a GitHub Release with auto-generated notes.

To cut a release:
```bash
git tag v0.0.X
git push origin v0.0.X
```

Image tagging is `:latest` only — images are not versioned with release tags. They are rebuilt weekly and on any change to `images/`.

## Things That Will Bite You

- **go-microvm is a tagged dependency**: `go.mod` depends on `github.com/stacklok/go-microvm` as a versioned module. `build` downloads pre-built go-microvm runtime artifacts and embeds them — no local checkout or system libkrun needed. Use `build-dev-system` to build go-microvm-runner from source (requires `libkrun-devel`).
- **CGO boundary**: Brood Box itself is pure Go (`CGO_ENABLED=0`). The embedded go-microvm-runner was pre-built with CGO elsewhere — no CGO needed at bbox build time.
- **`gh` CLI dependency**: `task fetch-runtime` uses the GitHub CLI (`gh`) to download release artifacts. Firmware is downloaded at runtime via HTTPS by default.
- **Domain purity**: `pkg/domain/` must never import from `internal/infra/` or `pkg/sandbox/`. This is the most important architectural invariant — break it and you break the entire DDD foundation.
- **Always use `task`**: Never run `go build`, `go test ./...`, `golangci-lint`, `go fmt`, or `goimports` directly. The Taskfile sets critical env vars and flags. Raw commands will silently produce wrong results.
- **macOS entitlements**: `go-microvm-runner` must be code-signed with `assets/entitlements.plist` on macOS (Hypervisor.framework requirement). `task build-dev-system-darwin` handles this automatically. On macOS, install libkrun via `brew tap slp/krun && brew install libkrun libkrunfw`.

## Debugging & Profiling

### Startup timing

```bash
bbox claude-code --timings                    # Per-phase timing summary (user-facing)
bbox claude-code --trace                      # OTel trace JSON to VM data dir
bbox claude-code --trace --timings            # Both
BBOX_TRACE=1 bbox claude-code                 # Env var alternative to --trace
```

`--timings` prints a human-readable summary to stderr. `--trace` writes detailed OTel spans (with parent-child hierarchy) to `trace.json` in the VM data dir. View traces with `cat trace.json | jq .` or import into Jaeger.

Key spans in the trace hierarchy:
```
bbox.Prepare → bbox.StartVM → microvm.Run → microvm.RootfsClone / microvm.SSHWaitReady
```

### Preserving VM data for debugging

By default, the VM data directory (console.log, vm.log, rootfs-work) is cleaned up after each run. To preserve it:

```bash
BBOX_KEEP_VM_DATA=1 bbox claude-code --trace
```

The data dir path is logged in broodbox.log. Note: the VM data dir path differs from the session dir — check the log for the actual path.

### Local go-microvm development

To test changes to go-microvm locally without publishing a release:

```bash
# Add replace directive to go.mod (do NOT commit this)
echo 'replace github.com/stacklok/go-microvm => ../go-microvm' >> go.mod
go mod tidy

# Rebuild — must force bbox-init since it embeds go-microvm guest code
task build-init --force && task build --force
```

**Gotcha**: `task build` runs `fetch-runtime` which downloads runtime binaries from the go-microvm GitHub Release matching the tag in go.mod. If your go-microvm tag has no release assets (e.g. a quick patch release), `fetch-runtime` will fail. Workaround: the runtime binary (`go-microvm-runner` + `libkrun.so`) rarely changes — skip `fetch-runtime` and build directly:

```bash
task build-init --force
# build bbox directly (runtime from previous version is fine)
task build
```

Remove the `replace` directive before committing.

## Verification

After any code change:
```bash
task fmt && task lint    # Format and lint
task test                # Full test suite with race detector
```
