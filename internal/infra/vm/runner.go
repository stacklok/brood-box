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
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/stacklok/propolis"
	"github.com/stacklok/propolis/extract"
	"github.com/stacklok/propolis/hooks"
	"github.com/stacklok/propolis/hypervisor/libkrun"
	"github.com/stacklok/propolis/image"
	"github.com/stacklok/propolis/net/hosted"
	"github.com/stacklok/propolis/net/topology"
	propolisssh "github.com/stacklok/propolis/ssh"

	"github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/credential"
	domvm "github.com/stacklok/brood-box/pkg/domain/vm"
)

// Ensure PropolisRunner implements domvm.VMRunner at compile time.
var _ domvm.VMRunner = (*PropolisRunner)(nil)

// PropolisRunner implements VMRunner using the propolis library.
type PropolisRunner struct {
	runnerPath      string
	libDir          string
	runtimeSource   extract.Source
	firmwareSource  extract.Source
	cacheDir        string
	imageCacheDir   string
	credentialStore credential.Store
	logger          *slog.Logger
}

// RunnerOption configures a PropolisRunner.
type RunnerOption func(*PropolisRunner)

// WithRunnerPath sets an explicit path to the propolis-runner binary.
func WithRunnerPath(p string) RunnerOption {
	return func(r *PropolisRunner) { r.runnerPath = p }
}

// WithLibDir sets the directory containing bundled shared libraries
// (e.g. Homebrew libkrun). Propolis passes this as DYLD_LIBRARY_PATH
// or LD_LIBRARY_PATH to the runner subprocess.
func WithLibDir(d string) RunnerOption {
	return func(r *PropolisRunner) { r.libDir = d }
}

// WithRuntimeSource sets an extract.Source providing propolis-runner and libkrun.
// Mutually exclusive with WithRunnerPath and WithLibDir.
func WithRuntimeSource(src extract.Source) RunnerOption {
	return func(r *PropolisRunner) { r.runtimeSource = src }
}

// WithFirmwareSource sets an extract.Source providing libkrunfw.
func WithFirmwareSource(src extract.Source) RunnerOption {
	return func(r *PropolisRunner) { r.firmwareSource = src }
}

// WithCacheDir sets the cache directory for bundle-based extract.Sources.
func WithCacheDir(dir string) RunnerOption {
	return func(r *PropolisRunner) { r.cacheDir = dir }
}

// WithCredentialStore sets the credential store used to inject saved
// agent credentials into the guest rootfs before boot.
func WithCredentialStore(store credential.Store) RunnerOption {
	return func(r *PropolisRunner) { r.credentialStore = store }
}

// WithImageCacheDir sets a shared OCI image cache directory. When set, the
// image cache is externalized from the per-VM data directory, surviving
// across VM restarts and enabling sub-second warm starts via COW cloning.
func WithImageCacheDir(dir string) RunnerOption {
	return func(r *PropolisRunner) { r.imageCacheDir = dir }
}

