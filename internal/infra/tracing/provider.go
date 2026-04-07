// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package tracing provides OpenTelemetry tracing setup for brood-box.
package tracing

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"

	"github.com/stacklok/brood-box/internal/version"
)

// NewProvider creates a TracerProvider that exports spans synchronously
// to a JSON file at tracePath. WithSyncer is used instead of WithBatcher
// to guarantee all spans are flushed before the CLI process exits.
func NewProvider(tracePath string) (*sdktrace.TracerProvider, error) {
	f, err := os.Create(tracePath)
	if err != nil {
		return nil, fmt.Errorf("create trace file: %w", err)
	}

	exporter, err := stdouttrace.New(
		stdouttrace.WithWriter(f),
		stdouttrace.WithPrettyPrint(),
	)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("create stdout exporter: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceName("brood-box"),
			semconv.ServiceVersion(version.Version),
		),
	)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("create resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithResource(res),
	)

	return tp, nil
}

// Shutdown flushes remaining spans and releases resources. Safe to call
// with a nil provider.
func Shutdown(ctx context.Context, tp *sdktrace.TracerProvider) {
	if tp == nil {
		return
	}
	_ = tp.Shutdown(ctx)
}
