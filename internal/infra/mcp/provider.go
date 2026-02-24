// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package mcp provides an in-process vmcp MCP proxy as a host service.
package mcp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/stacklok/toolhive-core/env"
	"github.com/stacklok/toolhive-core/logging"
	thvauth "github.com/stacklok/toolhive/pkg/auth"
	"github.com/stacklok/toolhive/pkg/groups"
	thvlogger "github.com/stacklok/toolhive/pkg/logger"
	"github.com/stacklok/toolhive/pkg/vmcp"
	"github.com/stacklok/toolhive/pkg/vmcp/aggregator"
	vmcpauthfactory "github.com/stacklok/toolhive/pkg/vmcp/auth/factory"
	vmcpclient "github.com/stacklok/toolhive/pkg/vmcp/client"
	vmcpconfig "github.com/stacklok/toolhive/pkg/vmcp/config"
	"github.com/stacklok/toolhive/pkg/vmcp/discovery"
	"github.com/stacklok/toolhive/pkg/vmcp/router"
	vmcpserver "github.com/stacklok/toolhive/pkg/vmcp/server"
	workloadsmgr "github.com/stacklok/toolhive/pkg/workloads"

	"github.com/stacklok/apiary/pkg/domain/hostservice"
)

// VMCPProvider implements hostservice.Provider using toolhive's vmcp library.
type VMCPProvider struct {
	group      string
	port       uint16
	configPath string
	server     *vmcpserver.Server
	logger     *slog.Logger
	logWriter  io.Writer
}

// NewVMCPProvider creates a new provider that will proxy MCP traffic to
// backends discovered in the given ToolHive group.
// logWriter receives toolhive's zap logs (typically the apiary log file).
// If nil, toolhive logs are discarded.
func NewVMCPProvider(group string, port uint16, configPath string, logger *slog.Logger, logWriter io.Writer) *VMCPProvider {
	return &VMCPProvider{
		group:      group,
		port:       port,
		configPath: configPath,
		logger:     logger,
		logWriter:  logWriter,
	}
}

