// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package runtimebin

import "testing"

func TestAvailable_Stub(t *testing.T) {
	// Without embed_runtime tag, Available() should return false.
	if Available() {
		t.Error("Available() = true, want false in stub build")
	}
}

func TestRuntimeSource_Stub(t *testing.T) {
	if src := RuntimeSource(); src != nil {
		t.Error("RuntimeSource() = non-nil, want nil in stub build")
	}
}

func TestFirmwareSource_Stub(t *testing.T) {
	if src := FirmwareSource(); src != nil {
		t.Error("FirmwareSource() = non-nil, want nil in stub build")
	}
}

func TestVersion_Default(t *testing.T) {
	if Version != "dev" {
		t.Errorf("Version = %q, want %q", Version, "dev")
	}
}
