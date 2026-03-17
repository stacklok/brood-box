# Firmware Handling Plan (No Embedding)

## Goals

- Avoid embedding `libkrunfw` to prevent GPL contamination risk.
- Prefer runtime download with caching.
- Fall back to system-provided firmware when download is unavailable.
- Keep running VMs stable across upgrades by pinning firmware per VM.

## Plan

1) **Versioned firmware cache directory**
- Add `firmwareCacheDir()` (XDG-aware): `~/.cache/broodbox/firmware`.
- Store firmware in versioned subdirs:
  `~/.cache/broodbox/firmware/<go-microvm-version>/<os>-<arch>/`.
- Write `firmware.json` manifest in that directory with:
  `version`, `os`, `arch`, `source` (`download` or `system`), `url` (if download), `timestamp`.

2) **Per-VM firmware reference (JSON)**
- On VM creation, write `<vm-data-dir>/firmware.ref.json` with:
  `version`, `source`, `path`, `timestamp`, and `url` (if download).
- This keeps running VMs on the same firmware across upgrades.

3) **Runtime download (preferred)**
- Download firmware via raw HTTPS:
  `https://github.com/stacklok/go-microvm/releases/download/<version>/go-microvm-firmware-<os>-<arch>.tar.gz`.
- Use Go `net/http` with timeouts.
- Extract tar.gz into the versioned firmware cache directory.
- Validate that `libkrunfw.*` exists.
- Use a lockfile in the cache root to avoid concurrent downloads.
- If download fails (404/private repo/network), fall back to system lookup.

4) **System fallback detection**
- macOS: `/opt/homebrew/lib`, `/usr/local/lib`.
- Linux: `/usr/lib`, `/usr/local/lib`, `/lib`, `/lib64`, `/usr/lib64`.
- If found, record `source=system` in the manifest and per-VM ref.

5) **CLI flag + config**
- Add `--no-firmware-download` to skip downloads (system-only mode).
- Default behavior: download preferred, fallback to system.
- If neither works, return a clear error with remediation steps.

6) **Wire firmware source**
- Resolve firmware directory first, then pass `WithFirmwareSource(extract.Dir(dir))`.
- Keep embedded runtime for `go-microvm-runner` + `libkrun` intact.

7) **Remove firmware embedding**
- Stop embedding `libkrunfw` bytes in `embed_runtime` builds.
- Runtime firmware resolution uses download/system only.

8) **Docs update**
- Explain firmware is not embedded (GPL concern).
- Firmware is downloaded at runtime and cached by version.
- Document `--no-firmware-download`.
- Document system fallback locations.
