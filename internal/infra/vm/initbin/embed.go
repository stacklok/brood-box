// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package initbin embeds the pre-compiled bbox-init binary.
//
// The binary is built by `task build-init` and placed at
// internal/infra/vm/initbin/bbox-init before compiling bbox. Because that
// file is .gitignore'd, it is not served by the Go module proxy. Embedding it
// unconditionally would make the whole module uncompilable for downstream
// consumers that import brood-box as a library (see issue #110).
//
// To avoid that, the embed is guarded by the `bbox_full` build tag:
//
//   - embed_full.go (//go:build bbox_full): the real //go:embed directive,
//     used by the bbox CLI build (`task build` / `task install`).
//   - embed_stub.go (//go:build !bbox_full): an empty Binary, used by every
//     other build, including `go test ./...` and library consumers that never
//     boot a VM directly.
//
// The vm rootfs hook validates that Binary is non-empty before use, so a stub
// build that does attempt to run a VM fails fast with a clear error rather
// than booting with a zero-byte init.
package initbin
