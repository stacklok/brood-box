# Architecture

apiary follows Domain-Driven Design (DDD) with strict layering
and dependency injection throughout.

## Layers

```
cmd/apiary/main.go     (composition root — wires everything)
cmd/apiary-init/main.go      (guest PID 1 init binary)
        │
        ▼
   pkg/sandbox/               (application service — orchestrates domain)
   pkg/runtime/               (public factory — wires default infra)
        │
   ┌────┼─────────────────────────────────────────────┐
   ▼    ▼                ▼                ▼            ▼
pkg/domain/agent/   pkg/domain/config/  pkg/domain/vm/   pkg/domain/session/
pkg/domain/snapshot/ pkg/domain/workspace/ pkg/domain/egress/
pkg/domain/git/     pkg/domain/hostservice/ pkg/domain/progress/
   (pure domain — no imports from infra, public SDK)
   │
   │    ┌────────────────┬────────────────┬──────────────┐
    ▼    ▼                ▼                ▼              ▼
infra/vm/         infra/ssh/        infra/config/   infra/agent/
(propolis)        (PTY terminal)    (YAML loader)   (built-in registry)
infra/exclude/    infra/workspace/  infra/diff/     infra/review/
(pattern match)   (COW cloning)     (SHA-256 diff)  (reviewer+flusher)
infra/git/        infra/mcp/        infra/terminal/ infra/progress/
(identity+sanitize)(MCP proxy)      (OS terminal)   (spinner+log)
infra/logging/
(slog file handler)

guest/boot/  guest/mount/  guest/network/  guest/env/  guest/sshd/  guest/reaper/
   (guest VM packages — Linux only, runs inside microVM)
   (note: these live in github.com/stacklok/propolis under guest/)
```

### Domain Layer (`pkg/domain/`)

Pure business logic with zero infrastructure dependencies. This layer
defines the core types and interfaces.

- **`agent/agent.go`** -- `Agent` value object (name, image, command,
  env patterns, resource defaults) and `Registry` interface.
- **`agent/env.go`** -- `ForwardEnv()` collects host env vars matching
  patterns. `ShellEscape()` quotes values for safe shell injection.
  Both are pure functions with injectable `EnvProvider` for testing.
- **`config/config.go`** -- `Config`, `DefaultsConfig`, `AgentOverride`
  structs (pure data, YAML tags). `Merge()` combines agent + override +
  defaults with clear precedence rules.
- **`vm/vm.go`** -- `VMRunner` interface (`Start` → `VM`), `VM` interface
  (`Stop`, `SSHPort`, `DataDir`, `SSHKeyPath`), `VMConfig` value object.
- **`session/session.go`** -- `TerminalSession` interface (`Run` with
  `SessionOpts` for host, port, user, key path, command, I/O streams).
- **`workspace/workspace.go`** -- `WorkspaceCloner` interface
  (`CreateSnapshot` → `*Snapshot`), `Snapshot` type with `Cleanup()`.
- **`snapshot/snapshot.go`** -- `FileChange` (RelPath, Kind, UnifiedDiff,
  Hash), `ReviewDecision`, `ReviewResult`. Domain interfaces: `Matcher`,
  `Differ`, `Reviewer`, `Flusher`.
- **`snapshot/exclude.go`** -- `ExcludeConfig` with security patterns
  (non-overridable) and performance patterns (overridable).
- **`egress/egress.go`** -- DNS-aware egress firewall policies.
  `ProfileName` (`permissive`, `standard`, `locked`), `Host` (name,
  ports, protocol), `Policy` (allowed hosts). `Resolve()` maps a
  profile + agent hosts to a concrete policy. `Merge()` appends extra
  hosts. `Stricter()` compares profiles.
- **`git/git.go`** -- `Identity` (Name, Email) and `IdentityProvider`
  interface for resolving the host git user.
- **`hostservice/hostservice.go`** -- `Service` (Name, Port, Handler)
  and `Provider` interface for host-side HTTP services exposed to the
  guest (used by MCP proxy).
- **`progress/progress.go`** -- `Phase` enum (ResolvingAgent,
  CreatingSnapshot, StartingVM, Connecting, ShuttingDown,
  ComputingDiff, FlushingChanges, ConfiguringMCP, Cleaning) and
  `Observer` interface (`Start`, `Complete`, `Warn`, `Fail`).

**Rule**: `pkg/domain/` NEVER imports from `internal/infra/` or `pkg/sandbox/`.

### Application Layer (`pkg/sandbox/`)

The `SandboxRunner` orchestrates the full lifecycle:

1. Resolve agent from registry
2. Load config and merge overrides
3. Collect forwarded env vars
4. Create workspace snapshot (if review enabled)
5. Start VM via `VMRunner`
6. Run interactive terminal session
7. Stop VM explicitly (before diff/review)
8. Diff snapshot against original workspace
9. Interactive per-file review
10. Flush accepted changes to original workspace
11. Cleanup snapshot

All dependencies are injected via the `SandboxDeps` struct. The
orchestrator has no direct dependency on propolis, SSH libraries, or
the filesystem — only on domain interfaces. The `SandboxConfig` type
narrows the config surface so SDK consumers don't need the full CLI
config schema.

