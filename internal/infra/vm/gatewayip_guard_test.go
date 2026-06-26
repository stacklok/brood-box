// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"testing"

	"github.com/stacklok/go-microvm/net/topology"
)

// sandboxGatewayIP duplicates the literal hardcoded in pkg/sandbox so the
// application layer does not have to import go-microvm's topology package
// (which would violate the "sandbox depends only on domain" rule). This test
// fails if go-microvm ever changes its gateway IP, catching the drift before
// BBOX_MCP_URL silently points at the wrong address.
const sandboxGatewayIP = "192.168.127.1"

func TestSandboxGatewayIPMatchesTopology(t *testing.T) {
	if topology.GatewayIP != sandboxGatewayIP {
		t.Fatalf("go-microvm topology.GatewayIP = %q, but pkg/sandbox hardcodes %q; update sandbox.gatewayIP",
			topology.GatewayIP, sandboxGatewayIP)
	}
}
