// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build embed_runtime && darwin

package runtimebin

import (
	_ "embed"

	"github.com/stacklok/propolis/extract"
)

//go:embed propolis-runner
var runner []byte

//go:embed libkrun.1.dylib
var libkrun []byte

//go:embed libepoxy.0.dylib
var libepoxy []byte

//go:embed libvirglrenderer.1.dylib
var libvirglrenderer []byte

//go:embed libMoltenVK.dylib
var libMoltenVK []byte

const available = true

func extraLibs() []extract.File {
	return []extract.File{
		{Name: "libepoxy.0.dylib", Content: libepoxy, Mode: 0o755},
		{Name: "libvirglrenderer.1.dylib", Content: libvirglrenderer, Mode: 0o755},
		{Name: "libMoltenVK.dylib", Content: libMoltenVK, Mode: 0o755},
	}
}
