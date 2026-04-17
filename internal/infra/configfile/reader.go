// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package configfile provides hardened file reading and YAML decoding
// primitives shared by brood-box's config loaders (global config,
// workspace `.broodbox.yaml`, MCP vmcp config).
//
// All three loaders want the same three things: a size cap (so a
// gigantic YAML cannot OOM bbox before it can report an error), strict
// unknown-field checking (so security-sensitive typos like
// `mcp.athz.profile: observe` fail loudly rather than silently falling
// back to a permissive default), and — for workspace-local files only
// — rejection of symlinks (so a malicious repo cannot point
// `.broodbox.yaml` at an arbitrary readable file on the host).
package configfile

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// MaxSize is the cap on config file size in bytes. Real broodbox
// configs are single-digit KiB; 1 MiB is generous.
const MaxSize int64 = 1 << 20

// ReadOptions controls the hardening applied when reading a config
// file. The zero value (no workspace-local hardening) is appropriate
// for operator-supplied paths (global config, --mcp-config flag).
type ReadOptions struct {
	// RejectSymlinks, when true, refuses to read a file whose final
	// path component is a symlink. Use for workspace-local config
	// files (e.g. `.broodbox.yaml`) whose path is attacker-controllable.
	RejectSymlinks bool
}

// ReadFile reads a config file with a size cap and, optionally,
// symlink rejection. Errors wrap fs.ErrNotExist when the file is
// absent so callers can distinguish missing from failing.
//
// Non-regular files (FIFOs, sockets, device nodes) are always
// rejected. Opening a FIFO without a writer would block indefinitely
// and produce no useful error, so a malicious or accidental FIFO at
// the config path fails cleanly instead of hanging bbox.
func ReadFile(path string, opts ReadOptions) ([]byte, error) {
	if opts.RejectSymlinks {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		mode := info.Mode()
		if mode&os.ModeSymlink != 0 {
			target, _ := os.Readlink(path)
			return nil, fmt.Errorf(
				"refusing to load %s: symlinks are not allowed for workspace-local config (points to %q)",
				path, target)
		}
		if !mode.IsRegular() {
			return nil, fmt.Errorf(
				"refusing to load %s: not a regular file (mode=%s)",
				path, mode)
		}
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf(
			"config file %s is not a regular file (mode=%s)",
			path, info.Mode())
	}
	if info.Size() > MaxSize {
		return nil, fmt.Errorf(
			"config file %s is too large: %d bytes (limit %d)",
			path, info.Size(), MaxSize)
	}

	// LimitReader as belt-and-braces in case the size changes between
	// stat and read (rare for local files, but the cost is trivial).
	return io.ReadAll(io.LimitReader(f, MaxSize))
}

// DecodeStrict decodes YAML into out with strict unknown-field
// checking. A YAML key that does not correspond to a field on the
// target struct — for example a misspelled `mcp.athz.profile` instead
// of `mcp.authz.profile` — returns an error naming the offending key
// and the line number it appears on, rather than silently being
// dropped and letting the operator's intended tightening fail open.
func DecodeStrict(data []byte, out any) error {
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	return d.Decode(out)
}
