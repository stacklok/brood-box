// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package vm

import (
	"context"
	"fmt"
	"os"
	"syscall"

	"github.com/stacklok/go-microvm/preflight"
)

const kvmDevicePath = "/dev/kvm"

func buildPreflightChecker(dataDir string) preflight.Checker {
	checker := preflight.NewEmpty()
	checker.Register(kvmCheck())
	checker.Register(preflight.UserNamespaceCheck())
	checker.Register(preflight.DiskSpaceCheck(dataDir, 2.0))
	checker.Register(preflight.ResourceCheck(1, 1.0))
	return checker
}

func kvmCheck() preflight.Check {
	return preflight.Check{
		Name:        "kvm",
		Description: "Verify KVM is available and accessible",
		Run: func(_ context.Context) error {
			info, err := os.Stat(kvmDevicePath)
			if err != nil {
				if os.IsNotExist(err) {
					return fmt.Errorf("%s does not exist: ensure KVM kernel modules are loaded "+
						"(try: sudo modprobe kvm kvm_intel or sudo modprobe kvm kvm_amd)", kvmDevicePath)
				}
				return fmt.Errorf("failed to stat %s: %w", kvmDevicePath, err)
			}

			stat, ok := info.Sys().(*syscall.Stat_t)
			if !ok {
				return fmt.Errorf("unexpected stat type for %s", kvmDevicePath)
			}

			if stat.Mode&syscall.S_IFMT != syscall.S_IFCHR {
				return fmt.Errorf("%s exists but is not a character device", kvmDevicePath)
			}

			f, err := os.OpenFile(kvmDevicePath, os.O_RDWR, 0)
			if err != nil {
				if os.IsPermission(err) {
					return fmt.Errorf("permission denied accessing %s: add your user to the 'kvm' group "+
						"(try: sudo usermod -aG kvm $USER) and log out/in, or run as root", kvmDevicePath)
				}
				return fmt.Errorf("cannot open %s: %w", kvmDevicePath, err)
			}
			_ = f.Close()

			return nil
		},
		Required: true,
	}
}
