# macOS Support

Apiary supports macOS on Apple Silicon (M1+) via Hypervisor.framework, using the
same propolis framework that powers the Linux backend.

## Requirements

- Apple Silicon Mac (M1, M2, M3, M4)
- macOS 11 (Big Sur) or later
- [Homebrew](https://brew.sh/) libkrun and libkrunfw (for building propolis-runner)
- Go 1.25+ (matching `go.mod`)
- [Task](https://taskfile.dev/) runner

## Installing libkrun

```bash
brew tap slp/krun
brew install libkrun libkrunfw
```

This installs the shared libraries that propolis-runner links against via CGO.

## Building

### Pure Go binary (apiary only)

The `apiary` binary itself is pure Go (`CGO_ENABLED=0`) and compiles on any platform:

```bash
task build
```

This works identically on Linux and macOS.

### Full development build (apiary + propolis-runner)

```bash
task build-dev-darwin
```

This:
1. Builds the `apiary` binary (pure Go)
2. Builds `propolis-runner` from the pinned propolis module version (CGO, links libkrun)
3. Code-signs `propolis-runner` with Hypervisor.framework entitlements

The resulting binaries are in `bin/`.

## Running

```bash
bin/apiary claude-code --workspace /path/to/project
```

Propolis auto-discovers `propolis-runner` next to the `apiary` binary (both in `bin/`).

## Platform Differences

| Feature | Linux | macOS |
|---------|-------|-------|
| Hypervisor | KVM (libkrun) | Hypervisor.framework (libkrun) |
| Build task | `task build-dev` | `task build-dev-darwin` |
| libkrun install | `libkrun-devel` (system package) | `brew install libkrun` |
| Code signing | Not required | Required (entitlements.plist) |
| Library path | `LD_LIBRARY_PATH` | `DYLD_LIBRARY_PATH` |
| Workspace COW | `FICLONE` ioctl | `clonefile(2)` |
| Guest arch | Matches host | Always aarch64 |

## Troubleshooting

### Code signing errors

If you see `EXC_BAD_ACCESS` or `killed` when running propolis-runner, the binary
likely lacks Hypervisor.framework entitlements:

```bash
codesign --entitlements assets/entitlements.plist --force -s - bin/propolis-runner
```

The `task build-dev-darwin` command does this automatically.

### Hypervisor.framework not available

Verify your Mac supports hardware virtualization:

```bash
sysctl kern.hv_support
# kern.hv_support: 1
```

If this returns 0, your hardware does not support Hypervisor.framework.

### Library not found errors

If propolis-runner fails to find libkrun at runtime, ensure Homebrew libraries are
on the dynamic linker path:

```bash
export DYLD_LIBRARY_PATH=/opt/homebrew/lib:$DYLD_LIBRARY_PATH
```

Alternatively, pass a `LibDir` when constructing the runner via the SDK:

```go
deps := runtime.NewDefaultSandboxDeps(runtime.DefaultSandboxDepsOpts{
    LibDir: "/opt/homebrew/lib",
})
```

### "operation not permitted" from Hypervisor.framework

macOS may block Hypervisor access for unsigned or ad-hoc signed binaries in
certain security contexts. Ensure:

1. The binary is signed with the entitlements plist (`task build-dev-darwin` handles this)
2. Your terminal app has Full Disk Access if running from a sandboxed environment
3. SIP (System Integrity Protection) is enabled (required for Hypervisor.framework)
