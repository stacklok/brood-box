// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package egress defines domain types for DNS-aware egress firewall policies.
// All types are pure data with no I/O dependencies.
package egress

import (
	"fmt"
	"strconv"
	"strings"
)

// Policy describes the set of hosts a VM is allowed to reach.
// A nil *Policy means permissive (no restrictions).
type Policy struct {
	AllowedHosts []Host
}

// Host defines a single hostname allowed for egress traffic.
type Host struct {
	Name     string   // "api.github.com" or "*.docker.io"
	Ports    []uint16 // empty = all ports
	Protocol uint8    // 0 = TCP+UDP, 6 = TCP, 17 = UDP
}

// ProfileName is a named egress restriction level.
type ProfileName string

const (
	// ProfilePermissive allows all outbound traffic (no restrictions).
	ProfilePermissive ProfileName = "permissive"

	// ProfileStandard allows the agent's LLM provider plus common dev infra.
	ProfileStandard ProfileName = "standard"

	// ProfileLocked allows only the agent's LLM provider.
	ProfileLocked ProfileName = "locked"
)

// profileStrictness maps each profile to a numeric strictness level.
// Higher values are stricter.
var profileStrictness = map[ProfileName]int{
	ProfilePermissive: 0,
	ProfileStandard:   1,
	ProfileLocked:     2,
}

// IsValid returns true if the profile name is a recognised value.
func (p ProfileName) IsValid() bool {
	_, ok := profileStrictness[p]
	return ok
}

// ValidProfiles returns all recognised profile names in order of increasing strictness.
func ValidProfiles() []ProfileName {
	return []ProfileName{ProfilePermissive, ProfileStandard, ProfileLocked}
}

// Resolve returns the egress policy for the given profile and agent host map.
// It returns nil for ProfilePermissive (no restrictions).
// It returns an error if the profile is unrecognised or the agent has no hosts
// for the requested profile.
func Resolve(profile ProfileName, agentHosts map[ProfileName][]Host) (*Policy, error) {
	if !profile.IsValid() {
		return nil, fmt.Errorf("unknown egress profile: %q", profile)
	}

	if profile == ProfilePermissive {
		return nil, nil
	}

	hosts, ok := agentHosts[profile]
	if !ok || len(hosts) == 0 {
		return nil, fmt.Errorf("agent has no egress hosts for profile %q", profile)
	}

	return &Policy{AllowedHosts: hosts}, nil
}

// Merge returns a new policy with extra hosts appended.
// If base is nil (permissive), it returns nil — extra hosts are irrelevant
// when there are no restrictions.
func Merge(base *Policy, extraHosts []Host) *Policy {
	if base == nil {
		return nil
	}
	if len(extraHosts) == 0 {
		return base
	}

	merged := make([]Host, len(base.AllowedHosts)+len(extraHosts))
	copy(merged, base.AllowedHosts)
	copy(merged[len(base.AllowedHosts):], extraHosts)

	return &Policy{AllowedHosts: merged}
}

// ParseHostFlag parses a CLI host flag value in the format "hostname[:port]".
// The port is optional; when omitted, all ports are allowed (Ports is empty).
// Protocol defaults to 0 (TCP+UDP).
func ParseHostFlag(s string) (Host, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Host{}, fmt.Errorf("empty host flag")
	}

	host, portStr, hasPort := strings.Cut(s, ":")
	if host == "" {
		return Host{}, fmt.Errorf("empty hostname in %q", s)
	}

	h := Host{Name: host}
	if hasPort && portStr != "" {
		port, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil {
			return Host{}, fmt.Errorf("invalid port in %q: %w", s, err)
		}
		if port == 0 {
			return Host{}, fmt.Errorf("port must be non-zero in %q", s)
		}
		h.Ports = []uint16{uint16(port)}
	}

	return h, nil
}

// Stricter returns the stricter of two profiles.
// Strictness order: locked > standard > permissive.
// If either profile is unrecognised, the other is returned.
func Stricter(a, b ProfileName) ProfileName {
	sa, okA := profileStrictness[a]
	sb, okB := profileStrictness[b]

	switch {
	case !okA:
		return b
	case !okB:
		return a
	case sa >= sb:
		return a
	default:
		return b
	}
}
