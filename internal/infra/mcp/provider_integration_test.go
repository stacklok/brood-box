// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package mcp_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/brood-box/internal/infra/mcp"
	"github.com/stacklok/brood-box/pkg/domain/config"
)

// skipIfToolhiveUnavailable skips the test when ToolHive is not installed or
// has no running servers in the default group.
func skipIfToolhiveUnavailable(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("thv"); err != nil {
		t.Skip("thv CLI not found — skipping integration test")
	}

	cmd := exec.Command("thv", "list", "--format", "json")
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("thv list failed — skipping integration test: %v", err)
	}

	var servers []json.RawMessage
	if err := json.Unmarshal(out, &servers); err != nil {
		t.Skipf("thv list returned invalid JSON — skipping integration test: %s", out)
	}
	if len(servers) == 0 {
		t.Skip("no ToolHive servers running — skipping integration test")
	}
}

// TestVMCPProvider_Services_Integration verifies that VMCPProvider.Services
// can discover backends, build an HTTP handler, and return a valid service
// when ToolHive is running with at least one server.
func TestVMCPProvider_Services_Integration(t *testing.T) {
	skipIfToolhiveUnavailable(t)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	provider := mcp.NewVMCPProvider(
		"default",  // group
		4483,       // port
		nil,        // no custom MCP config
		nil,        // full-access (no authz restrictions)
		logger,     // logger
		io.Discard, // log writer
	)

	ctx := context.Background()
	services, err := provider.Services(ctx)
	require.NoError(t, err, "Services() must not return an error when ToolHive is running")
	require.Len(t, services, 1, "expected exactly one MCP service")

	svc := services[0]
	assert.Equal(t, "mcp", svc.Name)
	assert.Equal(t, uint16(4483), svc.Port)
	assert.NotNil(t, svc.Handler, "HTTP handler must not be nil")

	// Clean up the vmcp server to avoid leaking goroutines.
	require.NoError(t, provider.Close())
}

// TestVMCPProvider_Services_WithAuthzProfiles_Integration verifies that
// Services succeeds with each built-in authorization profile.
func TestVMCPProvider_Services_WithAuthzProfiles_Integration(t *testing.T) {
	skipIfToolhiveUnavailable(t)

	profiles := []string{
		config.MCPAuthzProfileFullAccess,
		config.MCPAuthzProfileObserve,
		config.MCPAuthzProfileSafeTools,
	}

	for _, profile := range profiles {
		t.Run(profile, func(t *testing.T) {
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))

			provider := mcp.NewVMCPProvider(
				"default",
				4483,
				nil,
				&config.MCPAuthzConfig{Profile: profile},
				logger,
				io.Discard,
			)

			ctx := context.Background()
			services, err := provider.Services(ctx)
			require.NoError(t, err, "Services() must not error with profile %q", profile)
			require.Len(t, services, 1)
			assert.NotNil(t, services[0].Handler)

			require.NoError(t, provider.Close())
		})
	}
}
