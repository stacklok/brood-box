# Refactoring Plan: Split Monolithic `Run()` into Session Lifecycle

**Plan file**: `REFACTOR_PLAN.md` (untracked — do not commit)

**Goal**: Refactor `SandboxRunner` so the same application orchestrator can be driven
by both the existing CLI and a future HTTP+WebSocket server — with full interactive
terminal support from day one.

**Non-goal**: Building the HTTP server itself. This plan only restructures the app
layer so that a second composition root (`cmd/sandbox-server/`) can be added later
with zero changes to `internal/app/` or `internal/domain/`.

**After completing each task**: Update the task's status in this file from `[ ]` to
`[x]` so implementation state is preserved across sessions.

---

## Context for the Agent

Read the project's `CLAUDE.md` first — it has build commands, conventions (SPDX
headers, `slog`, error wrapping, table-driven tests, imperative commits, never
`git add -A`), and architecture rules.

### Key files you will modify

| File | Role |
|------|------|
| `internal/app/sandbox.go` | Application orchestrator — **primary target** |
| `internal/app/sandbox_test.go` | 10 tests with 8 mocks — must stay green |
| `cmd/sandbox-agent/main.go` | CLI composition root — must call new lifecycle |

### Key files you will only read (for context)

| File | Why |
|------|-----|
| `internal/domain/session/terminal.go` | `Terminal` and `IOStreams` interfaces |
| `internal/domain/session/session.go` | `TerminalSession` and `SessionOpts` |
| `internal/domain/vm/vm.go` | `VMRunner`, `VM`, `VMConfig` |
| `internal/domain/workspace/workspace.go` | `WorkspaceCloner`, `Snapshot` |
| `internal/domain/snapshot/snapshot.go` | `FileChange`, `ReviewResult`, `ReviewDecision` |
| `internal/domain/snapshot/differ.go` | `Differ` interface |
| `internal/domain/snapshot/reviewer.go` | `Reviewer` interface |
| `internal/domain/snapshot/flusher.go` | `Flusher` interface |
| `internal/domain/snapshot/matcher.go` | `Matcher` interface, `NopMatcher` |
| `internal/infra/ssh/terminal.go` | SSH session impl (also calls `MakeRaw`) |
| `internal/infra/terminal/os.go` | `OSTerminal` impl |
| `internal/infra/review/reviewer.go` | `InteractiveReviewer` impl |

### Architecture rule (enforced)

`internal/domain/` NEVER imports from `internal/infra/` or `internal/app/`.
`internal/app/` NEVER imports from `internal/infra/`.
Only `cmd/` wires infra into app via dependency injection.

---

## Current State: What's Wrong

`SandboxRunner.Run()` (`internal/app/sandbox.go:108-263`) is a single blocking
method that does everything:

```
resolve agent -> config overrides -> collect env -> create snapshot ->
start VM -> MakeRaw -> run SSH session -> restore terminal ->
stop VM -> diff -> review -> flush -> cleanup
```

### Problem 1: Monolithic lifecycle

An HTTP server needs to control each phase independently:
- `POST /sessions` -> prepare + start VM -> return session ID
- `WebSocket /sessions/{id}/terminal` -> attach interactive session
- The session ends -> auto-stop VM
- `GET /sessions/{id}/changes` -> return diff
- `POST /sessions/{id}/flush` -> flush accepted changes
- `DELETE /sessions/{id}` -> cleanup

The current `Run()` bundles all of this into one call with no way to interleave.

### Problem 2: `Terminal` is runner-scoped

`Terminal` is a field on `SandboxDeps` (line 62), meaning one terminal per runner.
For HTTP, each concurrent session needs its own terminal (backed by a different
WebSocket). It should be passed per-session, not stored on the runner.

### Problem 3: `MakeRaw()` in the wrong layer