// Services discovers backends from the configured ToolHive group and returns
// an HTTP handler that aggregates their MCP capabilities.
func (p *VMCPProvider) Services(ctx context.Context) ([]hostservice.Service, error) {
	// Redirect the toolhive zap global logger to the apiary log file
	// so vmcp diagnostics don't pollute stdout/stderr during the terminal session.
	initToolhiveLogger(p.logWriter)

	// Force CLI-mode discovery. apiary always runs on the host, never
	// in K8s, but a K8s kubeconfig on the machine would cause auto-detection
	// to pick K8s mode and resolve wrong backend URLs.
	groupsMgr, err := groups.NewCLIManager()
	if err != nil {
		return nil, fmt.Errorf("creating groups manager: %w", err)
	}

	wlMgr, err := workloadsmgr.NewManager(ctx)
	if err != nil {
		return nil, fmt.Errorf("creating workload manager: %w", err)
	}
	backendDiscoverer := aggregator.NewUnifiedBackendDiscoverer(
		workloadsmgr.NewDiscovererAdapter(wlMgr), groupsMgr, nil,
	)

	// Load optional vmcp config for advanced customization.
	var aggConfig *vmcpconfig.AggregationConfig
	if p.configPath != "" {
		loader := vmcpconfig.NewYAMLLoader(p.configPath, &env.OSReader{})
		cfg, loadErr := loader.Load()
		if loadErr != nil {
			return nil, fmt.Errorf("loading vmcp config %s: %w", p.configPath, loadErr)
		}
		aggConfig = cfg.Aggregation
	}

	// Discover backends in the group.
	backends, err := backendDiscoverer.Discover(ctx, p.group)
	if err != nil {
		return nil, fmt.Errorf("discovering backends in group %q: %w", p.group, err)
	}

	if len(backends) == 0 {
		p.logger.Warn("no MCP backends found in group", "group", p.group)
	} else {
		p.logger.Info("discovered MCP backends", "group", p.group, "count", len(backends))
	}

	// Create auth registry (unauthenticated for local ToolHive containers).
	authRegistry, err := vmcpauthfactory.NewOutgoingAuthRegistry(ctx, &env.OSReader{})
	if err != nil {
		return nil, fmt.Errorf("creating auth registry: %w", err)
	}

	// Create backend client.
	backendClient, err := vmcpclient.NewHTTPBackendClient(authRegistry)
	if err != nil {
		return nil, fmt.Errorf("creating backend client: %w", err)
	}

	// Create conflict resolver.
	conflictResolver, err := aggregator.NewConflictResolver(aggConfig)
	if err != nil {
		return nil, fmt.Errorf("creating conflict resolver: %w", err)
	}

	// Create aggregator.
	agg := aggregator.NewDefaultAggregator(backendClient, conflictResolver, aggConfig, nil)

	// Create discovery manager.
	discoveryMgr, err := discovery.NewManager(agg)
	if err != nil {
		return nil, fmt.Errorf("creating discovery manager: %w", err)
	}

	// Create backend registry from discovered backends.
	backendRegistry := vmcp.NewImmutableRegistry(backends)

	// Create router.
	rt := router.NewDefaultRouter()

	// Create vmcp server with local user auth middleware so the discovery
	// manager has an identity in the request context (required for caching).
	srv, err := vmcpserver.New(
		ctx,
		&vmcpserver.Config{
			Name:           "apiary-mcp",
			GroupRef:       p.group,
			Port:           int(p.port),
			EndpointPath:   "/mcp",
			AuthMiddleware: thvauth.LocalUserMiddleware("sandbox"),
		},
		rt,
		backendClient,
		discoveryMgr,
		backendRegistry,
		nil, // no composite workflows
	)
	if err != nil {
		return nil, fmt.Errorf("creating vmcp server: %w", err)
	}
	p.server = srv

	// Get the HTTP handler without starting a listener.
	handler, err := srv.Handler(ctx)
	if err != nil {
		return nil, fmt.Errorf("building vmcp handler: %w", err)
	}

	// Wrap handler with request logging so errors appear in the slog log file.
	logged := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		handler.ServeHTTP(rec, r)
		if rec.status >= 400 {
			p.logger.Error("MCP request failed",
				"method", r.Method, "path", r.URL.Path,
				"status", rec.status)
		} else {
			p.logger.Debug("MCP request",
				"method", r.Method, "path", r.URL.Path,
				"status", rec.status)
		}
	})

	return []hostservice.Service{
		{
			Name:    "mcp",
			Port:    p.port,
			Handler: logged,
		},
	}, nil
}

// Close shuts down the vmcp server gracefully.
func (p *VMCPProvider) Close() error {
	if p.server != nil {
		ctx := context.Background()
		return p.server.Stop(ctx)
	}
	return nil
}

// statusRecorder wraps http.ResponseWriter to capture the status code.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// initToolhiveLogger redirects both the zap global logger and the toolhive
// slog singleton to the given writer (typically the apiary log file) so that
// vmcp diagnostics don't pollute stdout/stderr during the terminal session.
// If w is nil, no-op loggers are installed for both.
func initToolhiveLogger(w io.Writer) {
	if w == nil {
		zap.ReplaceGlobals(zap.NewNop())
		thvlogger.Set(slog.New(slog.NewJSONHandler(io.Discard, nil)))
		return
	}

	// Redirect zap (legacy toolhive code paths).
	enc := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
	core := zapcore.NewCore(enc, zapcore.AddSync(w), zap.DebugLevel)
	zap.ReplaceGlobals(zap.New(core))

	// Redirect the toolhive slog singleton — this is the primary logger used
	// by vmcp internals (aggregator, discovery, server, etc.).
	thvlogger.Set(logging.New(logging.WithOutput(w)))
}
