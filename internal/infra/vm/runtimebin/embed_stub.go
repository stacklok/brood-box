// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build !embed_runtime

package runtimebin

var (
	runner    []byte
	libkrun   []byte
	libkrunfw []byte
)

const available = false