`sandbox.go:229` calls `s.terminal.MakeRaw()` in the orchestrator. The SSH session
infra (`infra/ssh/terminal.go:79`) also calls it. The double-call is intentional
(force-restore for review prompts after SSH), but it means the app layer has
terminal-mode concerns that belong to the caller.

### Problem 4: Review is coupled to the session flow

`runReview()` is called inside `Run()` and calls `Reviewer.Review()` synchronously.
For HTTP, the diff computation and the user's accept/reject decisions happen via
separate HTTP requests. The orchestrator should expose diff and flush as separate
operations.

---

## Task List

Execute these tasks **in order**. Each task is self-contained and leaves all tests
passing. Run `task fmt && task lint && task test` after each task.

---

### Task 1: Introduce `Sandbox` session type in the app layer `[x]`

**File**: `internal/app/sandbox.go`

Create a `Sandbox` struct that holds the state for a single session lifecycle.
This is an app-layer type (not domain) because it aggregates domain objects for
orchestration purposes.

```go
// Sandbox holds the state of a running sandbox session.
// Created by Prepare, consumed by Attach/Stop/Changes/Flush/Cleanup.
type Sandbox struct {
    Agent         agent.Agent
    VM            domvm.VM
    VMConfig      domvm.VMConfig
    Snapshot      *workspace.Snapshot
    WorkspacePath string
    DiffMatcher   snapshot.Matcher
    EnvVars       map[string]string
}

// Cleanup releases resources (snapshot dir). Safe to call multiple times.
func (sb *Sandbox) Cleanup() error {
    if sb.Snapshot != nil {
        return sb.Snapshot.Cleanup()
    }
    return nil
}
```

**What to do**:
1. Add the `Sandbox` struct and its `Cleanup()` method to `sandbox.go` (after the
   existing types, before `SandboxRunner`).
2. No other changes yet. Tests should still compile and pass unchanged.

**Verification**: `task fmt && task lint && task test`

---

### Task 2: Extract `Prepare()` from `Run()` `[x]`

**File**: `internal/app/sandbox.go`

Extract steps 1-5 of `Run()` (lines 109-208) into a new method:

```go
// Prepare resolves the agent, applies config, collects env, sets up the
// workspace snapshot (if enabled), and starts the VM.
// The caller must call Cleanup() on the returned Sandbox when done.
func (s *SandboxRunner) Prepare(ctx context.Context, agentName string, opts RunOpts) (*Sandbox, error)
```

**What moves into `Prepare()`**:
- Agent resolution (current lines 109-146)
- Env collection (lines 148-156)
- Snapshot setup (lines 158-192) — but do NOT defer cleanup here; store
  the snapshot on `Sandbox` and let the caller handle cleanup via `sb.Cleanup()`
- VM start (lines 194-208) — store the returned VM on `Sandbox`
- Return the populated `Sandbox`

**Do NOT modify `Run()` yet** — you will rewrite it in Task 6. Just add `Prepare()`
as a new method alongside `Run()`. Tests don't call it yet.

**Verification**: `task fmt && task lint && task test`

---

### Task 3: Extract `Attach()` from `Run()` `[x]`

**File**: `internal/app/sandbox.go`

Extract step 6 (lines 210-236) into:

```go
// Attach runs an interactive terminal session against the sandbox VM.
// It blocks until the remote command exits or the context is cancelled.
// The terminal parameter provides I/O streams and PTY control for this session.
func (s *SandboxRunner) Attach(ctx context.Context, sb *Sandbox, terminal session.Terminal) error
```

**Key design decisions**:
- `terminal` is a **parameter**, not pulled from `s.terminal`. This is the fix for
  Problem 2 (runner-scoped terminal). Each call to `Attach()` can pass a different
  terminal implementation.
- Do NOT call `terminal.MakeRaw()` here. That is the caller's responsibility (fixes
  Problem 3). The SSH session infra already handles raw mode internally
  (`infra/ssh/terminal.go:78-108`).
- Build `SessionOpts` from `sb.VM` and the passed `terminal`.
- Call `s.sessionRunner.Run(ctx, sessionOpts)` and return its error.

