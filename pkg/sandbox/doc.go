// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package sandbox provides the application service that orchestrates
// the full sandbox VM lifecycle. It is the primary port in apiary's
// hexagonal architecture — both the CLI and library consumers (SDK)
// drive it as peer adapters through the same SandboxRunner API.
package sandbox