### Infrastructure Layer (`internal/infra/`)

Concrete implementations of domain interfaces and system integration.

- **`vm/runner.go`** -- `PropolisRunner` implements `VMRunner` using
  `propolis.Run()` with options for ports, virtio-fs, rootfs hooks,
  init override, and post-boot SSH readiness check.
- **`vm/hooks.go`** -- `RootFSHook` factories: `InjectInitBinary`
  (writes the compiled Go init binary) and `InjectMCPConfig` (writes
  agent-specific MCP config files). SSH keys, env files, and git
  config are injected by the runner directly via propolis options.
- **`ssh/terminal.go`** -- `InteractiveSession` implements PTY-forwarded
  SSH sessions with raw terminal mode, SIGWINCH handling, and context
  cancellation support.
- **`config/loader.go`** -- `Loader` reads YAML config from
  `$XDG_CONFIG_HOME/apiary/config.yaml` with graceful fallback
  when the file doesn't exist.
- **`agent/registry.go`** -- In-memory `Registry` pre-loaded with
  built-in agents (claude-code, codex, opencode). Supports adding
  custom agents from config.
- **`exclude/`** -- Two-tier gitignore-compatible pattern matching.
  Security patterns are non-overridable; performance patterns can be
  negated in `.apiaryignore`.
- **`workspace/`** -- COW workspace cloning using FICLONE (Linux),
  clonefile (macOS), or copy fallback. Includes stale snapshot cleanup
  and symlink validation for path traversal protection.
- **`diff/`** -- SHA-256 based file differ. Builds hash indices of
  original and snapshot directories, detects added/modified/deleted
  files, generates unified diffs.
- **`review/`** -- Interactive per-file terminal reviewer and filesystem
  flusher with hash re-verification between diff and flush (TOCTOU
  protection).
- **`git/`** -- `HostIdentityProvider` resolves git user.name/email
  from the host. `ConfigSanitizer` strips sensitive values from
  `.git/config` in snapshots.
- **`mcp/`** -- `VMCPProvider` integrates toolhive's vmcp library to
  discover MCP backends from ToolHive groups and expose an aggregated
  MCP proxy as an HTTP host service.
- **`terminal/`** -- `OSTerminal` wraps real terminal I/O with raw
  mode, SIGWINCH, and dimension queries.
- **`progress/`** -- `SpinnerObserver` (animated spinner for
  interactive terminals), `SimpleObserver` (line-based for
  non-interactive), `LogObserver` (structured slog output).
- **`logging/`** -- `FileHandler` is a custom slog handler that writes
  to a log file with timestamp formatting.

### Composition Root (`cmd/apiary/main.go`)

Wires all concrete implementations together:

- Creates the agent registry and registers custom agents from config
- Creates `PropolisRunner`, `InteractiveSession`, `Loader`, `OSEnvProvider`
- Injects everything into `SandboxRunner`
- Cobra CLI with positional agent name arg and flags
- Signal handling (SIGINT/SIGTERM) via `signal.NotifyContext`

### Runtime Factory (`pkg/runtime/`)

Public helper package that wires apiary's default infrastructure for SDK
consumers (for example, orchestration systems). This keeps `internal/infra/`
private while providing a supported path to construct `SandboxRunner` with
the standard VM runner, session implementation, differ, flusher, and workspace
cloner. It also provides helpers to build snapshot/diff matchers from
workspace ignore files.

## Dependency Injection

Every struct accepts interfaces via constructor injection. No global state.

```go
// Domain defines interfaces
type Registry interface {
    Get(name string) (Agent, error)
    List() []Agent
}

type EnvProvider interface {
    Environ() []string
}

// Sandbox layer accepts all deps via struct
type SandboxDeps struct {
    Registry    agent.Registry
    VMRunner    vm.VMRunner
    Terminal    session.TerminalSession
    Config      *sandbox.SandboxConfig
    EnvProvider agent.EnvProvider
    Logger      *slog.Logger

    // Snapshot isolation (nil when review disabled)
    WorkspaceCloner workspace.WorkspaceCloner
    Differ          snapshot.Differ
    Reviewer        snapshot.Reviewer
    Flusher         snapshot.Flusher
}

// Infra provides implementations
// vm.PropolisRunner implements vm.VMRunner
// ssh.InteractiveSession implements session.TerminalSession
// agent.Registry implements agent.Registry
```

This makes the sandbox layer fully testable with mocks — see
`pkg/sandbox/sandbox_test.go` for examples.

## VM Lifecycle

