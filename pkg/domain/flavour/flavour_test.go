// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package flavour

import "testing"

func TestName_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  Name
		valid bool
	}{
		{Go, true},
		{Python, true},
		{Node, true},
		{Rust, true},
		{Generic, true},
		{"", false},
		{"java", false},
		{"auto", false},
		{"none", false},
		{"../../evil", false},
	}

	for _, tt := range tests {
		t.Run(string(tt.name), func(t *testing.T) {
			t.Parallel()
			if got := tt.name.IsValid(); got != tt.valid {
				t.Errorf("Name(%q).IsValid() = %v, want %v", tt.name, got, tt.valid)
			}
		})
	}
}

func TestName_DisplayName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    Name
		display string
	}{
		{Go, "Go"},
		{Python, "Python"},
		{Node, "Node.js"},
		{Rust, "Rust"},
		{Generic, "generic"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		t.Run(string(tt.name), func(t *testing.T) {
			t.Parallel()
			if got := tt.name.DisplayName(); got != tt.display {
				t.Errorf("Name(%q).DisplayName() = %q, want %q", tt.name, got, tt.display)
			}
		})
	}
}

func TestValidNames(t *testing.T) {
	t.Parallel()

	names := ValidNames()
	if len(names) != 6 {
		t.Fatalf("ValidNames() returned %d values, want 6", len(names))
	}

	// Must include CLI sentinels.
	want := map[string]bool{Auto: true, None: true, "go": true, "python": true, "node": true, "rust": true}
	for _, n := range names {
		if !want[n] {
			t.Errorf("unexpected value in ValidNames(): %q", n)
		}
		delete(want, n)
	}
	for n := range want {
		t.Errorf("missing value in ValidNames(): %q", n)
	}
}
