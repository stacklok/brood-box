// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package devhosts exposes the common dev-infrastructure egress hosts shared
// across built-in clients at the standard profile (registries, GitHub, etc.).
// Internal to pkg/clients/ — external consumers should not depend on it.
package devhosts

import "github.com/stacklok/brood-box/pkg/domain/egress"

// Standard returns the dev-infra hosts every built-in client appends to
// its locked profile to form the standard profile.
//
// Returns a fresh slice on every call so callers can append to it without
// mutating shared state.
//
// Remaining wildcards and why they are necessary:
//   - *.githubusercontent.com — GitHub CDN subdomains (raw., objects., avatars., etc.)
//   - *.pypi.org — warehouse, upload, and test subdomains used by pip
//   - *.docker.io — registry-1., auth., index. subdomains required for image pulls
func Standard() []egress.Host {
	return []egress.Host{
		{Name: "github.com", Ports: []uint16{443, 22}},
		{Name: "api.github.com", Ports: []uint16{443}},
		{Name: "*.githubusercontent.com", Ports: []uint16{443}},
		{Name: "registry.npmjs.org", Ports: []uint16{443}},
		{Name: "pypi.org", Ports: []uint16{443}},
		{Name: "*.pypi.org", Ports: []uint16{443}},
		{Name: "proxy.golang.org", Ports: []uint16{443}},
		{Name: "sum.golang.org", Ports: []uint16{443}},
		{Name: "*.docker.io", Ports: []uint16{443}},
		{Name: "ghcr.io", Ports: []uint16{443}},
		{Name: "sentry.io", Ports: []uint16{443}},
	}
}
