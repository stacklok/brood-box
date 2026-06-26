// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainagent "github.com/stacklok/brood-box/pkg/domain/agent"
	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
	"github.com/stacklok/brood-box/pkg/domain/egress"
	"github.com/stacklok/brood-box/pkg/domain/settings"
)

func TestFieldSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                              string
		cli, workspace, global, wantLabel string
		setCLI, setWS, setGlobal          bool
		want                              string
	}{
		{name: "cli wins", setCLI: true, setWS: true, setGlobal: true, want: "CLI"},
		{name: "workspace beats global", setWS: true, setGlobal: true, want: "workspace"},
		{name: "global", setGlobal: true, want: "global"},
		{name: "built-in fallback", want: "built-in"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, fieldSource(tt.setCLI, tt.setWS, tt.setGlobal))
		})
	}
}

func TestRunDoctor(t *testing.T) {
	t.Parallel()

	validOverride := domainconfig.AgentOverride{
		Image:         "ghcr.io/acme/agent:latest",
		Command:       []string{"run"},
		EgressProfile: string(egress.ProfilePermissive),
		EnvRequired:   []string{"ACME_API_KEY"},
	}

	t.Run("clean config with present env passes", func(t *testing.T) {
		t.Parallel()
		lookup := func(string) (string, bool) { return "secret", true }
		results, ok := runDoctor("agent", validOverride, false, lookup, imageRefValidator, false, false)
		assert.True(t, ok)
		require.NotEmpty(t, results)
	})

	t.Run("missing required env fails with clear message", func(t *testing.T) {
		t.Parallel()
		lookup := func(string) (string, bool) { return "", false }
		results, ok := runDoctor("agent", validOverride, false, lookup, imageRefValidator, false, false)
		assert.False(t, ok)
		assert.True(t, containsMessage(results, "required env ACME_API_KEY is missing"),
			"missing required env must report 'is missing', not 'present'")
		assert.False(t, containsMessage(results, "required env ACME_API_KEY present"))
	})

	t.Run("present required env reports present", func(t *testing.T) {
		t.Parallel()
		lookup := func(string) (string, bool) { return "v", true }
		results, _ := runDoctor("agent", validOverride, false, lookup, imageRefValidator, false, false)
		assert.True(t, containsMessage(results, "required env ACME_API_KEY present"))
	})

	t.Run("invalid image ref fails", func(t *testing.T) {
		t.Parallel()
		bad := validOverride
		bad.Image = "::::not a ref::::"
		lookup := func(string) (string, bool) { return "v", true }
		_, ok := runDoctor("agent", bad, false, lookup, imageRefValidator, false, false)
		assert.False(t, ok)
	})

	t.Run("local-added agent flagged", func(t *testing.T) {
		t.Parallel()
		lookup := func(string) (string, bool) { return "v", true }
		results, ok := runDoctor("agent", validOverride, false, lookup, imageRefValidator, true, false)
		assert.False(t, ok)
		assert.True(t, containsMessage(results, "attempted to add this agent"))
	})

	t.Run("local-added credentials flagged", func(t *testing.T) {
		t.Parallel()
		lookup := func(string) (string, bool) { return "v", true }
		results, ok := runDoctor("agent", validOverride, false, lookup, imageRefValidator, false, true)
		assert.False(t, ok)
		assert.True(t, containsMessage(results, "credential paths"))
	})

	t.Run("built-in agent passes despite empty override", func(t *testing.T) {
		t.Parallel()
		// A built-in agent carries its image/command in the registry entry,
		// not in the override. ValidateCustomAgent must be skipped so the
		// empty Image does not trigger a spurious failure.
		lookup := func(string) (string, bool) { return "", false }
		results, ok := runDoctor("claude-code", domainconfig.AgentOverride{}, true, lookup, imageRefValidator, false, false)
		assert.True(t, ok, "built-in agent with empty override must not fail validation")
		assert.False(t, containsMessage(results, "image is required"))
		assert.True(t, containsMessage(results, "built-in agent"))
	})

	t.Run("built-in agent still flags missing required env", func(t *testing.T) {
		t.Parallel()
		override := domainconfig.AgentOverride{EnvRequired: []string{"ACME_API_KEY"}}
		lookup := func(string) (string, bool) { return "", false }
		results, ok := runDoctor("claude-code", override, true, lookup, imageRefValidator, false, false)
		assert.False(t, ok)
		assert.True(t, containsMessage(results, "required env ACME_API_KEY is missing"))
	})
}

