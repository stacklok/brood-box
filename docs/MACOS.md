# macOS Support

Brood Box supports macOS on Apple Silicon (M1+) via Hypervisor.framework, using the
same go-microvm framework that powers the Linux backend.

## Requirements

- Apple Silicon Mac (M1, M2, M3, M4)
- macOS 11 (Big Sur) or later
- [Homebrew](https://brew.sh/) libkrun and libkrunfw (for building go-microvm-runner)
- Go 1.26+ (matching `go.mod`)
- [Task](https://taskfile.dev/) runner

## Installing libkrun

```bash
brew tap slp/krun
brew install libkrun libkrunfw
```

This installs the shared libraries that go-microvm-runner links against via CGO.

## Building

### Default build (embedded runtime)

The default build embeds the go-microvm runtime into `bbox`:

```bash
task build
```

This downloads the pinned runtime artifacts via `gh` and produces a self-contained
`bin/bbox` (pure Go, no CGO) that does not depend on Homebrew `libkrun`.

Firmware (`libkrunfw`) is not embedded. It is downloaded at runtime and cached
under `~/.cache/broodbox/firmware/`, with a system fallback if the download is
unavailable.

### System build (bbox + go-microvm-runner)

```bash
task build-dev-system-darwin
```

This:
1. Builds the `bbox` binary (pure Go)
2. Builds `go-microvm-runner` from the pinned go-microvm module version (CGO, links libkrun)
3. Code-signs `go-microvm-runner` with Hypervisor.framework entitlements

The resulting binaries are in `bin/`.

## Running

```bash
bin/bbox claude-code --workspace /path/to/project
```

go-microvm auto-discovers `go-microvm-runner` next to the `bbox` binary (both in `bin/`)
when using the system build.

## Platform Differences

| Feature | Linux | macOS |
|---------|-------|-------|
| Hypervisor | KVM (libkrun) | Hypervisor.framework (libkrun) |
| Build task | `task build` | `task build` |
| libkrun install | `libkrun-devel` (system package) | `brew install libkrun` |
| Code signing | Not required | Required (entitlements.plist) |
| Library path | `LD_LIBRARY_PATH` | `DYLD_LIBRARY_PATH` |
| Workspace COW | `FICLONE` ioctl | `clonefile(2)` |
| Guest arch | Matches host | Always aarch64 |

## Troubleshooting

### Code signing errors

If you see `EXC_BAD_ACCESS` or `killed` when running go-microvm-runner, the binary
likely lacks Hypervisor.framework entitlements:

```bash
codesign --entitlements assets/entitlements.plist --force -s - bin/go-microvm-runner
```

The `task build-dev-system-darwin` command does this automatically.

### Hypervisor.framework not available

Verify your Mac supports hardware virtualization:

```bash
sysctl kern.hv_support
# kern.hv_support: 1
```

If this returns 0, your hardware does not support Hypervisor.framework.

### Library not found errors

If go-microvm-runner fails to find libkrunfw at runtime, ensure Homebrew libraries are
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
