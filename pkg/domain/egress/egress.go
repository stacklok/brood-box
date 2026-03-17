// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package egress defines domain types for DNS-aware egress firewall policies.
// All types are pure data with no I/O dependencies.
package egress

import (
	"fmt"
	"net"
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

	// Reject bracketed IPv6 early (before Cut splits on ':').
	if strings.HasPrefix(s, "[") {
		return Host{}, errIPNotAllowed(s)
	}

	// Detect bare (unbracketted) IPv6 before splitting on ':'.
	// IPv6 addresses contain multiple colons; a valid hostname:port has at most one.
	if strings.Count(s, ":") > 1 {
		return Host{}, errIPNotAllowed(s)
	}

	host, portStr, hasPort := strings.Cut(s, ":")
	if host == "" {
		return Host{}, fmt.Errorf("empty hostname in %q", s)
	}

	// Reject trailing colon with no port (e.g. "example.com:") as malformed.
	if hasPort && portStr == "" {
		return Host{}, fmt.Errorf("missing port after ':' in %q", s)
	}

	// Canonicalize to lowercase — DNS is case-insensitive (RFC 4343)
	// and go-microvm lowercases at matching time, so we normalize early.
	h := Host{Name: strings.ToLower(host)}
	if hasPort {
		port, err := strconv.ParseUint(portStr, 10, 16)
		if err != nil {
			return Host{}, fmt.Errorf("invalid port in %q: %w", s, err)
		}
		if port == 0 {
			return Host{}, fmt.Errorf("port must be non-zero in %q", s)
		}
		h.Ports = []uint16{uint16(port)}
	}

	if err := validateHostname(h.Name); err != nil {
		return Host{}, err
	}

	return h, nil
}

// ValidateHost validates and canonicalizes a Host struct's Name field.
// It lowercases the name (DNS is case-insensitive per RFC 4343) and
// runs all hostname validation rules. The Host is modified in place.
func ValidateHost(h *Host) error {
	h.Name = strings.ToLower(h.Name)
	return validateHostname(h.Name)
}

// errIPNotAllowed returns a standardised error for IP address inputs.
func errIPNotAllowed(input string) error {
	return fmt.Errorf("IP address %q is not allowed: the egress firewall is DNS-based and requires hostnames", input)
}

// validateHostname checks that name is a valid DNS hostname suitable for
// the DNS-based egress firewall. It rejects IP addresses, bare wildcards,
// mid-label wildcards, trailing dots, and labels that violate RFC 1123.
func validateHostname(name string) error {
	if name == "*" {
		return fmt.Errorf("bare wildcard is not allowed; use *.example.com")
	}

	// Reject trailing dots (FQDN notation).
	if strings.HasSuffix(name, ".") {
		return fmt.Errorf("trailing dot not allowed in %q", name)
	}

	// Reject IP addresses (v4, v6, and IPv4-mapped IPv6).
	candidate := name
	// Strip brackets for IPv6 like [::1].
	if strings.HasPrefix(candidate, "[") && strings.HasSuffix(candidate, "]") {
		candidate = candidate[1 : len(candidate)-1]
	}
	if net.ParseIP(candidate) != nil {
		return errIPNotAllowed(name)
	}

	// Total length check (RFC 1035: max 253 characters).
	if len(name) > 253 {
		return fmt.Errorf("hostname exceeds 253 characters (%d)", len(name))
	}

	labels := strings.Split(name, ".")
	for i, label := range labels {
		// Wildcard handling: only valid as the entire leftmost label.
		if strings.Contains(label, "*") {
			if i != 0 || label != "*" {
				return fmt.Errorf("wildcard must be the entire leftmost label, got %q", label)
			}
			continue // skip RFC 1123 checks for the "*" label
		}

		if len(label) == 0 {
			return fmt.Errorf("empty label in hostname %q", name)
		}
		if len(label) > 63 {
			return fmt.Errorf("label %q exceeds 63 characters", label)
		}
		if label[0] == '-' {
			return fmt.Errorf("label %q must not start with a hyphen", label)
		}
		if label[len(label)-1] == '-' {
			return fmt.Errorf("label %q must not end with a hyphen", label)
		}
		for _, c := range label {
			if !isLabelChar(c) {
				return fmt.Errorf("label %q contains invalid character %q", label, c)
			}
		}
	}

	return nil
}

// isLabelChar returns true if c is valid in a DNS label: [a-zA-Z0-9_-].
// Underscores are allowed for SRV/DMARC compatibility.
func isLabelChar(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_'
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
