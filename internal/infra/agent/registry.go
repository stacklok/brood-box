// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package agent provides an in-memory implementation of the
// agent.Registry interface for the brood-box client plugin layer.
//
// The registry is empty by default. Composition roots (cmd/bbox/main.go
// or SDK consumers via pkg/runtime) populate it by passing the desired
// ClientEntry values — typically clients.Builtins() plus any extras —
// to NewRegistry.
package agent

import (
	"fmt"
	"sort"

	domainagent "github.com/stacklok/brood-box/pkg/domain/agent"
)

// Registry implements agent.Registry with an in-memory map of client entries.
type Registry struct {
	entries map[string]domainagent.ClientEntry
}

// NewRegistry creates a new registry populated with the given client
// entries. Duplicate names are resolved in last-wins order so callers
// can append override entries after Builtins().
//
// Unlike the previous version, this constructor does NOT auto-load any
// built-in clients — callers must pass them explicitly (e.g. via
// clients.Builtins()).
func NewRegistry(entries ...domainagent.ClientEntry) *Registry {
	r := &Registry{entries: make(map[string]domainagent.ClientEntry, len(entries))}
	for _, e := range entries {
		r.entries[e.Agent.Name] = e
	}
	return r
}

// Add registers or overrides a data-only agent in the registry. Used by
// the CLI to register custom agents loaded from config; the resulting
// entry has a nil Plugin.
func (r *Registry) Add(a domainagent.Agent) error {
	if err := domainagent.ValidateName(a.Name); err != nil {
		return fmt.Errorf("cannot register agent: %w", err)
	}
	r.entries[a.Name] = domainagent.ClientEntry{Agent: a, Plugin: nil}
	return nil
}

// AddEntry registers a full ClientEntry (Agent + Plugin) in the
// registry. Intended for SDK consumers extending the registry with
// their own client packages at runtime.
func (r *Registry) AddEntry(e domainagent.ClientEntry) error {
	if err := domainagent.ValidateName(e.Agent.Name); err != nil {
		return fmt.Errorf("cannot register agent: %w", err)
	}
	r.entries[e.Agent.Name] = e
	return nil
}

// Get returns the client entry with the given name, or ErrNotFound.
func (r *Registry) Get(name string) (domainagent.ClientEntry, error) {
	e, ok := r.entries[name]
	if !ok {
		return domainagent.ClientEntry{}, &domainagent.ErrNotFound{Name: name}
	}
	return e, nil
}

// List returns all registered client entries sorted by Agent.Name.
func (r *Registry) List() []domainagent.ClientEntry {
	result := make([]domainagent.ClientEntry, 0, len(r.entries))
	for _, e := range r.entries {
		result = append(result, e)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Agent.Name < result[j].Agent.Name
	})
	return result
}
