// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build embed_runtime && darwin

package runtimebin

import _ "embed"

//go:embed propolis-runner
var runner []byte

//go:embed libkrun.1.dylib
var libkrun []byte

const available = true
