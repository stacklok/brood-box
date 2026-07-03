// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

// AgentManifest is the standalone, shareable form of a custom (bring-your-own)
// agent definition. It carries the same fields as an AgentOverride (inlined)
// plus a top-level name, so a reusable agent can live in its own file instead
// of only inline under `agents:` in the global config.
//
// The inline embedding guarantees the manifest fields exactly track
// AgentOverride — there is no second field list to keep in sync. `bbox agents
// import` decodes a manifest file into this struct; `bbox agents export`
// marshals it back out.
//
// Example manifest:
//
//	name: aider
//	image: ghcr.io/acme/aider-bbox:latest
//	command: ["aider"]
//	env_forward: [OPENAI_API_KEY]
//	egress_profile: standard
//	egress_hosts:
//	  standard:
//	    - name: api.openai.com
//	      ports: [443]
type AgentManifest struct {
	// Name is the agent name (the key under `agents:` in the global config).
	Name string `yaml:"name"`

	// AgentOverride is inlined so its fields appear at the top level of the
	// manifest, matching the nesting an operator would write under
	// `agents.<name>:` in the config file.
	AgentOverride `yaml:",inline"`
}
