// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package settings defines domain types and interfaces for injecting
// agent settings (rules, skills, commands, config files) from the host
// into the guest VM rootfs.
package settings

// EntryKind classifies how a settings entry is injected.
type EntryKind int

const (
	// KindFile copies a single file.
	KindFile EntryKind = iota

	// KindDirectory recursively copies a directory.
	KindDirectory

	// KindMergeFile parses a config file, filters fields, and merges
	// the result into an existing guest file (preserving existing keys).
	KindMergeFile
)

// FieldFilter defines an allowlist of top-level keys to extract from a config file.
// Only keys in AllowKeys are copied. Security: denylist would leak new sensitive keys.
type FieldFilter struct {
	// AllowKeys lists top-level keys to include. Empty means copy all.
	AllowKeys []string

	// DenySubKeys maps allowed top-level keys to sub-key patterns to strip.
	// Patterns support a leading wildcard: "*.apiKey" matches any map entry's
	// "apiKey" field under that top-level key.
	DenySubKeys map[string][]string
}

// Entry describes a single item to inject from host to guest.
type Entry struct {
	// Category groups entries for enable/disable control:
	// "settings", "instructions", "rules", "agents", "skills",
	// "commands", "tools", "plugins", "themes".
	Category string

	// HostPath is relative to $HOME on the host.
	HostPath string

	// GuestPath is relative to the guest home directory.
	// Usually the same as HostPath.
	GuestPath string

	// Kind determines how the entry is processed.
	Kind EntryKind

	// Optional means skip silently if the source does not exist.
	Optional bool

	// Filter controls field-level filtering for KindMergeFile entries.
	// Nil for KindFile and KindDirectory.
	Filter *FieldFilter

	// Format identifies the file format for KindMergeFile entries:
	// "json", "toml", or "jsonc".
	Format string
}

// Manifest is the per-agent list of settings to inject.
type Manifest struct {
	Entries []Entry
}

// Safety limits for settings injection.
const (
	// MaxFileSize is the maximum size per file (1 MiB).
	MaxFileSize int64 = 1 << 20

	// MaxTotalSize is the maximum aggregate size (50 MiB).
	MaxTotalSize int64 = 50 << 20

	// MaxFileCount is the maximum number of files to inject.
	MaxFileCount = 500

	// MaxDepth is the maximum directory nesting depth.
	MaxDepth = 8
)

// Injector copies filtered settings from the host into the guest rootfs.
type Injector interface {
	// Inject processes each entry in the manifest, copying files from
	// hostHomeDir into rootfsPath according to each entry's Kind.
	Inject(rootfsPath, hostHomeDir string, manifest Manifest) error
}