```
apiary claude-code
        │
        ▼
   Create workspace snapshot (if review enabled)
        │
        ▼
   Pull OCI image (propolis handles caching)
        │
        ▼
   Extract rootfs from layers
        │
        ▼
   Run rootfs hooks:
     1. InjectInitBinary → /apiary-init (compiled Go binary)
     2. InjectMCPConfig  → agent-specific MCP config (if MCP enabled)
   Propolis options inject:
     - SSH keys          → /home/sandbox/.ssh/authorized_keys
     - Env file          → /etc/sandbox-env
     - Git config        → /home/sandbox/.gitconfig
        │
        ▼
   Write .krun_config.json (init override → /apiary-init)
        │
        ▼
   Start networking (in-process, gvisor-tap-vsock)
        │
        ▼
   Spawn propolis-runner (libkrun microVM)
        │
        ▼
   Guest boots (/apiary-init as PID 1):
     apiary-init → guest/boot.Run():
       - Mount essential filesystems (/proc, /sys, /dev, /tmp, /run)
       - Configure loopback networking (netlink)
       - Mount virtiofs workspace → /workspace
       - Load /etc/sandbox-env into environment
       - Parse authorized_keys
       - Start embedded Go SSH server on port 22
       - Start child reaper (PID 1 duty)
       - Wait for signals (SIGTERM/SIGINT → graceful shutdown)
        │
        ▼
   Post-boot hook: WaitForReady (SSH poll)
        │
        ▼
   SSH session:
     source /etc/sandbox-env
     cd /workspace
     exec claude   (or codex, opencode, etc.)
        │
        ▼
   Agent exits → SSH session ends → VM stopped
        │
        ▼
   Diff → Review → Flush (if snapshot enabled) → Cleanup
```

## Guest Layer (`internal/guest/`)

Code that runs inside the microVM (Linux only, compiled into
`cmd/apiary-init/`):

- **`boot/`** -- Orchestrates guest startup: mount filesystems,
  configure networking, mount workspace, load env, start SSH server.
  Returns a shutdown function.
- **`mount/`** -- Mounts essential filesystems (/proc, /sys, /dev, /tmp,
  /run) and virtiofs workspace with retry logic.
- **`network/`** -- Configures loopback interface via netlink.
- **`env/`** -- Parses KEY=VALUE lines from `/etc/sandbox-env`.
- **`sshd/`** -- Embedded Go SSH server with authorized key auth, PTY
  support, command wrapping, and process exit code forwarding.
- **`reaper/`** -- Reaps orphaned child processes (required for PID 1).

## Guest Environment

Inside the VM:

| Path | Contents |
|------|----------|
| `/workspace` | Host workspace directory (virtio-fs mount) |
| `/etc/sandbox-env` | `export KEY='value'` lines for forwarded vars |
| `/home/sandbox/.ssh/authorized_keys` | Ephemeral public key for SSH access |
| `/apiary-init` | Compiled Go init binary (PID 1) |
| `/.krun_config.json` | libkrun config pointing to `/apiary-init` |

## Security Model

- **Hardware isolation**: VMs run under KVM (Linux) via libkrun. This
  provides stronger isolation than containers.
- **Ephemeral SSH keys**: Generated per session (ECDSA P-256), deleted
  on exit. Never written to persistent storage.
- **Localhost-only SSH**: Port forwards bind to `127.0.0.1` only.
- **Shell-escaped env injection**: All environment variable values are
  single-quote escaped to prevent injection.
- **No persistent state**: apiary doesn't maintain any state
  between runs. Each invocation is fully ephemeral.
- **Snapshot hash verification**: File hashes are re-verified between
  diff and flush to prevent TOCTOU (time-of-check-time-of-use) attacks.
- **Path traversal protection**: Symlinks are validated in-bounds before
  copying. `ValidateInBounds` resolves symlinks in both base and target.
- **Non-overridable security patterns**: Sensitive files (`.env*`,
  `*.pem`, `.ssh/`, credentials, etc.) are always excluded from snapshots
  and cannot be negated in `.apiaryignore`.
- **Permission stripping**: Setuid, setgid, and sticky bits are stripped
  when flushing files back to the original workspace.
- **VM stopped before review**: The VM is explicitly stopped before
  diff/review/flush, preventing the agent from modifying files during
  the review phase.

## Relationship to Propolis

apiary is a consumer of the [propolis](https://github.com/stacklok/propolis)
library. It uses propolis via a local `replace` directive in `go.mod`:

```
replace github.com/stacklok/propolis => ../propolis
```

### Propolis APIs Used

| API | Usage |
|-----|-------|
| `propolis.Run()` | Orchestrate the full OCI-to-VM pipeline |
| `WithName` | Name the VM `sandbox-<agent>` |
| `WithCPUs` / `WithMemory` | Set VM resources |
| `WithPorts` | Forward SSH (host → guest:22) |
| `WithVirtioFS` | Mount workspace directory |
| `WithRootFSHook` | Inject SSH keys, init binary, env file |
| `WithInitOverride` | Replace OCI CMD with `/apiary-init` |
| `WithPostBoot` | Wait for SSH readiness |
| `WithRunnerPath` | Locate propolis-runner binary |
| `ssh.GenerateKeyPair` | Create ephemeral SSH keys |
| `ssh.GetPublicKeyContent` | Read public key for injection |
| `ssh.NewClient` | Create SSH client for readiness check |
| `VM.Stop` | Graceful VM shutdown |
| `VM.Ports` | Discover actual SSH port |
| `VM.DataDir` | Get VM data directory |
