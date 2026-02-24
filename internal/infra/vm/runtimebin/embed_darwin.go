// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build embed_runtime && darwin

package runtimebin

import _ "embed"

//go:embed propolis-runner
var runner []byte

//go:embed libkrun.dylib
var libkrun []byte

//go:embed libkrunfw.dylib
var libkrunfw []byte

const available = true
