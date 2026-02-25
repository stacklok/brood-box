// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package flavour defines types for workspace toolchain detection.
// All types are pure data with no I/O dependencies.
package flavour

// Name identifies a toolchain flavour.
type Name string

const (
	// Go represents a Go toolchain.
	Go Name = "go"

	// Python represents a Python toolchain.
	Python Name = "python"

	// Node represents a Node.js toolchain.
	Node Name = "node"

	// Rust represents a Rust toolchain.
	Rust Name = "rust"

	// Generic represents no specific toolchain (bare base image).
	Generic Name = "generic"
)

// CLI sentinel values — not valid flavour names, but accepted by the --flavour flag.
const (
	// Auto means auto-detect the workspace flavour from marker files.
	Auto = "auto"

	// None means skip flavour detection and use the bare base image.
	None = "none"
)

// Detection holds the result of workspace flavour detection.
type Detection struct {
	// Primary is the dominant flavour detected in the workspace.
	Primary Name

	// Secondary lists other flavours detected (informational only).
	Secondary []Name
}

// IsValid returns true if the name is a recognized flavour.
func (n Name) IsValid() bool {
	switch n {
	case Go, Python, Node, Rust, Generic:
		return true
	default:
		return false
	}
}

// String returns the string representation of the flavour name.
func (n Name) String() string {
	return string(n)
}

// DisplayName returns a human-friendly name for terminal output.
func (n Name) DisplayName() string {
	switch n {
	case Go:
		return "Go"
	case Python:
		return "Python"
	case Node:
		return "Node.js"
	case Rust:
		return "Rust"
	case Generic:
		return "generic"
	default:
		return string(n)
	}
}

// ValidNames returns all accepted --flavour flag values for CLI help text.
// Includes CLI sentinels (Auto, None) which are not valid domain flavour
// names — use IsValid() to check domain validity.
func ValidNames() []string {
	return []string{Auto, None, "go", "python", "node", "rust"}
}
