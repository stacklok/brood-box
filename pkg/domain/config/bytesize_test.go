// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseByteSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    ByteSize
		wantErr string
	}{
		// Bare numbers (backward compatible — treated as MiB).
		{name: "bare number", input: "512", want: 512},
		{name: "bare zero", input: "0", want: 0},
		{name: "empty string", input: "", want: 0},

		// MiB suffixes.
		{name: "lowercase m", input: "256m", want: 256},
		{name: "uppercase M", input: "256M", want: 256},
		{name: "mi suffix", input: "256mi", want: 256},
		{name: "mib suffix", input: "256MiB", want: 256},

		// GiB suffixes.
		{name: "lowercase g", input: "2g", want: 2048},
		{name: "uppercase G", input: "2G", want: 2048},
		{name: "gi suffix", input: "2gi", want: 2048},
		{name: "gib suffix", input: "2GiB", want: 2048},
		{name: "1g equals 1024m", input: "1g", want: 1024},

		// Whitespace handling.
		{name: "leading whitespace", input: "  512m", want: 512},
		{name: "trailing whitespace", input: "512m  ", want: 512},
		{name: "space before suffix", input: "512 m", want: 512},

		// Error cases.
		{name: "unknown suffix", input: "512k", wantErr: "unknown suffix"},
		{name: "no numeric value", input: "m", wantErr: "no numeric value"},
		{name: "letters only", input: "abc", wantErr: "no numeric value"},
		{name: "negative rejected by ParseUint", input: "-1m", wantErr: "no numeric value"},
		{name: "overflow gib", input: "99999999g", wantErr: "exceeds maximum"},
		{name: "overflow gib wrap", input: "18014398509481985g", wantErr: "exceeds maximum"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseByteSize(tt.input)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestByteSize_MiB(t *testing.T) {
	t.Parallel()

	assert.Equal(t, uint32(0), ByteSize(0).MiB())
	assert.Equal(t, uint32(512), ByteSize(512).MiB())
	assert.Equal(t, uint32(2048), ByteSize(2048).MiB())
}

func TestByteSize_String(t *testing.T) {
	t.Parallel()

	tests := []struct {
		size ByteSize
		want string
	}{
		{0, "0"},
		{256, "256m"},
		{512, "512m"},
		{1024, "1g"},
		{2048, "2g"},
		{1536, "1536m"}, // Not evenly divisible by 1024.
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.size.String())
		})
	}
}

func TestByteSize_UnmarshalText(t *testing.T) {
	t.Parallel()

	var b ByteSize
	require.NoError(t, b.UnmarshalText([]byte("2g")))
	assert.Equal(t, ByteSize(2048), b)

	require.NoError(t, b.UnmarshalText([]byte("512m")))
	assert.Equal(t, ByteSize(512), b)

	require.NoError(t, b.UnmarshalText([]byte("256")))
	assert.Equal(t, ByteSize(256), b)

	err := b.UnmarshalText([]byte("bad"))
	require.Error(t, err)
}

func TestByteSize_MarshalText(t *testing.T) {
	t.Parallel()

	text, err := ByteSize(2048).MarshalText()
	require.NoError(t, err)
	assert.Equal(t, "2g", string(text))

	text, err = ByteSize(512).MarshalText()
	require.NoError(t, err)
	assert.Equal(t, "512m", string(text))
}
