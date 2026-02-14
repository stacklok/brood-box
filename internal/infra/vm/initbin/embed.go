// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package initbin embeds the pre-compiled sandbox-init binary.
// The binary is built by `task build-init` and placed at
// internal/infra/vm/initbin/sandbox-init before compiling sandbox-agent.
package initbin

import _ "embed"

//go:embed sandbox-init
var Binary []byte
