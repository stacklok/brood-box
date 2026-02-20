// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package runtime

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewDefaultSandboxDeps_WiresDefaults(t *testing.T) {
	t.Parallel()

	deps := NewDefaultSandboxDeps(DefaultSandboxDepsOpts{})

	require.NotNil(t, deps.Registry)
	require.NotNil(t, deps.VMRunner)
	require.NotNil(t, deps.SessionRunner)
	require.NotNil(t, deps.Config)
	require.NotNil(t, deps.EnvProvider)
	require.NotNil(t, deps.Logger)
	require.NotNil(t, deps.WorkspaceCloner)
	require.NotNil(t, deps.Differ)
	require.NotNil(t, deps.Flusher)
	require.NotNil(t, deps.GitIdentityProvider)
}
