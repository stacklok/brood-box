// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package vm defines domain interfaces and types for sandbox VM management.
package vm

import (
	"context"
	"net/http"

	"golang.org/x/crypto/ssh"

	"github.com/stacklok/brood-box/pkg/domain/agent"
	"github.com/stacklok/brood-box/pkg/domain/bytesize"
	"github.com/stacklok/brood-box/pkg/domain/egress"
	"github.com/stacklok/brood-box/pkg/domain/git"
	"github.com/stacklok/brood-box/pkg/domain/settings"
	"github.com/stacklok/brood-box/pkg/domain/workspace"
)

// VMConfig holds the parameters needed to start a sandbox VM.
type VMConfig struct {
	// Name is a unique name for this VM instance.
	Name string

	// Image is the OCI image reference to pull and boot.
	Image string

	// CPUs is the number of vCPUs.
	CPUs uint32

	// Memory is the RAM allocation for this VM.
	Memory bytesize.ByteSize

	// SSHPort is the host port to forward to guest port 22.
	// If 0, an ephemeral port will be chosen.
	SSHPort uint16

	// WorkspacePath is the host directory to mount as /workspace in the VM.
	WorkspacePath string

	// EnvVars are environment variables to inject into the VM.
	EnvVars map[string]string

	// EgressPolicy restricts outbound VM traffic. Nil means no restrictions.
	EgressPolicy *egress.Policy

	// HostServices are HTTP services to expose on the VM gateway IP.
	// Each service is reachable from the guest at http://192.168.127.1:<port>/.
	HostServices []HostService

	// MCPConfigFormat identifies how MCP config should be injected into the rootfs.
	MCPConfigFormat agent.MCPConfigFormat

	// GitIdentity is the git user identity to inject into the VM.
	GitIdentity git.Identity

	// HasGitToken indicates whether a GitHub token is available for forwarding.
	HasGitToken bool

	// SSHAgentForward enables SSH agent forwarding to the VM.
	SSHAgentForward bool

	// AgentName is the canonical agent name (e.g. "claude-code") used as the
	// credential store key. Distinct from Name, which is a unique VM instance
	// identifier.
	AgentName string

	// CredentialPaths lists relative paths (from the sandbox user's home)
	// whose contents are injected into the rootfs before boot.
	CredentialPaths []string

	// LogLevel sets the hypervisor log verbosity (0=off, 5=trace).
	LogLevel uint32

	// TmpSize is the size of the /tmp tmpfs inside the guest.
	// Zero uses the go-microvm default (256 MiB).
	TmpSize bytesize.ByteSize

	// SettingsManifest declares agent settings to inject into the rootfs.
	SettingsManifest *settings.Manifest

	// ExtraMounts are additional virtiofs mounts requested by snapshot
	// post-processors (e.g. git objects for worktree support).
	ExtraMounts []workspace.MountRequest
}

// HostService describes an HTTP service exposed from host to guest.
type HostService struct {
	// Name identifies the service (e.g., "mcp").
	Name string

	// Port is the TCP port on the gateway IP.
	Port uint16

	// Handler serves HTTP requests for this service.
	Handler http.Handler
}

// VMRunner creates and manages sandbox VMs.
type VMRunner interface {
	// Start boots a VM with the given configuration. The returned VM must
	// be stopped when no longer needed.
	Start(ctx context.Context, cfg VMConfig) (VM, error)
}

// VM represents a running sandbox VM.
type VM interface {
	// Stop gracefully shuts down the VM.
	Stop(ctx context.Context) error

	// SSHPort returns the host port mapped to guest SSH.
	SSHPort() uint16

	// DataDir returns the VM's data directory.
	DataDir() string

	// SSHKeyPath returns the path to the ephemeral SSH private key.
	SSHKeyPath() string

	// SSHHostKey returns the expected SSH host public key for this VM.
	// Returns nil when host key pinning is not available, in which case
	// the client should fall back to accepting any host key.
	SSHHostKey() ssh.PublicKey

	// RootFSPath returns the path to the extracted rootfs directory.
	// Used for credential extraction after the session ends.
	RootFSPath() string
}
