// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package vm provides the go-microvm-backed VM runner implementation.
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

	microvm "github.com/stacklok/go-microvm"
	"github.com/stacklok/go-microvm/extract"
	"github.com/stacklok/go-microvm/hooks"
	"github.com/stacklok/go-microvm/hypervisor/libkrun"
	"github.com/stacklok/go-microvm/image"
	"github.com/stacklok/go-microvm/net/hosted"
	"github.com/stacklok/go-microvm/net/topology"
	microvmssh "github.com/stacklok/go-microvm/ssh"

	"github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/credential"
	domvm "github.com/stacklok/brood-box/pkg/domain/vm"
)

// Ensure MicroVMRunner implements domvm.VMRunner at compile time.
var _ domvm.VMRunner = (*MicroVMRunner)(nil)

// MicroVMRunner implements VMRunner using the go-microvm library.
type MicroVMRunner struct {
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
type RunnerOption func(*MicroVMRunner)

// WithRunnerPath sets an explicit path to the go-microvm-runner binary.
func WithRunnerPath(p string) RunnerOption {
	return func(r *MicroVMRunner) { r.runnerPath = p }
}

// WithLibDir sets the directory containing bundled shared libraries
// (e.g. Homebrew libkrun). go-microvm passes this as DYLD_LIBRARY_PATH
// or LD_LIBRARY_PATH to the runner subprocess.
func WithLibDir(d string) RunnerOption {
	return func(r *MicroVMRunner) { r.libDir = d }
}

// WithRuntimeSource sets an extract.Source providing go-microvm-runner and libkrun.
// Mutually exclusive with WithRunnerPath and WithLibDir.
func WithRuntimeSource(src extract.Source) RunnerOption {
	return func(r *MicroVMRunner) { r.runtimeSource = src }
}

// WithFirmwareSource sets an extract.Source providing libkrunfw.
func WithFirmwareSource(src extract.Source) RunnerOption {
	return func(r *MicroVMRunner) { r.firmwareSource = src }
}

// WithCacheDir sets the cache directory for bundle-based extract.Sources.
func WithCacheDir(dir string) RunnerOption {
	return func(r *MicroVMRunner) { r.cacheDir = dir }
}

// WithCredentialStore sets the credential store used to inject saved
// agent credentials into the guest rootfs before boot.
func WithCredentialStore(store credential.Store) RunnerOption {
	return func(r *MicroVMRunner) { r.credentialStore = store }
}

// WithImageCacheDir sets a shared OCI image cache directory. When set, the
// image cache is externalized from the per-VM data directory, surviving
// across VM restarts and enabling sub-second warm starts via COW cloning.
func WithImageCacheDir(dir string) RunnerOption {
	return func(r *MicroVMRunner) { r.imageCacheDir = dir }
}

// NewMicroVMRunner creates a VMRunner backed by go-microvm.
func NewMicroVMRunner(logger *slog.Logger, opts ...RunnerOption) *MicroVMRunner {
	r := &MicroVMRunner{logger: logger}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Start boots a microVM using go-microvm.
func (r *MicroVMRunner) Start(ctx context.Context, cfg domvm.VMConfig) (domvm.VM, error) {
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

	privKeyPath, pubKeyPath, err := microvmssh.GenerateKeyPair(keyDir)
	if err != nil {
		_ = os.RemoveAll(keyDir)
		return nil, fmt.Errorf("generating ssh key pair: %w", err)
	}

	pubKey, err := microvmssh.GetPublicKeyContent(pubKeyPath)
	if err != nil {
		_ = os.RemoveAll(keyDir)
		return nil, fmt.Errorf("reading public key: %w", err)
	}

	// Generate host key pair for host key pinning.
	hostKeyPEM, hostPubKey, err := microvmssh.GenerateHostKeyPair()
	if err != nil {
		_ = os.RemoveAll(keyDir)
		return nil, fmt.Errorf("generating host key pair: %w", err)
	}

	// Resolve SSH port. When 0, pick a free ephemeral port up front so
	// we know the actual port before the VM starts. The runner would
	// also bind :0, but it never reports the resolved port back — so
	// we reserve one ourselves and pass the concrete value.
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

	// Build microvm options.
	opts := []microvm.Option{
		microvm.WithName(cfg.Name),
		microvm.WithDataDir(dataDir),
		microvm.WithPreflightChecker(buildPreflightChecker(dataDir)),
		microvm.WithCleanDataDir(),
		microvm.WithCPUs(cfg.CPUs),
		microvm.WithMemory(cfg.Memory),
		microvm.WithLogLevel(cfg.LogLevel),
		microvm.WithTmpSize(cfg.TmpSizeMiB),
		microvm.WithPorts(microvm.PortForward{Host: sshPort, Guest: 22}),
		microvm.WithRootFSHook(
			hooks.InjectAuthorizedKeys(pubKey),
			hooks.InjectFile("/etc/ssh/ssh_host_ecdsa_key", hostKeyPEM, 0o600),
			InjectInitBinary(),
			hooks.InjectEnvFile("/etc/sandbox-env", cfg.EnvVars),
			InjectGitConfig(cfg.GitIdentity, cfg.HasGitToken, bestEffortLchown),
			InjectSSHKnownHosts(bestEffortLchown),
		),
		microvm.WithInitOverride("/bbox-init"),
		microvm.WithPostBoot(func(ctx context.Context, _ *microvm.VM) error {
			r.logger.Debug("waiting for SSH", "port", sshPort)
			client := microvmssh.NewClient("127.0.0.1", sshPort, "sandbox", privKeyPath,
				microvmssh.WithHostKey(hostPubKey),
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
	// This must happen before the egress policy is set because go-microvm
	// will auto-create a hosted.Provider if netProvider is nil.
	if len(cfg.HostServices) > 0 {
		provider := hosted.NewProvider()
		for _, svc := range cfg.HostServices {
			provider.AddService(hosted.Service{
				Port:    svc.Port,
				Handler: svc.Handler,
			})
		}
		opts = append(opts, microvm.WithNetProvider(provider))

		r.logger.Info("registered hosted services",
			"count", len(cfg.HostServices),
		)
	}

	// Add egress policy if specified.
	if cfg.EgressPolicy != nil {
		hosts := make([]microvm.EgressHost, len(cfg.EgressPolicy.AllowedHosts))
		for i, h := range cfg.EgressPolicy.AllowedHosts {
			hosts[i] = microvm.EgressHost{
				Name:     h.Name,
				Ports:    h.Ports,
				Protocol: h.Protocol,
			}
		}
		opts = append(opts, microvm.WithEgressPolicy(microvm.EgressPolicy{
			AllowedHosts: hosts,
		}))
	}

	// Add credential injection hook if store and paths are configured.
	if r.credentialStore != nil && len(cfg.CredentialPaths) > 0 {
		opts = append(opts, microvm.WithRootFSHook(
			InjectCredentials(r.credentialStore, cfg.AgentName, cfg.CredentialPaths),
		))
	}

	// Add MCP config injection hook if host services and agent format are set.
	if len(cfg.HostServices) > 0 && cfg.MCPConfigFormat != "" && cfg.MCPConfigFormat != agent.MCPConfigFormatNone {
		opts = append(opts, microvm.WithRootFSHook(
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
	// Spawn the runner in a user namespace so libkrun's virtiofs passthrough
	// gains CAP_SETGID within the namespace. Without this, set_creds() fails
	// with EPERM when host GID != guest GID.
	backendOpts = append(backendOpts, libkrun.WithUserNamespaceUID(sandboxUID, sandboxGID))
	if len(backendOpts) > 0 {
		opts = append(opts, microvm.WithBackend(libkrun.NewBackend(backendOpts...)))
	}

	// Use external image cache when configured. WithImageCache sets
	// an internal flag so option ordering with WithDataDir is irrelevant.
	if r.imageCacheDir != "" {
		opts = append(opts, microvm.WithImageCache(image.NewCache(r.imageCacheDir)))
	}

	// Add workspace mount if specified.
	if cfg.WorkspacePath != "" {
		absPath, err := filepath.Abs(cfg.WorkspacePath)
		if err != nil {
			return nil, fmt.Errorf("resolving workspace path: %w", err)
		}
		opts = append(opts, microvm.WithVirtioFS(microvm.VirtioFSMount{
			Tag:      "workspace",
			HostPath: absPath,
		}))
	}

	// Run microvm.
	start := time.Now()
	pvm, err := microvm.Run(ctx, cfg.Image, opts...)
	if err != nil {
		// Clean up SSH keys on failure.
		_ = os.RemoveAll(keyDir)
		return nil, fmt.Errorf("starting VM: %w", err)
	}
	r.logger.Debug("sandbox VM started", "elapsed", time.Since(start))

	return &microvmVM{
		vm:         pvm,
		sshPort:    sshPort,
		sshKeyPath: privKeyPath,
		sshKeyDir:  keyDir,
		sshHostKey: hostPubKey,
		logger:     r.logger,
	}, nil
}

// microvmVM wraps a microvm.VM to implement our VM interface.
type microvmVM struct {
	vm         *microvm.VM
	sshPort    uint16
	sshKeyPath string
	sshKeyDir  string
	sshHostKey ssh.PublicKey
	logger     *slog.Logger
}

func (v *microvmVM) Stop(ctx context.Context) error {
	v.logger.Info("stopping sandbox VM")
	err := v.vm.Remove(ctx)
	// Clean up ephemeral SSH keys regardless of stop outcome.
	_ = os.RemoveAll(v.sshKeyDir)
	if err != nil {
		return fmt.Errorf("stopping VM: %w", err)
	}
	return nil
}

func (v *microvmVM) SSHPort() uint16 {
	return v.sshPort
}

func (v *microvmVM) DataDir() string {
	return v.vm.DataDir()
}

func (v *microvmVM) SSHKeyPath() string {
	return v.sshKeyPath
}

func (v *microvmVM) SSHHostKey() ssh.PublicKey {
	return v.sshHostKey
}

func (v *microvmVM) RootFSPath() string {
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
// between closing the listener and the VM binding the same port, but in
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
