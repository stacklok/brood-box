// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package progress

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/stacklok/apiary/pkg/domain/progress"
)

func TestSpinnerObserver_Complete(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	obs := NewSpinnerObserver(&buf)

	obs.Complete("All done")

	assert.Contains(t, buf.String(), "✓")
	assert.Contains(t, buf.String(), "All done")
}

func TestSpinnerObserver_Info(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	obs := NewSpinnerObserver(&buf)

	obs.Info("Detected Go project")

	assert.Contains(t, buf.String(), "ℹ")
	assert.Contains(t, buf.String(), "Detected Go project")
}

func TestSpinnerObserver_Warn(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	obs := NewSpinnerObserver(&buf)

	obs.Warn("Something off")

	assert.Contains(t, buf.String(), "⚠")
	assert.Contains(t, buf.String(), "Something off")
}

func TestSpinnerObserver_Fail(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	obs := NewSpinnerObserver(&buf)

	obs.Fail("Broke it")

	assert.Contains(t, buf.String(), "✗")
	assert.Contains(t, buf.String(), "Broke it")
}

func TestSpinnerObserver_StartThenComplete(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	obs := NewSpinnerObserver(&buf)

	obs.Start(progress.PhaseStartingVM, "Starting VM...")
	obs.Complete("VM started")

	output := buf.String()
	assert.Contains(t, output, "✓")
	assert.Contains(t, output, "VM started")
}

func TestSpinnerObserver_MultiplePhases(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	obs := NewSpinnerObserver(&buf)

	obs.Start(progress.PhaseResolvingAgent, "Resolving...")
	obs.Complete("Resolved")

	obs.Start(progress.PhaseStartingVM, "Starting...")
	obs.Complete("Started")

	output := buf.String()
	assert.Equal(t, 2, strings.Count(output, "✓"))
}
