// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package progress defines the Observer interface for reporting lifecycle
// progress events. Implementations live in infra/progress.
package progress

// Phase identifies a stage of the sandbox lifecycle.
type Phase int

const (
	// PhaseResolvingAgent is the agent resolution phase.
	PhaseResolvingAgent Phase = iota
	// PhaseCreatingSnapshot is the workspace snapshot creation phase.
	PhaseCreatingSnapshot
	// PhaseStartingVM is the VM boot phase.
	PhaseStartingVM
	// PhaseConnecting is the SSH connection phase.
	PhaseConnecting
	// PhaseShuttingDown is the VM shutdown phase.
	PhaseShuttingDown
	// PhaseComputingDiff is the workspace diff computation phase.
	PhaseComputingDiff
	// PhaseFlushingChanges is the accepted-changes flush phase.
	PhaseFlushingChanges
	// PhaseConfiguringMCP is the MCP proxy configuration phase.
	PhaseConfiguringMCP
	// PhaseSavingCredentials is the credential persistence phase.
	PhaseSavingCredentials
	// PhaseSavingSettings is the settings persistence phase.
	PhaseSavingSettings
	// PhaseCleaning is the snapshot cleanup phase.
	PhaseCleaning
)

// Observer receives typed lifecycle progress events from the application layer.
// Implementations render these as spinners, log lines, or structured output.
type Observer interface {
	// Start begins a new phase with a descriptive message.
	// Any previously active phase is implicitly completed.
	Start(phase Phase, msg string)

	// Complete marks the current phase as successfully finished.
	Complete(msg string)

	// Info emits an informational message (e.g. detected configuration).
	// Stops any active spinner but does not imply phase completion.
	Info(msg string)

	// Warn emits a non-fatal warning for the current phase.
	Warn(msg string)

	// Fail marks the current phase as failed.
	Fail(msg string)
}

// nopObserver is a silent Observer for tests and disabled progress.
type nopObserver struct{}

func (nopObserver) Start(Phase, string) {}
func (nopObserver) Complete(string)     {}
func (nopObserver) Info(string)         {}
func (nopObserver) Warn(string)         {}
func (nopObserver) Fail(string)         {}

// Nop returns a silent Observer that discards all events.
func Nop() Observer { return nopObserver{} }
