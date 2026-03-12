// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build !embed_runtime

package runtimebin

import "github.com/stacklok/propolis/extract"

var (
	runner  []byte
	libkrun []byte
)

const available = false

func extraLibs() []extract.File {
	return nil
}
