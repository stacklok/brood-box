// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build !bbox_full

package initbin

// Binary is empty in builds without the `bbox_full` tag so that brood-box can
// be imported as a Go module without the .gitignore'd bbox-init file present.
// Library consumers do not boot VMs directly; the bbox CLI is built with
// `bbox_full` to embed the real binary.
var Binary []byte
