// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package vm provides the propolis-backed VM runner implementation.
package vm

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"

	"github.com/stacklok/propolis"
	"github.com/stacklok/propolis/net/hosted"
	"github.com/stacklok/propolis/net/topology"
	propolisssh "github.com/stacklok/propolis/ssh"

	"github.com/stacklok/sandbox-agent/internal/domain/agent"
	domvm "github.com/stacklok/sandbox-agent/internal/domain/vm"
)

// Ensure PropolisRunner implements domvm.VMRunner at compile time.
var _ domvm.VMRunner = (*PropolisRunner)(nil)

// PropolisRunner implements VMRunner using the propolis library.
type PropolisRunner struct {
	runnerPath string
	logger     *slog.Logger
}

// NewPropolisRunner creates a VMRunner backed by propolis.
// runnerPath is the path to the propolis-runner binary.
func NewPropolisRunner(runnerPath string, logger *slog.Logger) *PropolisRunner {
	return &PropolisRunner{
		runnerPath: runnerPath,
		logger:     logger,
	}
}

// Start boots a microVM using propolis.
func (r *PropolisRunner) Start(ctx context.Context, cfg domvm.VMConfig) (domvm.VM, error) {
	r.logger.Info("starting sandbox VM",
		"name", cfg.Name,
		"image", cfg.Image,
		"cpus", cfg.CPUs,
		"memory", cfg.Memory,
		"workspace", cfg.WorkspacePath,
	)

	// Each VM gets its own data directory so multiple VMs can run in parallel
	// without conflicting on state files or logs.
	dataDir, err := vmDataDir(cfg.Name)
	if err != nil {
		return nil, fmt.Errorf("resolving VM data directory: %w", err)
	}

	// Generate ephemeral SSH key pair in a temp dir.
	keyDir, err := os.MkdirTemp("", "sandbox-ssh-*")
	if err != nil {
		return nil, fmt.Errorf("creating ssh key dir: %w", err)
	}

	privKeyPath, pubKeyPath, err := propolisssh.GenerateKeyPair(keyDir)
	if err != nil {
		return nil, fmt.Errorf("generating ssh key pair: %w", err)
	}

	pubKey, err := propolisssh.GetPublicKeyContent(pubKeyPath)
	if err != nil {
		return nil, fmt.Errorf("reading public key: %w", err)
	}

	// Resolve SSH port. When 0, pick a free ephemeral port up front so
	// we know the actual port before propolis starts. The runner inside
	// propolis would also bind :0, but it never reports the resolved
	// port back — so we reserve one ourselves and pass the concrete value.
	sshPort := cfg.SSHPort
	if sshPort == 0 {
		picked, pickErr := pickFreePort()
		if pickErr != nil {
			return nil, fmt.Errorf("picking ephemeral SSH port: %w", pickErr)
		}
		sshPort = picked
		r.logger.Info("picked ephemeral SSH port", "port", sshPort)
	}

	// Build propolis options.
	opts := []propolis.Option{
		propolis.WithName(cfg.Name),
		propolis.WithDataDir(dataDir),
		propolis.WithCPUs(cfg.CPUs),
		propolis.WithMemory(cfg.Memory),
		propolis.WithPorts(propolis.PortForward{Host: sshPort, Guest: 22}),
		propolis.WithRootFSHook(
			InjectSSHKeys(pubKey),
			InjectInitBinary(),
			InjectEnvFile(cfg.EnvVars),
		),
		propolis.WithInitOverride("/sandbox-init"),
		propolis.WithPostBoot(func(ctx context.Context, _ *propolis.VM) error {
			r.logger.Info("waiting for SSH", "port", sshPort)
			client := propolisssh.NewClient("127.0.0.1", sshPort, "sandbox", privKeyPath)
			return client.WaitForReady(ctx)
		}),
	}

	// Register hosted services on a custom network provider so they are
	// reachable from the guest at http://192.168.127.1:<port>/.
	// This must happen before the egress policy is set because propolis
	// will auto-create a hosted.Provider if netProvider is nil.
	if len(cfg.HostServices) > 0 {
		provider := hosted.NewProvider()
		for _, svc := range cfg.HostServices {
			provider.AddService(hosted.Service{
				Port:    svc.Port,
				Handler: svc.Handler,
			})
		}
		opts = append(opts, propolis.WithNetProvider(provider))

		r.logger.Info("registered hosted services",
			"count", len(cfg.HostServices),
		)
	}

	// Add egress policy if specified.
	if cfg.EgressPolicy != nil {
		hosts := make([]propolis.EgressHost, len(cfg.EgressPolicy.AllowedHosts))
		for i, h := range cfg.EgressPolicy.AllowedHosts {
			hosts[i] = propolis.EgressHost{
				Name:     h.Name,
				Ports:    h.Ports,
				Protocol: h.Protocol,
			}
		}
		opts = append(opts, propolis.WithEgressPolicy(propolis.EgressPolicy{
			AllowedHosts: hosts,
		}))
	}

	// Add MCP config injection hook if host services and agent format are set.
	if len(cfg.HostServices) > 0 && cfg.MCPConfigFormat != "" && cfg.MCPConfigFormat != agent.MCPConfigFormatNone {
		opts = append(opts, propolis.WithRootFSHook(
			InjectMCPConfig(cfg.MCPConfigFormat, topology.GatewayIP, cfg.HostServices[0].Port),
		))
	}

	// Add runner path if specified.
	if r.runnerPath != "" {
		opts = append(opts, propolis.WithRunnerPath(r.runnerPath))
	}

	// Add workspace mount if specified.
	if cfg.WorkspacePath != "" {
		absPath, err := filepath.Abs(cfg.WorkspacePath)
		if err != nil {
			return nil, fmt.Errorf("resolving workspace path: %w", err)
		}
		opts = append(opts, propolis.WithVirtioFS(propolis.VirtioFSMount{
			Tag:      "workspace",
			HostPath: absPath,
		}))
	}

	// Run propolis.
	pvm, err := propolis.Run(ctx, cfg.Image, opts...)
	if err != nil {
		// Clean up SSH keys on failure.
		_ = os.RemoveAll(keyDir)
		return nil, fmt.Errorf("starting VM: %w", err)
	}

	return &propolisVM{
		vm:         pvm,
		sshPort:    sshPort,
		sshKeyPath: privKeyPath,
		sshKeyDir:  keyDir,
		logger:     r.logger,
	}, nil
}

