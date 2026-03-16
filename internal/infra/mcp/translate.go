// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package mcp

import (
	"github.com/stacklok/toolhive/pkg/vmcp"
	vmcpconfig "github.com/stacklok/toolhive/pkg/vmcp/config"

	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
)

// translateAggregation converts a brood-box MCPAggregationConfig to the
// vmcp AggregationConfig. Returns nil for nil input.
func translateAggregation(cfg *domainconfig.MCPAggregationConfig) *vmcpconfig.AggregationConfig {
	if cfg == nil {
		return nil
	}

	out := &vmcpconfig.AggregationConfig{
		ConflictResolution: vmcp.ConflictResolutionStrategy(cfg.ConflictResolution),
		ExcludeAllTools:    cfg.ExcludeAllTools,
	}

	// Map flattened prefix/priority fields into nested ConflictResolutionConfig.
	if cfg.PrefixFormat != "" || len(cfg.PriorityOrder) > 0 {
		out.ConflictResolutionConfig = &vmcpconfig.ConflictResolutionConfig{
			PrefixFormat:  cfg.PrefixFormat,
			PriorityOrder: cfg.PriorityOrder,
		}
	}

	// Translate per-workload tool configs.
	if len(cfg.Tools) > 0 {
		out.Tools = make([]*vmcpconfig.WorkloadToolConfig, len(cfg.Tools))
		for i, t := range cfg.Tools {
			out.Tools[i] = translateWorkloadToolConfig(t)
		}
	}

	return out
}

// translateWorkloadToolConfig converts a single brood-box workload tool
// config to the vmcp equivalent.
func translateWorkloadToolConfig(t domainconfig.MCPWorkloadToolConfig) *vmcpconfig.WorkloadToolConfig {
	wt := &vmcpconfig.WorkloadToolConfig{
		Workload:   t.Workload,
		Filter:     t.Filter,
		ExcludeAll: t.ExcludeAll,
	}

	if len(t.Overrides) > 0 {
		wt.Overrides = make(map[string]*vmcpconfig.ToolOverride, len(t.Overrides))
		for name, o := range t.Overrides {
			if o == nil {
				continue
			}
			wt.Overrides[name] = &vmcpconfig.ToolOverride{
				Name:        o.Name,
				Description: o.Description,
			}
		}
	}

	return wt
}

// translateAuthz converts a brood-box MCPFileAuthzConfig to a vmcp
// IncomingAuthConfig with anonymous auth and Cedar policies.
// Returns nil for nil input or empty policies.
func translateAuthz(cfg *domainconfig.MCPFileAuthzConfig) *vmcpconfig.IncomingAuthConfig {
	if cfg == nil || len(cfg.Policies) == 0 {
		return nil
	}

	return &vmcpconfig.IncomingAuthConfig{
		Type: "anonymous",
		Authz: &vmcpconfig.AuthzConfig{
			Type:     "cedar",
			Policies: cfg.Policies,
		},
	}
}