// TestRenderInspect_NeverLeaksEnvValues asserts that inspect output never
// contains an environment variable's VALUE — only names/patterns.
func TestRenderInspect_NeverLeaksEnvValues(t *testing.T) {
	const secret = "super-secret-token-value"
	t.Setenv("ACME_API_KEY", secret)

	ag := domainagent.Agent{
		Name:                 "acme",
		Image:                "ghcr.io/acme/agent:latest",
		Command:              []string{"run"},
		EnvForward:           []string{"ACME_API_KEY", "ACME_*"},
		DefaultEnv:           map[string]string{"ACME_MODE": secret},
		DefaultEgressProfile: egress.ProfileStandard,
		EgressHosts: map[egress.ProfileName][]egress.Host{
			egress.ProfileStandard: {{Name: "api.acme.dev"}},
		},
		SettingsManifest: &settings.Manifest{
			Entries: []settings.Entry{
				{Category: "settings", HostPath: ".acme/s.json", GuestPath: ".acme/s.json", Kind: settings.KindMergeFile, Format: "json", Optional: true},
			},
		},
	}

	resolved := &resolvedRegistry{
		merged: &domainconfig.Config{
			Agents: map[string]domainconfig.AgentOverride{
				"acme": {
					Image:       "ghcr.io/acme/agent:latest",
					Description: "ACME agent",
					EnvRequired: []string{"ACME_API_KEY"},
					DefaultEnv:  map[string]string{"ACME_MODE": secret},
				},
			},
		},
		global: &domainconfig.Config{
			Agents: map[string]domainconfig.AgentOverride{
				"acme": {Image: "ghcr.io/acme/agent:latest"},
			},
		},
		local: &domainconfig.Config{},
	}

	var buf bytes.Buffer
	renderInspect(&buf, ag, resolved, "acme")
	out := buf.String()

	assert.NotContains(t, out, secret, "inspect output must never contain env values")
	// Sanity: names/patterns are present.
	assert.Contains(t, out, "ACME_API_KEY")
	assert.Contains(t, out, "ACME_MODE")
	assert.Contains(t, out, "present") // required env present indicator
}

func TestDidLocalAddAgent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		local  *domainconfig.Config
		global *domainconfig.Config
		agent  string
		want   bool
	}{
		{
			name:   "local-only agent flagged",
			local:  &domainconfig.Config{Agents: map[string]domainconfig.AgentOverride{"sneaky": {Image: "x"}}},
			global: &domainconfig.Config{},
			agent:  "sneaky",
			want:   true,
		},
		{
			name:   "agent present in global not flagged",
			local:  &domainconfig.Config{Agents: map[string]domainconfig.AgentOverride{"known": {Image: "x"}}},
			global: &domainconfig.Config{Agents: map[string]domainconfig.AgentOverride{"known": {Image: "g"}}},
			agent:  "known",
			want:   false,
		},
		{
			name:   "built-in name in local not flagged",
			local:  &domainconfig.Config{Agents: map[string]domainconfig.AgentOverride{"claude-code": {Image: "x"}}},
			global: &domainconfig.Config{},
			agent:  "claude-code",
			want:   false,
		},
		{
			name:   "nil local returns false",
			local:  nil,
			global: &domainconfig.Config{},
			agent:  "anything",
			want:   false,
		},
		{
			name:   "agent not in local returns false",
			local:  &domainconfig.Config{Agents: map[string]domainconfig.AgentOverride{"other": {Image: "x"}}},
			global: &domainconfig.Config{},
			agent:  "missing",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resolved := &resolvedRegistry{global: tt.global, local: tt.local}
			assert.Equal(t, tt.want, didLocalAddAgent(resolved, tt.agent))
		})
	}
}

