// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package progress

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/stacklok/apiary/pkg/domain/progress"
)

func TestSimpleObserver_Start(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	obs := NewSimpleObserver(&buf)

	obs.Start(progress.PhaseStartingVM, "Starting VM...")

	assert.Equal(t, ":: Starting VM...\n", buf.String())
}

func TestSimpleObserver_Complete(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	obs := NewSimpleObserver(&buf)

	obs.Complete("VM started")

	assert.Equal(t, "   done: VM started\n", buf.String())
}

func TestSimpleObserver_Warn(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	obs := NewSimpleObserver(&buf)

	obs.Warn("No changes")

	assert.Equal(t, "   warn: No changes\n", buf.String())
}

func TestSimpleObserver_Fail(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	obs := NewSimpleObserver(&buf)

	obs.Fail("VM crashed")

	assert.Equal(t, "   FAILED: VM crashed\n", buf.String())
}

func TestSimpleObserver_FullLifecycle(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	obs := NewSimpleObserver(&buf)

	obs.Start(progress.PhaseResolvingAgent, "Resolving agent...")
	obs.Complete("Resolved agent test (2 CPUs, 2048 MiB)")
	obs.Start(progress.PhaseStartingVM, "Starting sandbox VM...")
	obs.Complete("Sandbox ready")

	output := buf.String()
	assert.Contains(t, output, ":: Resolving agent...")
	assert.Contains(t, output, "done: Resolved agent test")
	assert.Contains(t, output, ":: Starting sandbox VM...")
	assert.Contains(t, output, "done: Sandbox ready")
}