**Do NOT modify `Run()` yet**. Tests don't call `Attach()` yet.

**Verification**: `task fmt && task lint && task test`

---

### Task 4: Extract `Stop()` from `Run()` `[x]`

**File**: `internal/app/sandbox.go`

Extract step 7 (lines 238-247) into:

```go
// Stop gracefully shuts down the sandbox VM.
// Uses a fresh context with timeout to ensure shutdown completes even if the
// parent context is already cancelled.
func (s *SandboxRunner) Stop(sb *Sandbox) error
```

This method:
- Creates a 10-second timeout context (like the current code)
- Calls `sb.VM.Stop(stopCtx)`
- Logs the shutdown

**Verification**: `task fmt && task lint && task test`

---

### Task 5: Extract `Changes()` and `Flush()` from `runReview()` `[x]`

**File**: `internal/app/sandbox.go`

Split the current `runReview()` (lines 265-301) into two public methods:

```go
// Changes computes the diff between the original workspace and the snapshot.
// Returns nil with no error if snapshot isolation was not active or differ is nil.
func (s *SandboxRunner) Changes(sb *Sandbox) ([]snapshot.FileChange, error)
```

This calls `s.differ.Diff(...)` with the snapshot's original/snapshot paths and
the diff matcher stored on `Sandbox`. Returns early (nil, nil) if `sb.Snapshot`
is nil or `s.differ` is nil.

```go
// Flush applies the accepted file changes from the snapshot to the original workspace.
func (s *SandboxRunner) Flush(sb *Sandbox, accepted []snapshot.FileChange) error
```

This calls `s.flusher.Flush(...)`. Returns early (nil) if `sb.Snapshot` is nil or
`s.flusher` is nil or `accepted` is empty.

**Keep `runReview()` for now** — `Run()` still uses it. You will remove it in Task 6.

**Verification**: `task fmt && task lint && task test`

---

### Task 6: Rewrite `Run()` to use lifecycle methods `[x]`

**File**: `internal/app/sandbox.go`

Rewrite `Run()` as a thin wrapper that calls the lifecycle methods in sequence.
This preserves the existing CLI behavior exactly while proving the decomposition
works.

```go
func (s *SandboxRunner) Run(ctx context.Context, agentName string, opts RunOpts) error {
    sb, err := s.Prepare(ctx, agentName, opts)
    if err != nil {
        return err
    }
    defer func() {
        s.logger.Info("cleaning up workspace snapshot")
        if cleanErr := sb.Cleanup(); cleanErr != nil {
            s.logger.Error("failed to clean up snapshot", "error", cleanErr)
        }
    }()

    restore, _ := s.terminal.MakeRaw()
    termErr := s.Attach(ctx, sb, s.terminal)
    restore()

    if stopErr := s.Stop(sb); stopErr != nil {
        s.logger.Error("failed to stop VM", "error", stopErr)
    }

    var reviewErr error
    if sb.Snapshot != nil && s.reviewer != nil {
        changes, err := s.Changes(sb)
        if err != nil {
            reviewErr = fmt.Errorf("computing diff: %w", err)
        } else if len(changes) > 0 {
            s.logger.Info("workspace changes detected", "count", len(changes))
            result, err := s.reviewer.Review(changes)
            if err != nil {
                reviewErr = fmt.Errorf("reviewing changes: %w", err)
            } else if len(result.Accepted) > 0 {
                s.logger.Info("flushing accepted changes",
                    "accepted", len(result.Accepted),
                    "rejected", len(result.Rejected),
                )
                reviewErr = s.Flush(sb, result.Accepted)
            } else {
                s.logger.Info("no changes accepted")
            }
        } else {
            s.logger.Info("no workspace changes detected")
        }
        if reviewErr != nil {
            s.logger.Error("review/flush failed", "error", reviewErr)
        }
    }

    if termErr != nil {
        return termErr
    }
    return reviewErr
}
```

