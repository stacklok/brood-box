// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/stacklok/propolis/image"

	domainagent "github.com/stacklok/sandbox-agent/internal/domain/agent"
	"github.com/stacklok/sandbox-agent/internal/infra/vm/initbin"
)

// InjectSSHKeys returns a RootFS hook that writes the given public key
// into /home/sandbox/.ssh/authorized_keys in the guest rootfs.
func InjectSSHKeys(pubKey string) func(string, *image.OCIConfig) error {
	return func(rootfsPath string, _ *image.OCIConfig) error {
		sshDir := filepath.Join(rootfsPath, "home", "sandbox", ".ssh")
		if err := os.MkdirAll(sshDir, 0o700); err != nil {
			return fmt.Errorf("creating .ssh dir: %w", err)
		}

		authKeysPath := filepath.Join(sshDir, "authorized_keys")
		if err := os.WriteFile(authKeysPath, []byte(pubKey+"\n"), 0o600); err != nil {
			return fmt.Errorf("writing authorized_keys: %w", err)
		}

		// Chown to sandbox user (UID/GID 1000) — rootfs hooks run as the
		// host user so the files are created with host ownership.
		if err := os.Chown(sshDir, 1000, 1000); err != nil {
			return fmt.Errorf("chowning .ssh dir: %w", err)
		}
		if err := os.Chown(authKeysPath, 1000, 1000); err != nil {
			return fmt.Errorf("chowning authorized_keys: %w", err)
		}

		return nil
	}
}

// InjectInitBinary returns a RootFS hook that writes the embedded sandbox-init
// binary to /sandbox-init in the guest rootfs. This replaces the former shell
// init script and its dependencies (dropbear, iproute2, mount).
func InjectInitBinary() func(string, *image.OCIConfig) error {
	return func(rootfsPath string, _ *image.OCIConfig) error {
		initPath := filepath.Join(rootfsPath, "sandbox-init")
		if err := os.WriteFile(initPath, initbin.Binary, 0o755); err != nil {
			return fmt.Errorf("writing init binary: %w", err)
		}
		return nil
	}
}

// InjectEnvFile returns a RootFS hook that writes forwarded environment
// variables as an /etc/sandbox-env file that can be sourced by the SSH session.
func InjectEnvFile(envVars map[string]string) func(string, *image.OCIConfig) error {
	return func(rootfsPath string, _ *image.OCIConfig) error {
		if len(envVars) == 0 {
			return nil
		}

		etcDir := filepath.Join(rootfsPath, "etc")
		if err := os.MkdirAll(etcDir, 0o755); err != nil {
			return fmt.Errorf("creating /etc dir: %w", err)
		}

		var sb strings.Builder
		for k, v := range envVars {
			fmt.Fprintf(&sb, "export %s=%s\n", k, domainagent.ShellEscape(v))
		}

		envPath := filepath.Join(etcDir, "sandbox-env")
		if err := os.WriteFile(envPath, []byte(sb.String()), 0o600); err != nil {
			return fmt.Errorf("writing env file: %w", err)
		}

		return nil
	}
}
