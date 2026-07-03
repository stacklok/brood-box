// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/stacklok/brood-box/internal/infra/configfile"
	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
)

// LoadManifest reads and decodes a standalone agent manifest file from path.
// It applies the same size cap and strict unknown-field checking as the global
// config loader so a malformed or oversized manifest fails loudly rather than
// being silently dropped. A symlinked path is accepted: import paths are
// operator-supplied (like --config), not attacker-controllable workspace files.
func LoadManifest(path string) (domainconfig.AgentManifest, error) {
	data, err := configfile.ReadFile(path, configfile.ReadOptions{})
	if err != nil {
		return domainconfig.AgentManifest{}, fmt.Errorf("reading manifest %s: %w", path, err)
	}

	var m domainconfig.AgentManifest
	if err := configfile.DecodeStrict(data, &m); err != nil {
		return domainconfig.AgentManifest{}, fmt.Errorf("parsing manifest %s: %w", path, err)
	}
	return m, nil
}

// MarshalManifest encodes a manifest to YAML suitable for `bbox agents export`.
// The output is a single document with the name at the top level followed by
// the agent override fields, normalized via yaml.v3 (omitempty honored). Env
// variable VALUES are the caller's responsibility: export constructs the
// manifest with DefaultEnv stripped so no host values ever reach the file.
func MarshalManifest(m domainconfig.AgentManifest) ([]byte, error) {
	data, err := yaml.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshalling manifest: %w", err)
	}
	return data, nil
}
