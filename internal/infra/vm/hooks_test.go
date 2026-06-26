// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/brood-box/pkg/domain/agent"
)

// fakeMCPInjector records the arguments passed to Inject and returns the
// configured error so the hook wrapper can be exercised in isolation.
type fakeMCPInjector struct {
	called    bool
	rootfs    string
	gatewayIP string
	port      uint16
	err       error
}

func (f *fakeMCPInjector) Inject(rootfsPath, gatewayIP string, port uint16, _ agent.ChownFunc) error {
	f.called = true
	f.rootfs = rootfsPath
	f.gatewayIP = gatewayIP
	f.port = port
	return f.err
}

func TestInjectMCPConfig_NilInjector_NoOp(t *testing.T) {
	t.Parallel()

	hook := InjectMCPConfig(nil, "192.168.127.1", 4483, bestEffortLchown)
	require.NoError(t, hook("/tmp/unused", nil))
}

func TestInjectMCPConfig_DelegatesToInjector(t *testing.T) {
	t.Parallel()

	injector := &fakeMCPInjector{}
	hook := InjectMCPConfig(injector, "10.0.0.1", 9999, bestEffortLchown)
	require.NoError(t, hook("/rootfs", nil))

	assert.True(t, injector.called, "injector.Inject must be called")
	assert.Equal(t, "/rootfs", injector.rootfs)
	assert.Equal(t, "10.0.0.1", injector.gatewayIP)
	assert.Equal(t, uint16(9999), injector.port)
}

func TestInjectMCPConfig_PropagatesError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("inject boom")
	injector := &fakeMCPInjector{err: wantErr}
	hook := InjectMCPConfig(injector, "10.0.0.1", 9999, bestEffortLchown)
	assert.ErrorIs(t, hook("/rootfs", nil), wantErr)
}