// NewPropolisRunner creates a VMRunner backed by propolis.
func NewPropolisRunner(logger *slog.Logger, opts ...RunnerOption) *PropolisRunner {
	r := &PropolisRunner{logger: logger}
	for _, o := range opts {
		o(r)
	}
	return r
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
		_ = os.RemoveAll(keyDir)
		return nil, fmt.Errorf("generating ssh key pair: %w", err)
	}

	pubKey, err := propolisssh.GetPublicKeyContent(pubKeyPath)
	if err != nil {
		_ = os.RemoveAll(keyDir)
		return nil, fmt.Errorf("reading public key: %w", err)
	}

	// Generate host key pair for host key pinning.
	hostKeyPEM, hostPubKey, err := propolisssh.GenerateHostKeyPair()
	if err != nil {
		_ = os.RemoveAll(keyDir)
		return nil, fmt.Errorf("generating host key pair: %w", err)
	}

	// Resolve SSH port. When 0, pick a free ephemeral port up front so
	// we know the actual port before propolis starts. The runner inside
	// propolis would also bind :0, but it never reports the resolved
	// port back — so we reserve one ourselves and pass the concrete value.
	sshPort := cfg.SSHPort
	if sshPort == 0 {
		picked, pickErr := pickFreePort()
		if pickErr != nil {
			_ = os.RemoveAll(keyDir)
			return nil, fmt.Errorf("picking ephemeral SSH port: %w", pickErr)
		}
		sshPort = picked
		r.logger.Info("picked ephemeral SSH port", "port", sshPort)
	}

	// Build propolis options.
	opts := []propolis.Option{
		propolis.WithName(cfg.Name),
		propolis.WithDataDir(dataDir),
		propolis.WithPreflightChecker(buildPreflightChecker(dataDir)),
		propolis.WithCleanDataDir(),
		propolis.WithCPUs(cfg.CPUs),
		propolis.WithMemory(cfg.Memory),
		propolis.WithLogLevel(cfg.LogLevel),
		propolis.WithPorts(propolis.PortForward{Host: sshPort, Guest: 22}),
		propolis.WithRootFSHook(
			hooks.InjectAuthorizedKeys(pubKey),
			hooks.InjectFile("/etc/ssh/ssh_host_ecdsa_key", hostKeyPEM, 0o600),
			InjectInitBinary(),
			hooks.InjectEnvFile("/etc/sandbox-env", cfg.EnvVars),
			InjectGitConfig(cfg.GitIdentity, cfg.HasGitToken, bestEffortLchown),
		),
		propolis.WithInitOverride("/bbox-init"),
		propolis.WithPostBoot(func(ctx context.Context, _ *propolis.VM) error {
			r.logger.Debug("waiting for SSH", "port", sshPort)
			client := propolisssh.NewClient("127.0.0.1", sshPort, "sandbox", privKeyPath,
				propolisssh.WithHostKey(hostPubKey),
			)
			start := time.Now()
			if err := client.WaitForReady(ctx); err != nil {
				r.logger.Warn("SSH readiness check failed",
					"elapsed", time.Since(start),
					"error", err,
				)
				return err
			}
			r.logger.Debug("SSH ready", "elapsed", time.Since(start))
			return nil
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

	// Add credential injection hook if store and paths are configured.
	if r.credentialStore != nil && len(cfg.CredentialPaths) > 0 {
		opts = append(opts, propolis.WithRootFSHook(
			InjectCredentials(r.credentialStore, cfg.AgentName, cfg.CredentialPaths),
		))
	}

	// Add MCP config injection hook if host services and agent format are set.
	if len(cfg.HostServices) > 0 && cfg.MCPConfigFormat != "" && cfg.MCPConfigFormat != agent.MCPConfigFormatNone {
		opts = append(opts, propolis.WithRootFSHook(
			InjectMCPConfig(cfg.MCPConfigFormat, topology.GatewayIP, cfg.HostServices[0].Port, bestEffortLchown),
		))
	}

	// Add backend options if runner path, lib dir, or embedded sources are specified.
	var backendOpts []libkrun.Option
	if r.runnerPath != "" {
		backendOpts = append(backendOpts, libkrun.WithRunnerPath(r.runnerPath))
	}
	if r.libDir != "" {
		backendOpts = append(backendOpts, libkrun.WithLibDir(r.libDir))
	}
	if r.runtimeSource != nil {
		backendOpts = append(backendOpts, libkrun.WithRuntime(r.runtimeSource))
	}
	if r.firmwareSource != nil {
		backendOpts = append(backendOpts, libkrun.WithFirmware(r.firmwareSource))
	}
	if r.cacheDir != "" {
		backendOpts = append(backendOpts, libkrun.WithCacheDir(r.cacheDir))
	}
	if len(backendOpts) > 0 {
		opts = append(opts, propolis.WithBackend(libkrun.NewBackend(backendOpts...)))
	}

	// Use external image cache when configured. WithImageCache sets
	// an internal flag so option ordering with WithDataDir is irrelevant.
	if r.imageCacheDir != "" {
		opts = append(opts, propolis.WithImageCache(image.NewCache(r.imageCacheDir)))
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
	start := time.Now()
	pvm, err := propolis.Run(ctx, cfg.Image, opts...)
	if err != nil {
		// Clean up SSH keys on failure.
		_ = os.RemoveAll(keyDir)
		return nil, fmt.Errorf("starting VM: %w", err)
	}
	r.logger.Debug("sandbox VM started", "elapsed", time.Since(start))

	return &propolisVM{
		vm:         pvm,
		sshPort:    sshPort,
		sshKeyPath: privKeyPath,
		sshKeyDir:  keyDir,
		sshHostKey: hostPubKey,
		logger:     r.logger,
	}, nil
}

// propolisVM wraps a propolis.VM to implement our VM interface.
type propolisVM struct {
	vm         *propolis.VM
	sshPort    uint16
	sshKeyPath string
	sshKeyDir  string
	sshHostKey ssh.PublicKey
	logger     *slog.Logger
}

func (v *propolisVM) Stop(ctx context.Context) error {
	v.logger.Info("stopping sandbox VM")
	err := v.vm.Remove(ctx)
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

func (v *propolisVM) SSHHostKey() ssh.PublicKey {
	return v.sshHostKey
}

func (v *propolisVM) RootFSPath() string {
	return v.vm.RootFSPath()
}

// vmDataDir returns a per-VM data directory under ~/.config/broodbox/vms/<name>/data.
// This isolates state files and locks so multiple VMs can run in parallel.
// The parent directory is used by bbox for logs and is cleaned by CleanupStaleLogs.
func vmDataDir(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, ".config", "broodbox", "vms", name, "data"), nil
}

// VMDataDir returns a per-VM data directory under ~/.config/broodbox/vms/<name>/data.
func VMDataDir(name string) (string, error) {
	return vmDataDir(name)
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