func TestDidLocalAddCredentials(t *testing.T) {
	t.Parallel()

	creds := func(paths ...string) *domainconfig.AgentCredentialsConfig {
		return &domainconfig.AgentCredentialsConfig{Persist: paths}
	}

	tests := []struct {
		name  string
		local *domainconfig.Config
		agent string
		want  bool
	}{
		{
			name:  "persist set flagged",
			local: &domainconfig.Config{Agents: map[string]domainconfig.AgentOverride{"a": {Credentials: creds(".x/creds")}}},
			agent: "a",
			want:  true,
		},
		{
			name:  "nil credentials not flagged",
			local: &domainconfig.Config{Agents: map[string]domainconfig.AgentOverride{"a": {Image: "x"}}},
			agent: "a",
			want:  false,
		},
		{
			name:  "empty persist not flagged",
			local: &domainconfig.Config{Agents: map[string]domainconfig.AgentOverride{"a": {Credentials: creds()}}},
			agent: "a",
			want:  false,
		},
		{
			name:  "nil local returns false",
			local: nil,
			agent: "a",
			want:  false,
		},
		{
			name:  "agent not in local returns false",
			local: &domainconfig.Config{Agents: map[string]domainconfig.AgentOverride{"other": {Credentials: creds(".x")}}},
			agent: "a",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resolved := &resolvedRegistry{local: tt.local}
			assert.Equal(t, tt.want, didLocalAddCredentials(resolved, tt.agent))
		})
	}
}

func TestFieldSourceImage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		global    *domainconfig.Config
		local     *domainconfig.Config
		isBuiltin bool
		agent     string
		want      string
	}{
		{
			name:   "image set only in global",
			global: &domainconfig.Config{Agents: map[string]domainconfig.AgentOverride{"a": {Image: "g"}}},
			agent:  "a",
			want:   "global",
		},
		{
			name:   "image set in workspace",
			global: &domainconfig.Config{},
			local:  &domainconfig.Config{Agents: map[string]domainconfig.AgentOverride{"a": {Image: "w"}}},
			agent:  "a",
			want:   "workspace",
		},
		{
			name:      "unset plus builtin falls back to built-in",
			global:    &domainconfig.Config{},
			isBuiltin: true,
			agent:     "claude-code",
			want:      "built-in",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resolved := &resolvedRegistry{global: tt.global, local: tt.local}
			assert.Equal(t, tt.want, fieldSourceImage(tt.agent, resolved, tt.isBuiltin))
		})
	}
}

// TestRunDoctor_MisconfiguredCustomAgent confirms doctor surfaces a custom
// agent's ValidateCustomAgent failure (bad image + unsafe credential path) as a
// FAIL, rather than the "unknown agent" hard-gate it would hit if it required
// the agent to be registered first.
func TestRunDoctor_MisconfiguredCustomAgent(t *testing.T) {
	t.Parallel()

	override := domainconfig.AgentOverride{
		Image:         "::::bad ref::::",
		Command:       []string{"run"},
		EgressProfile: "permissive",
		Credentials:   &domainconfig.AgentCredentialsConfig{Persist: []string{"../escape.json"}},
	}
	lookup := func(string) (string, bool) { return "", false }
	results, ok := runDoctor("badagent", override, false, lookup, imageRefValidator, false, false)
	assert.False(t, ok, "a misconfigured custom agent must FAIL doctor")
	assert.True(t, containsMessage(results, "config validation"),
		"doctor must surface the ValidateCustomAgent failure, got: %+v", results)
}

func containsMessage(results []checkResult, substr string) bool {
	for _, r := range results {
		if bytes.Contains([]byte(r.message), []byte(substr)) {
			return true
		}
	}
	return false
}