After rewriting `Run()`:
- Delete the old `runReview()` private method.
- All 10 existing tests must pass **without modification**.

**Verification**: `task fmt && task lint && task test`

---

### Task 7: Add tests for lifecycle methods `[x]`

**File**: `internal/app/sandbox_test.go`

Add new test cases that call the lifecycle methods individually. These prove
the decomposition works for HTTP-style callers.

**New tests to add**:

1. **`TestSandboxRunner_Prepare_Success`** — calls `Prepare()`, verifies the
   returned `Sandbox` has the correct agent, VM, workspace path, and env vars.
   Calls `sb.Cleanup()` at the end.

2. **`TestSandboxRunner_Prepare_AgentNotFound`** — verifies `Prepare()` returns
   the right error without starting a VM.

3. **`TestSandboxRunner_Attach_CallsSessionRunner`** — calls `Prepare()`, then
   `Attach()` with a `mockTerminal`, verifies `SessionOpts` were correct.

4. **`TestSandboxRunner_Stop_StopsVM`** — calls `Prepare()`, then `Stop()`,
   verifies `mockVM.stopped` is true.

5. **`TestSandboxRunner_Changes_ReturnsDiff`** — calls `Prepare()` with snapshot
   enabled, then `Changes()`, verifies the diff is returned correctly.

6. **`TestSandboxRunner_Flush_AppliesAccepted`** — calls `Flush()` with a list
   of accepted changes, verifies the flusher was called.

7. **`TestSandboxRunner_LifecycleEndToEnd`** — full lifecycle:
   `Prepare -> Attach -> Stop -> Changes -> Flush -> Cleanup`.
   Verifies correct ordering (VM stopped before changes computed, etc).

Use the same mock patterns as the existing tests. Table-driven where appropriate.

**Verification**: `task fmt && task lint && task test`

---

### Task 8: Move `MakeRaw` out of the app layer into `cmd/` `[x]`

**Files**: `internal/app/sandbox.go`, `cmd/sandbox-agent/main.go`

**In `Run()`**: Remove the `MakeRaw`/`restore` calls. `Run()` simply calls:
```go
termErr := s.Attach(ctx, sb, s.terminal)
```

**Why this is safe**: `infra/ssh/terminal.go:79` already calls `MakeRaw()` inside
the SSH session and defers restore. The outer `MakeRaw` in the app layer existed
to force-restore before the review reads stdin. With the lifecycle split, `Attach()`
finishes -> control returns to caller -> terminal is restored by the SSH defer ->
then `Changes()`/`Review()` run. The race that motivated the double-call no longer
exists.

**In `cmd/sandbox-agent/main.go`**: Switch `run()` from calling `runner.Run()` to
driving the lifecycle directly:

```go
sb, err := runner.Prepare(ctx, agentName, opts)
if err != nil { return err }
defer func() {
    logger.Info("cleaning up workspace snapshot")
    if cleanErr := sb.Cleanup(); cleanErr != nil {
        logger.Error("failed to clean up snapshot", "error", cleanErr)
    }
}()

restore, _ := terminal.MakeRaw()
termErr := runner.Attach(ctx, sb, terminal)
restore()

if stopErr := runner.Stop(sb); stopErr != nil {
    logger.Error("failed to stop VM", "error", stopErr)
}

// Review (terminal is restored, safe to read stdin)
var reviewErr error
if reviewEnabled && sb.Snapshot != nil {
    changes, err := runner.Changes(sb)
    // ... reviewer.Review(changes) ... runner.Flush(sb, accepted) ...
}

if termErr != nil { ... }
return reviewErr
```

`Run()` remains as a convenience method — it still works but no longer does
`MakeRaw`. Tests that call `Run()` with `mockTerminal` (which no-ops `MakeRaw`)
pass without changes.

**Verification**: `task fmt && task lint && task test`

---

### Task 9: Move `Terminal` from `SandboxDeps` to `RunOpts` `[ ]`

