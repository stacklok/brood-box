// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/stacklok/brood-box/internal/infra/configfile"
	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
)

// UpsertResult reports what an UpsertAgent call did. The fingerprints let the
// caller record a before/after receipt of the config mutation; env values are
// never touched, so the fingerprints only ever cover declared, non-secret
// fields.
type UpsertResult struct {
	// Path is the config file that was written.
	Path string
	// Created is true when the config file did not exist and was created.
	Created bool
	// Replaced is true when an existing agent entry with the same name was
	// overwritten (only possible with force).
	Replaced bool
	// BeforeSHA256 is the hex SHA-256 of the file before the write, or "" when
	// the file did not previously exist.
	BeforeSHA256 string
	// AfterSHA256 is the hex SHA-256 of the file after the write.
	AfterSHA256 string
}

// ErrAgentExists is returned by UpsertAgent when the named agent is already
// declared in the config file and force is false.
var ErrAgentExists = errors.New("agent already exists in config")

// UpsertAgent writes a custom (bring-your-own) agent override under the
// top-level `agents:` key of the global config at path, creating the file (and
// parent directories) if necessary.
//
// The write is a YAML node round-trip: the existing document is decoded into a
// yaml.Node, the single agent entry is inserted or replaced, and the tree is
// re-encoded. Comments elsewhere in the file are preserved; the added/replaced
// agent block itself is normalized YAML (no inline comments) and the document
// is re-indented to two spaces. The override is expected to be pre-validated by
// the caller (config.ValidateCustomAgent) — this function performs no semantic
// validation, only the structural mutation.
//
// If the agent name already exists in the file and force is false, it returns
// ErrAgentExists and leaves the file untouched.
func UpsertAgent(path, name string, override domainconfig.AgentOverride, force bool) (UpsertResult, error) {
	result := UpsertResult{Path: path}

	// Read existing contents (if any). A missing file is not an error — we
	// create it. Any other read error is fatal so we never clobber a file we
	// could not fully understand.
	existing, err := configfile.ReadFile(path, configfile.ReadOptions{})
	switch {
	case err == nil:
		result.BeforeSHA256 = fingerprint(existing)
	case errors.Is(err, fs.ErrNotExist):
		result.Created = true
	default:
		return UpsertResult{}, fmt.Errorf("reading config file %s: %w", path, err)
	}

	// Decode the existing document into a node tree so comments/formatting on
	// untouched keys survive the rewrite. A file that parses to no content
	// (empty, or — like the `config init` template — entirely comments) yields
	// a fresh root mapping and fresh==true. encodeTarget is the document node so
	// document-level (leading) comments survive re-encoding; root is the mapping
	// to mutate.
	encodeTarget, root, fresh, err := rootMapping(existing)
	if err != nil {
		return UpsertResult{}, fmt.Errorf("parsing config file %s: %w", path, err)
	}

	// Build the value node for the override by marshalling it and decoding the
	// result back into a node. This gives clean, tag-driven YAML honoring the
	// AgentOverride omitempty tags.
	valNode, err := overrideNode(override)
	if err != nil {
		return UpsertResult{}, err
	}

	agents, err := ensureMapping(root, "agents")
	if err != nil {
		return UpsertResult{}, err
	}

	replaced, err := setMapEntry(agents, name, valNode, force)
	if err != nil {
		return UpsertResult{}, err
	}
	result.Replaced = replaced

	// Re-encode. Marshalling the node tree preserves the head/foot/line comments
	// captured during decode — but only for files that had real content to hang
	// them on. A comment-only file parses to an empty tree (yaml.v3 cannot
	// attach a comment to a non-existent node), so re-encoding the fresh mapping
	// alone would drop every documentation line. To preserve them, keep the
	// original text verbatim and append just the new `agents:` stanza after it.
	stanza, err := encodeNode(encodeTarget)
	if err != nil {
		return UpsertResult{}, err
	}
	out := stanza
	if fresh && len(bytes.TrimSpace(existing)) > 0 {
		out = appendStanza(existing, stanza)
	}

	if err := writeConfigFile(path, out); err != nil {
		return UpsertResult{}, err
	}
	result.AfterSHA256 = fingerprint(out)

	return result, nil
}

