// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package hostservice defines domain types for services hosted on the
// VM gateway IP, reachable from the guest.
package hostservice

import (
	"context"
	"net/http"
)

// Service describes an HTTP service to expose inside the VM network.
type Service struct {
	// Name identifies the service (e.g., "mcp").
	Name string

	// Port is the TCP port on the gateway IP.
	Port uint16

	// Handler serves HTTP requests for this service.
	Handler http.Handler
}

// Provider creates host services for a sandbox session.
type Provider interface {
	// Services returns the services to register with the network provider.
	Services(ctx context.Context) ([]Service, error)

	// Close releases provider resources.
	Close() error
}
