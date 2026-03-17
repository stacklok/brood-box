// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build darwin

package vm

import (
	"context"
	"fmt"

	"github.com/stacklok/go-microvm/preflight"
	"golang.org/x/sys/unix"
)

func buildPreflightChecker(dataDir string) preflight.Checker {
	checker := preflight.NewEmpty()
	checker.Register(hvfCheck())
	checker.Register(preflight.DiskSpaceCheck(dataDir, 2.0))
	checker.Register(preflight.ResourceCheck(1, 1.0))
	return checker
}

func hvfCheck() preflight.Check {
	return preflight.Check{
		Name:        "hvf",
		Description: "Verify Hypervisor.framework is available",
		Run: func(_ context.Context) error {
			val, err := unix.SysctlUint32("kern.hv_support")
			if err != nil {
				return fmt.Errorf("cannot check Hypervisor.framework support "+
					"(try: sysctl kern.hv_support): %w", err)
			}
			if val != 1 {
				return fmt.Errorf("hypervisor framework is not available (kern.hv_support=%d): "+
					"Apple Silicon Mac required", val)
			}
			return nil
		},
		Required: true,
	}
}
