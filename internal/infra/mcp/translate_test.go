// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/toolhive/pkg/vmcp"

	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
)

func TestTranslateAuthz_Nil(t *testing.T) {
	t.Parallel()
	assert.Nil(t, translateAuthz(nil))
}

func TestTranslateAuthz_EmptyPolicies(t *testing.T) {
	t.Parallel()
	assert.Nil(t, translateAuthz(&domainconfig.MCPFileAuthzConfig{}))
}

func TestTranslateAuthz_WithPolicies(t *testing.T) {
	t.Parallel()

	policies := []string{
		`permit(principal, action == Action::"list_tools", resource);`,
		`permit(principal, action == Action::"call_tool", resource == Tool::"search_code");`,
	}
	result := translateAuthz(&domainconfig.MCPFileAuthzConfig{Policies: policies})

	require.NotNil(t, result)
	assert.Equal(t, "anonymous", result.Type)
	require.NotNil(t, result.Authz)
	assert.Equal(t, "cedar", result.Authz.Type)
	assert.Equal(t, policies, result.Authz.Policies)
}

func TestTranslateAggregation_Nil(t *testing.T) {
	t.Parallel()
	assert.Nil(t, translateAggregation(nil))
}

func TestTranslateAggregation_Full(t *testing.T) {
	t.Parallel()

	cfg := &domainconfig.MCPAggregationConfig{
		ConflictResolution: "prefix",
		PrefixFormat:       "{workload}_",
		PriorityOrder:      []string{"github", "context7"},
		ExcludeAllTools:    true,
		Tools: []domainconfig.MCPWorkloadToolConfig{
			{
				Workload:   "github",
				Filter:     []string{"search_code", "get_file_contents"},
				ExcludeAll: false,
				Overrides: map[string]*domainconfig.MCPToolOverride{
					"search_code": {
						Name:        "gh_search",
						Description: "Search GitHub code",
					},
				},
			},
			{
				Workload:   "fetch",
				ExcludeAll: true,
			},
		},
	}

	result := translateAggregation(cfg)
	require.NotNil(t, result)

	assert.Equal(t, vmcp.ConflictResolutionStrategy("prefix"), result.ConflictResolution)
	assert.True(t, result.ExcludeAllTools)

	require.NotNil(t, result.ConflictResolutionConfig)
	assert.Equal(t, "{workload}_", result.ConflictResolutionConfig.PrefixFormat)
	assert.Equal(t, []string{"github", "context7"}, result.ConflictResolutionConfig.PriorityOrder)

	require.Len(t, result.Tools, 2)

	// First workload: github with filter and overrides.
	assert.Equal(t, "github", result.Tools[0].Workload)
	assert.Equal(t, []string{"search_code", "get_file_contents"}, result.Tools[0].Filter)
	assert.False(t, result.Tools[0].ExcludeAll)
	require.Contains(t, result.Tools[0].Overrides, "search_code")
	assert.Equal(t, "gh_search", result.Tools[0].Overrides["search_code"].Name)
	assert.Equal(t, "Search GitHub code", result.Tools[0].Overrides["search_code"].Description)

	// Second workload: fetch with exclude_all.
	assert.Equal(t, "fetch", result.Tools[1].Workload)
	assert.True(t, result.Tools[1].ExcludeAll)
}

func TestTranslateAggregation_Partial(t *testing.T) {
	t.Parallel()

	cfg := &domainconfig.MCPAggregationConfig{
		ConflictResolution: "priority",
	}
	result := translateAggregation(cfg)
	require.NotNil(t, result)

	assert.Equal(t, vmcp.ConflictResolutionStrategy("priority"), result.ConflictResolution)
	assert.Nil(t, result.ConflictResolutionConfig)
	assert.Nil(t, result.Tools)
	assert.False(t, result.ExcludeAllTools)
}

func TestTranslateAggregation_NilOverrideSkipped(t *testing.T) {
	t.Parallel()

	cfg := &domainconfig.MCPAggregationConfig{
		Tools: []domainconfig.MCPWorkloadToolConfig{
			{
				Workload: "test",
				Overrides: map[string]*domainconfig.MCPToolOverride{
					"tool1": nil,
					"tool2": {Name: "renamed"},
				},
			},
		},
	}
	result := translateAggregation(cfg)
	require.NotNil(t, result)
	require.Len(t, result.Tools, 1)
	// nil override should not appear in the output map.
	assert.NotContains(t, result.Tools[0].Overrides, "tool1")
	assert.Contains(t, result.Tools[0].Overrides, "tool2")
}
