// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"github.com/stacklok/brood-box/pkg/domain/credential"
)

// ChownFunc abstracts file ownership changes performed during rootfs
// preparation. Implementations live in infrastructure; the domain only
// exposes the type so plugin packages can be expressed without crossing
// into internal/infra/.
type ChownFunc func(path string, uid, gid int) error

// MCPInjector writes an agent's MCP server configuration into the guest
// rootfs before VM boot so the agent discovers the in-process vmcp proxy
// on first launch. Each client implements its own injector because the
// file path, serialization format, and key layout vary.
type MCPInjector interface {
	Inject(rootfsPath, gatewayIP string, port uint16, chown ChownFunc) error
}

// Plugin carries the small behavioral fragments that cannot be expressed
// declaratively in Agent. Each built-in client ships a Plugin alongside
// its Agent value; data-only custom agents registered from config have a
// nil Plugin.
//
// Methods may return nil to indicate the agent does not need that
// concern (e.g. an agent that does not consume MCP, or one with no
// host-side credential seeding). Callers must nil-check.
type Plugin interface {
	// MCPConfig returns an injector for writing this agent's MCP config
	// into the guest rootfs, or nil when MCP config injection is not
	// applicable to this agent.
	MCPConfig() MCPInjector

	// Seeder returns a credential seeder that pulls fresh host
	// credentials into the credential store before VM boot, or nil
	// when no host-side seeding is implemented for this agent.
	Seeder() credential.Seeder
}

// ClientEntry pairs a declarative Agent value with its behavioral
// Plugin. Registries store one entry per registered client.
type ClientEntry struct {
	Agent  Agent
	Plugin Plugin
}
