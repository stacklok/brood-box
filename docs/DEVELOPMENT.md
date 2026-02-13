# Development Guide

## Prerequisites

- Go 1.25.7+
- [Task](https://taskfile.dev/) (task runner)
- [golangci-lint](https://golangci-lint.run/)
- [goimports](https://pkg.go.dev/golang.org/x/tools/cmd/goimports)
- [propolis](https://github.com/stacklok/propolis) checked out at `../propolis`

For building the propolis-runner (needed for actual VM execution):
- Linux with KVM support
- `libkrun-devel` package installed

## Getting Started

```bash
# Clone alongside propolis
cd ~/Development/stacklok
git clone https://github.com/stacklok/propolis.git
git clone https://github.com/stacklok/sandbox-agent.git

# Install dependencies
cd sandbox-agent
task tidy

# Run the full verification pipeline
task verify
```

## Task Commands

| Command | Description |
|---------|-------------|
| `task build` | Build `bin/sandbox-agent` (pure Go, `CGO_ENABLED=0`) |
| `task build-dev` | Build sandbox-agent + `bin/propolis-runner` (requires libkrun-devel) |
| `task test` | Run tests with race detector |
| `task test-coverage` | Run tests with coverage report |
| `task lint` | Run golangci-lint |
| `task lint-fix` | Run golangci-lint with auto-fix |
| `task fmt` | Run `go fmt` + `goimports` |
| `task tidy` | Run `go mod tidy` |
| `task verify` | Full pipeline: fmt + lint + test |
| `task run -- <args>` | Build and run with arguments |
| `task clean` | Remove `bin/` and coverage files |
| `task image-base` | Build base guest image |
| `task image-claude-code` | Build claude-code guest image |
| `task image-codex` | Build codex guest image |
| `task image-opencode` | Build opencode guest image |
| `task image-all` | Build all guest images |
| `task image-push` | Push all images to GHCR |

## Project Layout

```
sandbox-agent/
├── cmd/sandbox-agent/main.go     # CLI entrypoint and dependency wiring
├── internal/
│   ├── domain/                   # Pure business logic (no I/O)
│   │   ├── agent/                # Agent type, registry interface, env forwarding
│   │   └── config/               # Config types, merge logic
│   ├── app/                      # Application orchestrator
│   │   ├── sandbox.go            # SandboxRunner (the main use case)
│   │   └── sandbox_test.go       # Tests with full mock injection
│   └── infra/                    # Infrastructure implementations
│       ├── agent/registry.go     # Built-in agent registry
│       ├── config/loader.go      # YAML config file reader
│       ├── vm/                   # Propolis VM runner + rootfs hooks
│       └── ssh/terminal.go       # Interactive PTY SSH session
├── images/                       # OCI guest image definitions
│   ├── base/Containerfile        # Wolfi base: sshd, bash, git, coreutils
│   ├── claude-code/Containerfile # Base + Claude Code binary
│   ├── codex/Containerfile       # Base + Codex binary
│   └── opencode/Containerfile    # Base + OpenCode binary
├── docs/                         # Documentation
├── Taskfile.yaml                 # Development task runner
├── CLAUDE.md                     # AI assistant instructions
└── go.mod                        # Module with local propolis replace
```

## Adding a New Built-in Agent

Edit `internal/infra/agent/registry.go` and add an entry to the
`builtinAgents()` function:

```go
"my-agent": {
    Name:          "my-agent",
    Image:         "ghcr.io/stacklok/sandbox-agent/my-agent:latest",
    Command:       []string{"my-agent"},
    EnvForward:    []string{"MY_API_KEY", "MY_AGENT_*"},
    DefaultCPUs:   2,
    DefaultMemory: 2048,
},
```

Then update the CLI help text in `cmd/sandbox-agent/main.go` and the
documentation.

## Writing Tests

### Domain Layer Tests

Domain tests are pure unit tests with no I/O. Use the injectable
`EnvProvider` interface for env forwarding tests:

```go
provider := &staticEnvProvider{vars: []string{"KEY=value"}}
result := agent.ForwardEnv([]string{"KEY"}, provider)
```

### App Layer Tests

The `SandboxRunner` is tested with mock implementations of all interfaces.
See `internal/app/sandbox_test.go` for the pattern:

```go
runner := app.NewSandboxRunner(app.SandboxDeps{
    Registry:    &mockRegistry{...},
    VMRunner:    &mockVMRunner{...},
    Terminal:    &mockTerminal{...},
    CfgLoader:   infraconfig.NewLoader(tempConfigPath),
    EnvProvider: &mockEnvProvider{...},
    Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
})
```

### Infra Layer Tests

- `infra/config/loader_test.go` tests YAML parsing with temp files
- VM and SSH packages are integration-heavy and tested via the app
  layer mocks (actual VM tests require a running libkrun environment)

## Code Conventions

### SPDX Headers

Every `.go` file must start with:

```go
// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0
```

Every `.yaml` file must start with:

```yaml
# SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
# SPDX-License-Identifier: Apache-2.0
```

### Logging

Use `log/slog` exclusively. No `fmt.Println` or `log.Printf`.

```go
s.logger.Info("starting VM", "name", cfg.Name, "cpus", cfg.CPUs)
s.logger.Error("failed to stop", "error", err)
```

### Error Handling

Wrap errors with context using `%w`:

```go
return fmt.Errorf("starting VM: %w", err)
```

### Layer Boundaries

- `domain/` must NOT import from `infra/` or `app/`
- `app/` may import from `domain/` and `infra/` (for type references)
- `infra/` may import from `domain/` (to implement interfaces)
- `cmd/` wires everything together

### Git Conventions

- Imperative mood commit messages ("Add feature" not "Added feature")
- Capitalize the first letter
- No trailing period
- Never use `git add -A` -- stage specific files only

## Verification Checklist

Before submitting changes:

```bash
task fmt          # Format code
task lint         # Check for issues
task test         # Run tests with race detector
task build        # Verify compilation
```

Or run them all at once:

```bash
task verify
```
