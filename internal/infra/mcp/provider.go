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
	"github.com/stacklok/toolhive/pkg/vmcp"
	"github.com/stacklok/toolhive/pkg/vmcp/aggregator"
	vmcpauthfactory "github.com/stacklok/toolhive/pkg/vmcp/auth/factory"
	vmcpclient "github.com/stacklok/toolhive/pkg/vmcp/client"
	vmcpconfig "github.com/stacklok/toolhive/pkg/vmcp/config"
	"github.com/stacklok/toolhive/pkg/vmcp/discovery"
	"github.com/stacklok/toolhive/pkg/vmcp/router"
	vmcpserver "github.com/stacklok/toolhive/pkg/vmcp/server"
	workloadsmgr "github.com/stacklok/toolhive/pkg/workloads"

	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
	"github.com/stacklok/brood-box/pkg/domain/hostservice"
)

// VMCPProvider implements hostservice.Provider using toolhive's vmcp library.
type VMCPProvider struct {
	group       string
	port        uint16
	mcpConfig   *domainconfig.MCPFileConfig
	authzConfig *domainconfig.MCPAuthzConfig
	server      *vmcpserver.Server
	logger      *slog.Logger
	logWriter   io.Writer
}

// NewVMCPProvider creates a new provider that will proxy MCP traffic to
// backends discovered in the given ToolHive group.
// mcpConfig provides Cedar policies and aggregation settings (nil = no custom config).
// authzConfig controls MCP authorization (nil = full-access, no restrictions).
// logWriter receives toolhive's zap logs (typically the bbox log file).
// If nil, toolhive logs are discarded.
func NewVMCPProvider(group string, port uint16, mcpConfig *domainconfig.MCPFileConfig, authzConfig *domainconfig.MCPAuthzConfig, logger *slog.Logger, logWriter io.Writer) *VMCPProvider {
	return &VMCPProvider{
		group:       group,
		port:        port,
		mcpConfig:   mcpConfig,
		authzConfig: authzConfig,
		logger:      logger,
		logWriter:   logWriter,
	}
}

// Services discovers backends from the configured ToolHive group and returns
// an HTTP handler that aggregates their MCP capabilities.
func (p *VMCPProvider) Services(ctx context.Context) ([]hostservice.Service, error) {
	// Redirect the toolhive zap/slog loggers to the bbox log file so vmcp
	// diagnostics don't pollute stdout/stderr during the terminal session.
	// Save and restore the slog default because initToolhiveLogger clobbers
	// it, and go-microvm (called later) relies on the broodbox default.
	prevDefault := slog.Default()
	initToolhiveLogger(p.logWriter)
	defer slog.SetDefault(prevDefault)

	// Force CLI-mode discovery. bbox always runs on the host, never
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

	// Translate brood-box MCP config to vmcp types.
	var aggConfig *vmcpconfig.AggregationConfig
	var vmcpIncomingAuth *vmcpconfig.IncomingAuthConfig
	if p.mcpConfig != nil {
		aggConfig = translateAggregation(p.mcpConfig.Aggregation)
		vmcpIncomingAuth = translateAuthz(p.mcpConfig.Authz)
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

	// Resolve authorization middleware.
	authMiddleware, authzMiddleware, authInfoHandler, err := p.resolveAuthMiddleware(
		ctx, vmcpIncomingAuth,
	)
	if err != nil {
		return nil, err
	}

	// Create vmcp server with the resolved auth/authz middleware.
	srv, err := vmcpserver.New(
		ctx,
		&vmcpserver.Config{
			Name:            "bbox-mcp",
			GroupRef:        p.group,
			Port:            int(p.port),
			EndpointPath:    "/mcp",
			AuthMiddleware:  authMiddleware,
			AuthzMiddleware: authzMiddleware,
			AuthInfoHandler: authInfoHandler,
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

// resolveAuthMiddleware builds the auth/authz middleware stack based on the
// configured authorization profile.
//
// - full-access (or nil config): LocalUserMiddleware only, no authz.
// - observe / safe-tools: Anonymous auth + Cedar authz with built-in policies.
// - custom: Anonymous auth + Cedar authz with policies from the vmcp config YAML.
func (p *VMCPProvider) resolveAuthMiddleware(
	ctx context.Context,
	vmcpIncomingAuth *vmcpconfig.IncomingAuthConfig,
) (
	authMw func(http.Handler) http.Handler,
	authzMw func(http.Handler) http.Handler,
	authInfoH http.Handler,
	err error,
) {
	// Handle the "custom" profile: read Cedar policies from the vmcp config YAML.
	if p.authzConfig != nil && p.authzConfig.Profile == domainconfig.MCPAuthzProfileCustom {
		if vmcpIncomingAuth == nil ||
			vmcpIncomingAuth.Authz == nil ||
			len(vmcpIncomingAuth.Authz.Policies) == 0 {
			return nil, nil, nil, fmt.Errorf(
				"MCP authz profile %q requires Cedar policies in --mcp-config "+
					"(authz.policies)", domainconfig.MCPAuthzProfileCustom)
		}
		authMw, authzMw, authInfoH, err = vmcpauthfactory.NewIncomingAuthMiddleware(
			ctx, vmcpIncomingAuth,
		)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("creating custom MCP auth middleware: %w", err)
		}
		return authMw, authzMw, authInfoH, nil
	}

	// Resolve built-in profile to Cedar policies.
	policies, err := ResolveProfile(p.authzConfig)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("resolving MCP authz profile: %w", err)
	}

	if policies == nil {
		// full-access: no authz, just identity for caching.
		return thvauth.LocalUserMiddleware("sandbox"), nil, nil, nil
	}

	authMw, authzMw, authInfoH, err = vmcpauthfactory.NewIncomingAuthMiddleware(
		ctx,
		&vmcpconfig.IncomingAuthConfig{
			Type: "anonymous",
			Authz: &vmcpconfig.AuthzConfig{
				Type:     "cedar",
				Policies: policies,
			},
		},
	)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("creating MCP auth middleware: %w", err)
	}
	return authMw, authzMw, authInfoH, nil
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
// slog singleton to the given writer (typically the bbox log file) so that
// vmcp diagnostics don't pollute stdout/stderr during the terminal session.
// If w is nil, no-op loggers are installed for both.
//
// NOTE: this clobbers the broodbox slog default, which means go-microvm debug
// logs (image pull, cache hit, COW clone) are lost. The proper fix is for
// toolhive to accept an injected *slog.Logger instead of mutating the global.
// Until that's done, we restore the broodbox logger after Services() returns.
func initToolhiveLogger(w io.Writer) {
	if w == nil {
		zap.ReplaceGlobals(zap.NewNop())
		slog.SetDefault(slog.New(slog.NewJSONHandler(io.Discard, nil)))
		return
	}

	// Redirect zap (legacy toolhive code paths).
	enc := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
	core := zapcore.NewCore(enc, zapcore.AddSync(w), zap.DebugLevel)
	zap.ReplaceGlobals(zap.New(core))

	// Redirect the default slog logger — this is the primary logger used
	// by vmcp internals (aggregator, discovery, server, etc.).
	slog.SetDefault(logging.New(logging.WithOutput(w)))
}
