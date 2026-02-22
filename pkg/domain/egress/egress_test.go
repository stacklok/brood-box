// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package egress

import (
	"strings"
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
		// --- Valid inputs ---
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
			name:     "wildcard hostname with port",
			input:    "*.docker.io:443",
			wantHost: Host{Name: "*.docker.io", Ports: []uint16{443}},
		},
		{
			name:     "wildcard hostname no port",
			input:    "*.docker.io",
			wantHost: Host{Name: "*.docker.io"},
		},
		{
			name:     "trimmed whitespace",
			input:    "  example.com:8080  ",
			wantHost: Host{Name: "example.com", Ports: []uint16{8080}},
		},
		{
			name:     "underscore label (SRV/DMARC)",
			input:    "_dmarc.example.com",
			wantHost: Host{Name: "_dmarc.example.com"},
		},
		{
			name:     "hyphenated label",
			input:    "registry-1.docker.io",
			wantHost: Host{Name: "registry-1.docker.io"},
		},
		{
			name:     "mixed case canonicalized to lowercase",
			input:    "API.GitHub.COM:443",
			wantHost: Host{Name: "api.github.com", Ports: []uint16{443}},
		},
		{
			name:     "single-label hostname",
			input:    "localhost",
			wantHost: Host{Name: "localhost"},
		},
		{
			name:     "label exactly 63 chars",
			input:    strings.Repeat("a", 63) + ".com",
			wantHost: Host{Name: strings.Repeat("a", 63) + ".com"},
		},
		{
			name:     "hostname exactly 253 chars",
			input:    strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 57) + ".com",
			wantHost: Host{Name: strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 57) + ".com"},
		},
		// --- Empty / malformed inputs ---
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
			name:    "trailing colon no port",
			input:   "example.com:",
			wantErr: "missing port after ':'",
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
		// --- IP address rejection ---
		{
			name:    "IPv4 address",
			input:   "1.2.3.4",
			wantErr: "IP address",
		},
		{
			name:    "IPv4 address with port",
			input:   "1.2.3.4:443",
			wantErr: "IP address",
		},
		{
			name:    "IPv4 loopback",
			input:   "127.0.0.1",
			wantErr: "IP address",
		},
		{
			name:    "IPv4 all-zeros",
			input:   "0.0.0.0",
			wantErr: "IP address",
		},
		{
			name:    "bracketed IPv6",
			input:   "[::1]",
			wantErr: "IP address",
		},
		{
			name:    "bare IPv6 loopback",
			input:   "::1",
			wantErr: "IP address",
		},
		{
			name:    "bare IPv6 link-local",
			input:   "fe80::1",
			wantErr: "IP address",
		},
		{
			name:    "IPv4-mapped IPv6",
			input:   "::ffff:1.2.3.4",
			wantErr: "IP address",
		},
		// --- Wildcard rules ---
		{
			name:    "bare wildcard",
			input:   "*",
			wantErr: "bare wildcard",
		},
		{
			name:    "mid-label wildcard",
			input:   "a*.com",
			wantErr: "wildcard must be the entire leftmost label",
		},
		{
			name:    "wildcard not leftmost",
			input:   "foo.*.com",
			wantErr: "wildcard must be the entire leftmost label",
		},
		// --- DNS format rules ---
		{
			name:    "trailing dot",
			input:   "example.com.",
			wantErr: "trailing dot",
		},
		{
			name:    "label starts with hyphen",
			input:   "-start.com",
			wantErr: "must not start with a hyphen",
		},
		{
			name:    "label ends with hyphen",
			input:   "end-.com",
			wantErr: "must not end with a hyphen",
		},
		{
			name:    "consecutive dots (empty label)",
			input:   "foo..bar",
			wantErr: "empty label",
		},
		{
			name:    "label exceeds 63 chars",
			input:   strings.Repeat("a", 64) + ".com",
			wantErr: "exceeds 63 characters",
		},
		{
			name:    "hostname exceeds 253 chars",
			input:   strings.Repeat("a", 64) + "." + strings.Repeat("b", 64) + "." + strings.Repeat("c", 64) + "." + strings.Repeat("d", 63),
			wantErr: "exceeds 253 characters",
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

func TestValidateHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		host     Host
		wantName string // expected Name after canonicalization (empty = same as input)
		wantErr  string
	}{
		// Valid hostnames that may come from config files.
		{
			name: "valid hostname",
			host: Host{Name: "api.github.com"},
		},
		{
			name: "valid wildcard",
			host: Host{Name: "*.docker.io"},
		},
		{
			name: "underscore label",
			host: Host{Name: "_dmarc.example.com"},
		},
		{
			name: "single-label hostname",
			host: Host{Name: "localhost"},
		},
		{
			name:     "uppercase canonicalized to lowercase",
			host:     Host{Name: "API.GitHub.COM"},
			wantName: "api.github.com",
		},
		{
			name:     "mixed case wildcard canonicalized",
			host:     Host{Name: "*.Docker.IO"},
			wantName: "*.docker.io",
		},
		// Invalid hostnames that config files might contain.
		{
			name:    "IPv4 address rejected",
			host:    Host{Name: "10.0.0.1"},
			wantErr: "IP address",
		},
		{
			name:    "IPv4-mapped IPv6 rejected",
			host:    Host{Name: "::ffff:127.0.0.1"},
			wantErr: "IP address",
		},
		{
			name:    "bare wildcard rejected",
			host:    Host{Name: "*"},
			wantErr: "bare wildcard",
		},
		{
			name:    "trailing dot rejected",
			host:    Host{Name: "example.com."},
			wantErr: "trailing dot",
		},
		{
			name:    "empty label rejected",
			host:    Host{Name: "foo..bar"},
			wantErr: "empty label",
		},
		{
			name:    "label starts with hyphen rejected",
			host:    Host{Name: "-bad.example.com"},
			wantErr: "must not start with a hyphen",
		},
		{
			name:    "wildcard not leftmost rejected",
			host:    Host{Name: "foo.*.com"},
			wantErr: "wildcard must be the entire leftmost label",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := tt.host // copy so parallel tests don't share
			err := ValidateHost(&h)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			if tt.wantName != "" {
				assert.Equal(t, tt.wantName, h.Name)
			}
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
