// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFilterEntries(t *testing.T) {
	t.Parallel()

	skillsEntry := Entry{
		Category:  "skills",
		HostPath:  ".config/skills",
		GuestPath: ".config/skills",
		Kind:      KindDirectory,
	}
	settingsEntry := Entry{
		Category:  "settings",
		HostPath:  "settings.json",
		GuestPath: "settings.json",
		Kind:      KindFile,
	}
	rulesEntry := Entry{
		Category:  "rules",
		HostPath:  ".rules",
		GuestPath: ".rules",
		Kind:      KindDirectory,
	}

	allEntries := []Entry{skillsEntry, settingsEntry, rulesEntry}

	tests := []struct {
		name    string
		entries []Entry
		keep    func(Entry) bool
		want    []Entry
	}{
		{
			name:    "empty entries returns nil",
			entries: nil,
			keep:    func(_ Entry) bool { return true },
			want:    nil,
		},
		{
			name:    "empty slice returns nil",
			entries: []Entry{},
			keep:    func(_ Entry) bool { return true },
			want:    nil,
		},
		{
			name:    "keep all returns full list",
			entries: allEntries,
			keep:    func(_ Entry) bool { return true },
			want:    allEntries,
		},
		{
			name:    "keep none returns nil",
			entries: allEntries,
			keep:    func(_ Entry) bool { return false },
			want:    nil,
		},
		{
			name:    "filter by category keeps skills only",
			entries: allEntries,
			keep:    func(e Entry) bool { return e.Category == "skills" },
			want:    []Entry{skillsEntry},
		},
		{
			name:    "filter by multiple categories",
			entries: allEntries,
			keep: func(e Entry) bool {
				return e.Category == "skills" || e.Category == "rules"
			},
			want: []Entry{skillsEntry, rulesEntry},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := FilterEntries(tt.entries, tt.keep)

			if tt.want == nil {
				assert.Nil(t, got)
			} else {
				require.Equal(t, tt.want, got)
			}
		})
	}
}

func TestFilterEntries_PredicateReceivesCorrectValues(t *testing.T) {
	t.Parallel()

	entries := []Entry{
		{
			Category:  "settings",
			HostPath:  "host/settings.json",
			GuestPath: "guest/settings.json",
			Kind:      KindMergeFile,
			Optional:  true,
			Format:    "json",
		},
		{
			Category:  "skills",
			HostPath:  "host/skills",
			GuestPath: "guest/skills",
			Kind:      KindDirectory,
			Optional:  false,
		},
	}

	var received []Entry
	_ = FilterEntries(entries, func(e Entry) bool {
		received = append(received, e)
		return false
	})

	require.Len(t, received, 2)

	// Verify the predicate received complete Entry values.
	assert.Equal(t, "settings", received[0].Category)
	assert.Equal(t, "host/settings.json", received[0].HostPath)
	assert.Equal(t, "guest/settings.json", received[0].GuestPath)
	assert.Equal(t, KindMergeFile, received[0].Kind)
	assert.True(t, received[0].Optional)
	assert.Equal(t, "json", received[0].Format)

	assert.Equal(t, "skills", received[1].Category)
	assert.Equal(t, "host/skills", received[1].HostPath)
	assert.Equal(t, "guest/skills", received[1].GuestPath)
	assert.Equal(t, KindDirectory, received[1].Kind)
	assert.False(t, received[1].Optional)
}
