// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"fmt"
	"testing"

	cedar "github.com/cedar-policy/cedar-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/brood-box/pkg/domain/config"
)

// toolAnnotations describes the MCP tool annotation hints attached to a Cedar
// resource entity. nil pointers mean the attribute is absent (not set by the
// MCP server), which is the scenario that triggers Cedar's `has` guard.
type toolAnnotations struct {
	readOnlyHint    *bool
	destructiveHint *bool
	openWorldHint   *bool
}

// toolScenario is a named annotation combination used across all profiles.
type toolScenario struct {
	name        string
	annotations toolAnnotations
}

func boolPtr(b bool) *bool { return &b }

// scenarios covers the six annotation combinations from the task description.
var scenarios = []toolScenario{
	{
		name: "full annotations (safe read-only, closed-world)",
		annotations: toolAnnotations{
			readOnlyHint: boolPtr(true), destructiveHint: boolPtr(false), openWorldHint: boolPtr(false),
		},
	},
	{
		name: "read-only but open-world (like osv, oci-registry)",
		annotations: toolAnnotations{
			readOnlyHint: boolPtr(true), destructiveHint: boolPtr(false), openWorldHint: boolPtr(true),
		},
	},
	{
		name: "partial annotations — only readOnlyHint=true (like GitHub read tools, context7)",
		annotations: toolAnnotations{
			readOnlyHint: boolPtr(true),
		},
	},
	{
		name:        "no annotations at all (like the fetch MCP server)",
		annotations: toolAnnotations{},
	},
	{
		name: "destructive tool",
		annotations: toolAnnotations{
			readOnlyHint: boolPtr(false), destructiveHint: boolPtr(true), openWorldHint: boolPtr(false),
		},
	},
	{
		name: "write tool, non-destructive, closed-world",
		annotations: toolAnnotations{
			readOnlyHint: boolPtr(false), destructiveHint: boolPtr(false), openWorldHint: boolPtr(false),
		},
	},
}

// buildPolicySet parses a slice of Cedar policy strings into a PolicySet.
func buildPolicySet(t *testing.T, policies []string) *cedar.PolicySet {
	t.Helper()
	ps := cedar.NewPolicySet()
	for i, policyStr := range policies {
		var p cedar.Policy
		require.NoError(t, p.UnmarshalCedar([]byte(policyStr)), "parsing policy %d: %s", i, policyStr)
		ps.Add(cedar.PolicyID(fmt.Sprintf("policy%d", i)), &p)
	}
	return ps
}

// buildEntities creates a Cedar EntityMap with principal, action, and resource
// entities. Resource attributes are populated from the given annotations,
// mirroring how toolhive's annotation enrichment works: only non-nil hints
// are added as attributes.
func buildEntities(
	principal, action, resource cedar.EntityUID,
	ann toolAnnotations,
) cedar.EntityMap {
	attrs := cedar.RecordMap{}
	if ann.readOnlyHint != nil {
		attrs[cedar.String("readOnlyHint")] = cedar.Boolean(*ann.readOnlyHint)
	}
	if ann.destructiveHint != nil {
		attrs[cedar.String("destructiveHint")] = cedar.Boolean(*ann.destructiveHint)
	}
	if ann.openWorldHint != nil {
		attrs[cedar.String("openWorldHint")] = cedar.Boolean(*ann.openWorldHint)
	}
	return cedar.EntityMap{
		principal: {
			UID:        principal,
			Parents:    cedar.NewEntityUIDSet(),
			Attributes: cedar.NewRecord(cedar.RecordMap{}),
			Tags:       cedar.NewRecord(cedar.RecordMap{}),
		},
		action: {
			UID:        action,
			Parents:    cedar.NewEntityUIDSet(),
			Attributes: cedar.NewRecord(cedar.RecordMap{}),
			Tags:       cedar.NewRecord(cedar.RecordMap{}),
		},
		resource: {
			UID:        resource,
			Parents:    cedar.NewEntityUIDSet(),
			Attributes: cedar.NewRecord(attrs),
			Tags:       cedar.NewRecord(cedar.RecordMap{}),
		},
	}
}

