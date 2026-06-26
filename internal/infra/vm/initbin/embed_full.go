// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build bbox_full

package initbin

import _ "embed"

// Binary is the pre-compiled bbox-init guest init binary, embedded into the
// bbox CLI. Built by `task build-init`; requires the `bbox_full` build tag.
//
//go:embed bbox-init
var Binary []byte