// propolisVM wraps a propolis.VM to implement our VM interface.
type propolisVM struct {
	vm         *propolis.VM
	sshPort    uint16
	sshKeyPath string
	sshKeyDir  string
	logger     *slog.Logger
}

func (v *propolisVM) Stop(ctx context.Context) error {
	v.logger.Info("stopping sandbox VM")
	err := v.vm.Stop(ctx)
	// Clean up ephemeral SSH keys regardless of stop outcome.
	_ = os.RemoveAll(v.sshKeyDir)
	if err != nil {
		return fmt.Errorf("stopping VM: %w", err)
	}
	return nil
}

func (v *propolisVM) SSHPort() uint16 {
	return v.sshPort
}

func (v *propolisVM) DataDir() string {
	return v.vm.DataDir()
}

func (v *propolisVM) SSHKeyPath() string {
	return v.sshKeyPath
}

// vmDataDir returns a per-VM data directory under ~/.config/sandbox-agent/<name>.
// This isolates state files, logs, and locks so multiple VMs can run in parallel.
func vmDataDir(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, ".config", "sandbox-agent", "vms", name), nil
}

// pickFreePort asks the kernel for a free TCP port by binding to :0, reading
// the assigned port, then closing the listener. There is a small TOCTOU window
// between closing the listener and propolis binding the same port, but in
// practice this is reliable for localhost ephemeral ports.
func pickFreePort() (uint16, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	addr := ln.Addr().(*net.TCPAddr)
	port := uint16(addr.Port)
	if err := ln.Close(); err != nil {
		return 0, fmt.Errorf("closing ephemeral listener: %w", err)
	}
	return port, nil
}