**Files**: `internal/app/sandbox.go`, `internal/app/sandbox_test.go`, `cmd/sandbox-agent/main.go`

Now that `Attach()` takes `Terminal` as a parameter and `Run()` no longer calls
`MakeRaw`, the `Terminal` field on `SandboxDeps`/`SandboxRunner` exists only so
`Run()` can pass it to `Attach()`. Move it to `RunOpts`:

```go
type RunOpts struct {
    // ... existing fields ...
    // Terminal provides I/O streams for the session. Required.
    Terminal session.Terminal
}
```

**Changes**:
- `SandboxDeps`: remove `Terminal` field
- `SandboxRunner`: remove `terminal` field
- `NewSandboxRunner()`: remove terminal assignment
- `Run()`: use `opts.Terminal` instead of `s.terminal`
- All existing tests: move `Terminal: &mockTerminal{}` from `SandboxDeps{}` to `RunOpts{}`
- `cmd/sandbox-agent/main.go`: pass terminal in `RunOpts` if still calling `Run()`,
  or pass to `Attach()` directly if using lifecycle methods from Task 8.

**Verification**: `task fmt && task lint && task test`

---

### Task 10: Clean up and document `[ ]`

**File**: `internal/app/sandbox.go`

1. Remove any dead code (`runReview()` if not removed in Task 6).
2. Ensure all public methods (`Prepare`, `Attach`, `Stop`, `Changes`, `Flush`,
   `Run`, `Sandbox.Cleanup`) have clear doc comments.
3. Add a type-level doc comment on `SandboxRunner` explaining the two usage patterns:

```go
// SandboxRunner orchestrates the full sandbox VM lifecycle.
//
// Two usage patterns are supported:
//
// Convenience (CLI): Call Run() for sequential prepare->attach->stop->review->cleanup.
//
// Lifecycle (HTTP server, custom control): Call Prepare(), Attach(), Stop(),
// Changes(), Flush(), and Sandbox.Cleanup() individually. This allows the caller
// to control terminal attachment, async review workflows, and concurrent sessions.
```

4. Update this plan file: mark all tasks `[x]`.

**Verification**: `task fmt && task lint && task test`

---

## Where to Start

**Start with Task 1.** It is the smallest, safest change (just adding a struct)
and gets you oriented in the codebase. Then proceed sequentially. Each task adds
new public methods without breaking the existing `Run()` path, so tests stay green
throughout.

Tasks 1-6 are additive (new code alongside old). Task 6 is the pivot where `Run()`
is rewritten. Tasks 7-10 are the payoff: proving the decomposition, moving terminal
control to the right layer, and cleaning up.

---

## What This Enables (future, out of scope)

After all 10 tasks, a new `cmd/sandbox-server/main.go` can:
- Call `runner.Prepare()` on `POST /sessions`
- Connect a WebSocket-backed `Terminal` impl via `runner.Attach()`
- Return diffs via `runner.Changes()` on `GET /sessions/{id}/changes`
- Flush via `runner.Flush()` on `POST /sessions/{id}/flush`
- Call `sb.Cleanup()` on `DELETE /sessions/{id}`

No changes to `internal/app/` or `internal/domain/` will be needed.

---

## Constraints and Gotchas

- **SPDX headers** on every `.go` file:
  ```
  // SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
  // SPDX-License-Identifier: Apache-2.0
  ```
- Use `log/slog` exclusively — no `fmt.Println` or `log.Printf`.
- Wrap errors: `fmt.Errorf("context: %w", err)`.
- Table-driven tests, test files alongside code.
- Run `task fmt && task lint && task test` after every task.
- Never `git add -A` — stage specific files only.
- `go.mod` has a local replace for propolis — the `../propolis` checkout must exist
  for `go build` but tests should work without it.
- Domain purity: `internal/domain/` must never import `internal/infra/` or `internal/app/`.
- App purity: `internal/app/` must never import `internal/infra/`.
