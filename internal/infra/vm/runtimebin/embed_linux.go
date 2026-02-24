// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build embed_runtime && linux

package runtimebin

import _ "embed"

//go:embed propolis-runner
var runner []byte

//go:embed libkrun.so.1
var libkrun []byte

//go:embed libkrunfw.so.5
var libkrunfw []byte

const available = true
