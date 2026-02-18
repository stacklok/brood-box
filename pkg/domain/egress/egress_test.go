// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package egress

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProfileName_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		profile ProfileName
		want    bool
	}{
		{"permissive", ProfilePermissive, true},
		{"standard", ProfileStandard, true},
		{"locked", ProfileLocked, true},
		{"empty", ProfileName(""), false},
		{"unknown", ProfileName("ultra"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.profile.IsValid())
		})
	}
}

func TestValidProfiles(t *testing.T) {
	t.Parallel()

	profiles := ValidProfiles()
	assert.Equal(t, []ProfileName{ProfilePermissive, ProfileStandard, ProfileLocked}, profiles)
}

func TestResolve(t *testing.T) {
	t.Parallel()

	agentHosts := map[ProfileName][]Host{
		ProfileLocked: {
			{Name: "api.example.com", Ports: []uint16{443}},
		},
		ProfileStandard: {
			{Name: "api.example.com", Ports: []uint16{443}},
			{Name: "github.com", Ports: []uint16{443}},
		},
	}

	tests := []struct {
		name       string
		profile    ProfileName
		agentHosts map[ProfileName][]Host
		wantNil    bool
		wantHosts  int
		wantErr    string
	}{
		{
			name:       "permissive returns nil policy",
			profile:    ProfilePermissive,
			agentHosts: agentHosts,
			wantNil:    true,
		},
		{
			name:       "standard returns agent hosts",
			profile:    ProfileStandard,
			agentHosts: agentHosts,
			wantHosts:  2,
		},
		{
			name:       "locked returns agent hosts",
			profile:    ProfileLocked,
			agentHosts: agentHosts,
			wantHosts:  1,
		},
		{
			name:       "unknown profile errors",
			profile:    ProfileName("ultra"),
			agentHosts: agentHosts,
			wantErr:    "unknown egress profile",
		},
		{
			name:       "missing host list errors",
			profile:    ProfileLocked,
			agentHosts: map[ProfileName][]Host{},
			wantErr:    "no egress hosts",
		},
		{
			name:    "nil agent hosts errors",
			profile: ProfileStandard,
			wantErr: "no egress hosts",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Resolve(tt.profile, tt.agentHosts)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			if tt.wantNil {
				assert.Nil(t, got)
			} else {
				require.NotNil(t, got)
				assert.Len(t, got.AllowedHosts, tt.wantHosts)
			}
		})
	}
}

func TestMerge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		base      *Policy
		extra     []Host
		wantNil   bool
		wantHosts int
	}{
		{
			name:    "nil base returns nil",
			base:    nil,
			extra:   []Host{{Name: "extra.com"}},
			wantNil: true,
		},
		{
			name: "no extra returns base unchanged",
			base: &Policy{AllowedHosts: []Host{
				{Name: "api.example.com"},
			}},
			extra:     nil,
			wantHosts: 1,
		},
		{
			name: "appends extra hosts",
			base: &Policy{AllowedHosts: []Host{
				{Name: "api.example.com"},
			}},
			extra: []Host{
				{Name: "github.com"},
				{Name: "registry.npmjs.org"},
			},
			wantHosts: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Merge(tt.base, tt.extra)
			if tt.wantNil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Len(t, got.AllowedHosts, tt.wantHosts)
		})
	}
}

func TestMerge_DoesNotMutateBase(t *testing.T) {
	t.Parallel()

	base := &Policy{AllowedHosts: []Host{{Name: "original.com"}}}
	extra := []Host{{Name: "extra.com"}}

	merged := Merge(base, extra)

	assert.Len(t, base.AllowedHosts, 1, "base should not be mutated")
	assert.Len(t, merged.AllowedHosts, 2)
}

func TestParseHostFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		wantHost Host
		wantErr  string
	}{
		{
			name:     "hostname only",
			input:    "api.github.com",
			wantHost: Host{Name: "api.github.com"},
		},
		{
			name:     "hostname with port",
			input:    "api.github.com:443",
			wantHost: Host{Name: "api.github.com", Ports: []uint16{443}},
		},
		{
			name:     "wildcard hostname",
			input:    "*.docker.io:443",
			wantHost: Host{Name: "*.docker.io", Ports: []uint16{443}},
		},
		{
			name:     "trimmed whitespace",
			input:    "  example.com:8080  ",
			wantHost: Host{Name: "example.com", Ports: []uint16{8080}},
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: "empty host flag",
		},
		{
			name:    "empty hostname with port",
			input:   ":443",
			wantErr: "empty hostname",
		},
		{
			name:    "invalid port",
			input:   "example.com:abc",
			wantErr: "invalid port",
		},
		{
			name:    "port zero",
			input:   "example.com:0",
			wantErr: "port must be non-zero",
		},
		{
			name:    "port out of range",
			input:   "example.com:99999",
			wantErr: "invalid port",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseHostFlag(tt.input)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantHost, got)
		})
	}
}

func TestStricter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a, b ProfileName
		want ProfileName
	}{
		{"locked vs permissive", ProfileLocked, ProfilePermissive, ProfileLocked},
		{"permissive vs locked", ProfilePermissive, ProfileLocked, ProfileLocked},
		{"standard vs locked", ProfileStandard, ProfileLocked, ProfileLocked},
		{"locked vs standard", ProfileLocked, ProfileStandard, ProfileLocked},
		{"standard vs permissive", ProfileStandard, ProfilePermissive, ProfileStandard},
		{"permissive vs standard", ProfilePermissive, ProfileStandard, ProfileStandard},
		{"same profile", ProfileStandard, ProfileStandard, ProfileStandard},
		{"invalid a returns b", ProfileName("bad"), ProfileStandard, ProfileStandard},
		{"invalid b returns a", ProfileLocked, ProfileName("bad"), ProfileLocked},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, Stricter(tt.a, tt.b))
		})
	}
}