// rootMapping decodes data and returns the node to encode (encodeTarget) plus
// the mapping node to mutate (root). encodeTarget is the document node when the
// file had real content, so document-level leading comments survive a re-encode
// — encoding the mapping alone would drop them. fresh is true when data parses
// to no YAML content (an empty file, or — like the `config init` template — one
// that is entirely comments/whitespace); in that case encodeTarget == root is a
// new empty mapping. It errors when the document root is a non-mapping (e.g. a
// bare scalar or sequence) — we cannot safely graft an `agents:` key onto that.
func rootMapping(data []byte) (encodeTarget, root *yaml.Node, fresh bool, err error) {
	if len(bytes.TrimSpace(data)) == 0 {
		m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		return m, m, true, nil
	}
	doc := &yaml.Node{}
	if err := yaml.Unmarshal(data, doc); err != nil {
		return nil, nil, false, err
	}
	if doc.Kind == 0 || len(doc.Content) == 0 {
		// Non-empty bytes that parse to nothing: a comment-only file.
		m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		return m, m, true, nil
	}
	r := doc.Content[0]
	if r.Kind != yaml.MappingNode {
		return nil, nil, false, fmt.Errorf("config root is not a YAML mapping")
	}
	return doc, r, false, nil
}

// encodeNode marshals a node tree to YAML with two-space indentation.
func encodeNode(root *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		_ = enc.Close()
		return nil, fmt.Errorf("encoding config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encoding config: %w", err)
	}
	return buf.Bytes(), nil
}

// appendStanza returns the original file bytes with the rendered stanza appended
// after a single blank line, normalizing the original's trailing newline so the
// result is well-formed regardless of how the original ended.
func appendStanza(original, stanza []byte) []byte {
	var buf bytes.Buffer
	buf.Write(bytes.TrimRight(original, "\n"))
	buf.WriteString("\n\n")
	buf.Write(stanza)
	return buf.Bytes()
}

// overrideNode marshals an AgentOverride and decodes it back into a mapping
// node suitable for use as a map value.
func overrideNode(override domainconfig.AgentOverride) (*yaml.Node, error) {
	data, err := yaml.Marshal(override)
	if err != nil {
		return nil, fmt.Errorf("marshalling agent override: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("re-decoding agent override: %w", err)
	}
	if len(doc.Content) == 0 {
		// An override with every field empty marshals to "{}"; represent it as
		// an empty mapping so the key is still written.
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	return doc.Content[0], nil
}

// ensureMapping returns the mapping node stored under key in the parent
// mapping, creating an empty one (and the key) when absent. It errors when the
// key exists but its value is not a mapping.
func ensureMapping(parent *yaml.Node, key string) (*yaml.Node, error) {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			val := parent.Content[i+1]
			if val.Kind == yaml.MappingNode {
				return val, nil
			}
			// A null value (e.g. a bare `agents:` line) becomes an empty map.
			if val.Tag == "!!null" {
				val.Kind = yaml.MappingNode
				val.Tag = "!!map"
				val.Value = ""
				return val, nil
			}
			return nil, fmt.Errorf("config key %q is not a mapping", key)
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	parent.Content = append(parent.Content, keyNode, valNode)
	return valNode, nil
}

// setMapEntry inserts or replaces the value for key in the mapping node. It
// returns whether an existing entry was replaced. When the key already exists
// and force is false, it returns ErrAgentExists without modifying the node.
func setMapEntry(m *yaml.Node, key string, value *yaml.Node, force bool) (bool, error) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			if !force {
				return false, fmt.Errorf("%q: %w (use --force to overwrite)", key, ErrAgentExists)
			}
			m.Content[i+1] = value
			return true, nil
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	m.Content = append(m.Content, keyNode, value)
	return false, nil
}

// writeConfigFile writes data to path with owner-only permissions, creating
// parent directories (0700) as needed and forcing 0600 on the file even when
// overwriting an existing one.
func writeConfigFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}
	// WriteFile only sets permissions on creation; force 0600 when overwriting.
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("setting config file permissions: %w", err)
	}
	return nil
}

// fingerprint returns the hex-encoded SHA-256 of data.
func fingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
