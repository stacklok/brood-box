// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ByteSize represents a size in MiB that can be specified with human-readable
// suffixes in configuration files.
//
// Supported formats:
//   - "256m" or "256M" — 256 MiB
//   - "2g" or "2G" — 2 GiB (= 2048 MiB)
//   - "512" — 512 MiB (bare number, backward compatible)
//   - 0 — zero value, meaning "use default"
//
// When used in YAML config, both quoted strings and bare integers work:
//
//	tmp_size: 512       # 512 MiB (backward compatible)
//	tmp_size: "512m"    # 512 MiB
//	tmp_size: "2g"      # 2 GiB
type ByteSize uint32

// MiB returns the size in MiB as a uint32.
func (b ByteSize) MiB() uint32 {
	return uint32(b)
}

// String returns a human-readable representation of the size.
func (b ByteSize) String() string {
	v := uint32(b)
	if v == 0 {
		return "0"
	}
	if v%1024 == 0 {
		return fmt.Sprintf("%dg", v/1024)
	}
	return fmt.Sprintf("%dm", v)
}

// UnmarshalText implements encoding.TextUnmarshaler so that yaml.v3 (and
// encoding/json) can decode human-readable size strings like "512m" or "2g".
//
// Bare YAML integers (e.g. `tmp_size: 512`) bypass this method and are
// decoded directly as uint32 by yaml.v3, preserving backward compatibility.
func (b *ByteSize) UnmarshalText(text []byte) error {
	mib, err := ParseByteSize(string(text))
	if err != nil {
		return err
	}
	*b = mib
	return nil
}

// MarshalText implements encoding.TextMarshaler for round-trip serialization.
func (b ByteSize) MarshalText() ([]byte, error) {
	return []byte(b.String()), nil
}

// ParseByteSize parses a human-readable size string into a ByteSize (MiB).
//
// Supported suffixes (case-insensitive):
//   - "" (bare number) — value is in MiB
//   - "m", "mi", "mib" — mebibytes
//   - "g", "gi", "gib" — gibibytes (×1024 MiB)
func ParseByteSize(s string) (ByteSize, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}

	lower := strings.ToLower(s)

	// Separate numeric prefix from suffix.
	i := 0
	for i < len(lower) && lower[i] >= '0' && lower[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, fmt.Errorf("invalid byte size %q: no numeric value", s)
	}

	numStr := lower[:i]
	suffix := strings.TrimSpace(lower[i:])

	n, err := strconv.ParseUint(numStr, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid byte size %q: %w", s, err)
	}

	var mib uint64
	switch suffix {
	case "", "m", "mi", "mib":
		mib = n
	case "g", "gi", "gib":
		if n > math.MaxUint64/1024 {
			return 0, fmt.Errorf("invalid byte size %q: exceeds maximum (%d MiB)", s, uint64(math.MaxUint32))
		}
		mib = n * 1024
	default:
		return 0, fmt.Errorf("invalid byte size %q: unknown suffix %q (use m or g)", s, suffix)
	}

	if mib > math.MaxUint32 {
		return 0, fmt.Errorf("invalid byte size %q: exceeds maximum (%d MiB)", s, uint64(math.MaxUint32))
	}

	return ByteSize(mib), nil
}
