// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build embed_runtime && linux

package runtimebin

import (
	_ "embed"

	"github.com/stacklok/propolis/extract"
)

//go:embed propolis-runner
var runner []byte

//go:embed libkrun.so.1
var libkrun []byte

const available = true

func extraLibs() []extract.File {
	return nil
}
