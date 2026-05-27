// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package clients aggregates the built-in coding-agent clients shipped
// with brood-box. Each client lives in its own subpackage under
// pkg/clients/<name>/ and is composed here so that the composition root
// (cmd/bbox/main.go) and the SDK runtime factory (pkg/runtime) can wire
// them all in a single call.
//
// SDK consumers can extend the registry with their own clients by
// constructing additional agent.ClientEntry values and passing them
// alongside Builtins().
package clients

import (
	"log/slog"

	"github.com/stacklok/brood-box/pkg/clients/claudecode"
	"github.com/stacklok/brood-box/pkg/clients/codex"
	"github.com/stacklok/brood-box/pkg/clients/gemini"
	"github.com/stacklok/brood-box/pkg/clients/hermes"
	"github.com/stacklok/brood-box/pkg/clients/opencode"
	"github.com/stacklok/brood-box/pkg/domain/agent"
)

// Builtins returns the standard set of brood-box client entries. The
// logger is threaded into clients that ship a stateful Plugin
// (currently only claude-code, for its credential seeder); pass
// slog.Default() or a discard logger when no logging is desired.
func Builtins(logger *slog.Logger) []agent.ClientEntry {
	return []agent.ClientEntry{
		claudecode.New(logger),
		codex.New(),
		gemini.New(),
		hermes.New(),
		opencode.New(),
	}
}