func TestResolveProfileReturns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     *config.MCPAuthzConfig
		wantNil bool
		wantErr string
	}{
		{
			name:    "nil config returns nil policies",
			cfg:     nil,
			wantNil: true,
		},
		{
			name:    "empty profile returns nil policies",
			cfg:     &config.MCPAuthzConfig{Profile: ""},
			wantNil: true,
		},
		{
			name:    "full-access returns nil policies",
			cfg:     &config.MCPAuthzConfig{Profile: config.MCPAuthzProfileFullAccess},
			wantNil: true,
		},
		{
			name:    "observe returns non-nil policies",
			cfg:     &config.MCPAuthzConfig{Profile: config.MCPAuthzProfileObserve},
			wantNil: false,
		},
		{
			name:    "safe-tools returns non-nil policies",
			cfg:     &config.MCPAuthzConfig{Profile: config.MCPAuthzProfileSafeTools},
			wantNil: false,
		},
		{
			name:    "custom profile returns error",
			cfg:     &config.MCPAuthzConfig{Profile: config.MCPAuthzProfileCustom},
			wantErr: "custom profile must be resolved from vmcp config",
		},
		{
			name:    "unknown profile returns error",
			cfg:     &config.MCPAuthzConfig{Profile: "custom-thing"},
			wantErr: `unknown MCP authz profile: "custom-thing"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			policies, err := ResolveProfile(tt.cfg)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			if tt.wantNil {
				assert.Nil(t, policies)
			} else {
				assert.NotEmpty(t, policies)
			}
		})
	}
}

func TestCedarAuthorizationDecisions(t *testing.T) {
	t.Parallel()

	principal := cedar.NewEntityUID("Client", "test-agent")

	type actionExpect struct {
		action string
		want   cedar.Decision
	}

	type profileCase struct {
		profile string
		expect  []actionExpect
	}

	tests := []struct {
		scenario    toolScenario
		profileExps []profileCase
	}{
		{
			scenario: scenarios[0], // full annotations (safe read-only, closed-world)
			profileExps: []profileCase{
				{profile: config.MCPAuthzProfileObserve, expect: []actionExpect{
					{"list_tools", cedar.Allow},
					{"call_tool", cedar.Deny},
				}},
				{profile: config.MCPAuthzProfileSafeTools, expect: []actionExpect{
					{"list_tools", cedar.Allow},
					{"call_tool", cedar.Allow},
				}},
			},
		},
		{
			scenario: scenarios[1], // read-only but open-world
			profileExps: []profileCase{
				{profile: config.MCPAuthzProfileObserve, expect: []actionExpect{
					{"list_tools", cedar.Allow},
					{"call_tool", cedar.Deny},
				}},
				{profile: config.MCPAuthzProfileSafeTools, expect: []actionExpect{
					{"list_tools", cedar.Allow},
					{"call_tool", cedar.Allow}, // readOnlyHint=true permits
				}},
			},
		},
		{
			scenario: scenarios[2], // partial annotations — only readOnlyHint=true
			profileExps: []profileCase{
				{profile: config.MCPAuthzProfileObserve, expect: []actionExpect{
					{"list_tools", cedar.Allow},
					{"call_tool", cedar.Deny},
				}},
				{profile: config.MCPAuthzProfileSafeTools, expect: []actionExpect{
					{"list_tools", cedar.Allow},
					{"call_tool", cedar.Allow}, // Bug catch: without `has` guards this would be Deny
				}},
			},
		},
		{
			scenario: scenarios[3], // no annotations at all
			profileExps: []profileCase{
				{profile: config.MCPAuthzProfileObserve, expect: []actionExpect{
					{"list_tools", cedar.Allow},
					{"call_tool", cedar.Deny},
				}},
				{profile: config.MCPAuthzProfileSafeTools, expect: []actionExpect{
					{"list_tools", cedar.Allow},
					{"call_tool", cedar.Deny}, // no hints → no `has` guards pass → default deny
				}},
			},
		},
		{
			scenario: scenarios[4], // destructive tool
			profileExps: []profileCase{
				{profile: config.MCPAuthzProfileObserve, expect: []actionExpect{
					{"list_tools", cedar.Allow},
					{"call_tool", cedar.Deny},
				}},
				{profile: config.MCPAuthzProfileSafeTools, expect: []actionExpect{
					{"list_tools", cedar.Allow},
					{"call_tool", cedar.Deny}, // readOnly=false, destructive=true → both policies deny
				}},
			},
		},
		{
			scenario: scenarios[5], // write tool, non-destructive, closed-world
			profileExps: []profileCase{
				{profile: config.MCPAuthzProfileObserve, expect: []actionExpect{
					{"list_tools", cedar.Allow},
					{"call_tool", cedar.Deny},
				}},
				{profile: config.MCPAuthzProfileSafeTools, expect: []actionExpect{
					{"list_tools", cedar.Allow},
					{"call_tool", cedar.Allow}, // destructive=false && openWorld=false → second policy permits
				}},
			},
		},
	}

	for _, tt := range tests {
		for _, pc := range tt.profileExps {
			for _, ae := range pc.expect {
				name := fmt.Sprintf("%s/%s/%s", pc.profile, tt.scenario.name, ae.action)
				t.Run(name, func(t *testing.T) {
					t.Parallel()

					policies, err := ResolveProfile(&config.MCPAuthzConfig{Profile: pc.profile})
					require.NoError(t, err)
					require.NotNil(t, policies)

					ps := buildPolicySet(t, policies)
					actionUID := cedar.NewEntityUID("Action", cedar.String(ae.action))
					resourceUID := cedar.NewEntityUID("Tool", "test-tool")
					entities := buildEntities(principal, actionUID, resourceUID, tt.scenario.annotations)

					req := cedar.Request{
						Principal: principal,
						Action:    actionUID,
						Resource:  resourceUID,
						Context:   cedar.NewRecord(cedar.RecordMap{}),
					}

					decision, diag := cedar.Authorize(ps, entities, req)

					// Cedar evaluation errors indicate a bug in the policies
					// (e.g., accessing an attribute that doesn't exist without
					// a `has` guard). The whole point of this test is to catch
					// that, so fail hard on any error.
					assert.Empty(t, diag.Errors, "Cedar evaluation errors (likely missing `has` guard)")
					assert.Equal(t, ae.want, decision,
						"profile=%s scenario=%q action=%s", pc.profile, tt.scenario.name, ae.action)
				})
			}
		}
	}
}

func TestResolveProfileDoesNotMutatePackageVars(t *testing.T) {
	t.Parallel()

	// Capture original lengths.
	origObserve := len(observePolicies)
	origSafe := len(safeToolsPolicies)

	// Call resolve for both profiles.
	_, err := ResolveProfile(&config.MCPAuthzConfig{Profile: config.MCPAuthzProfileObserve})
	require.NoError(t, err)
	_, err = ResolveProfile(&config.MCPAuthzConfig{Profile: config.MCPAuthzProfileSafeTools})
	require.NoError(t, err)

	// Package-level slices must be unchanged.
	assert.Len(t, observePolicies, origObserve)
	assert.Len(t, safeToolsPolicies, origSafe)
}
