// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package settings

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/stacklok/brood-box/internal/infra/safeio"
	"github.com/stacklok/brood-box/pkg/domain/agent"
)

// bboxEnvPrefix bounds ${...} substitution in inject merge trees to the
// universal BBOX_* namespace. Restricting to this prefix keeps forwarded host
// secrets (e.g. ${GITHUB_TOKEN}) from being expanded into a guest config file
// even when the VM env contains them.
const bboxEnvPrefix = "BBOX_"

// MCPConfigInjector implements agent.MCPInjector for data-only agents running
// with mcp.mode:config. It deep-merges each declarative inject entry's tree
// (after ${BBOX_*} substitution) into the corresponding guest config file,
// creating the file when absent and preserving any pre-existing content.
//
// It reuses the settings package's format-aware parse/serialize/merge and
// path-containment helpers so JSON/JSONC/TOML/YAML files are handled
// identically to host-to-guest settings injection.
type MCPConfigInjector struct {
	entries []agent.MCPInjectEntry
	env     map[string]string
	logger  *slog.Logger
}

// NewMCPConfigInjector builds a declarative MCP config injector from resolved
// inject entries and the VM's environment (used for ${BBOX_*} substitution).
func NewMCPConfigInjector(entries []agent.MCPInjectEntry, env map[string]string, logger *slog.Logger) *MCPConfigInjector {
	if logger == nil {
		logger = slog.Default()
	}
	return &MCPConfigInjector{entries: entries, env: env, logger: logger}
}

// Inject patches each declarative config file in the guest rootfs. The
// gatewayIP/port arguments are unused: the proxy endpoint is carried in the
// BBOX_MCP_URL env var (built by the caller from the same gateway/port), which
// the merge tree references via ${BBOX_MCP_URL}.
func (m *MCPConfigInjector) Inject(rootfsPath, _ string, _ uint16, chown agent.ChownFunc) error {
	base := filepath.Join(rootfsPath, sandboxHome)
	for i, e := range m.entries {
		if err := m.injectEntry(base, e, chown); err != nil {
			return fmt.Errorf("mcp inject[%d] %q: %w", i, e.GuestPath, err)
		}
	}
	return nil
}

func (m *MCPConfigInjector) injectEntry(base string, e agent.MCPInjectEntry, chown agent.ChownFunc) error {
	dstPath := filepath.Join(base, e.GuestPath)
	if err := validateContainment(base, dstPath); err != nil {
		return fmt.Errorf("path containment: %w", err)
	}

	// Read any pre-existing guest file so we merge on top of it rather than
	// clobbering keys another hook (credentials, settings) already placed.
	var existing map[string]any
	raw, err := os.ReadFile(dstPath)
	switch {
	case err == nil:
		existing, err = parseConfig(raw, e.Format)
		if err != nil {
			return fmt.Errorf("parsing existing guest file: %w", err)
		}
	case errors.Is(err, os.ErrNotExist):
		existing = make(map[string]any)
	default:
		return fmt.Errorf("reading existing guest file: %w", err)
	}

	// Substitute ${BBOX_*} references, then deep-merge over the existing file.
	tree, ok := expandBBOXEnv(e.Merge, m.env).(map[string]any)
	if !ok {
		// expandBBOXEnv preserves map shape; this is defensive only.
		tree = make(map[string]any)
	}
	merged := deepMerge(existing, tree)

	output, err := serializeConfig(merged, e.Format)
	if err != nil {
		return fmt.Errorf("serializing merged config: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(dstPath), dirPerm); err != nil {
		return fmt.Errorf("creating parent dirs: %w", err)
	}
	bestEffortChown(m.logger, filepath.Dir(dstPath))

	// O_NOFOLLOW: refuse to follow a pre-existing symlink in the guest rootfs
	// that could redirect the write outside the sandbox home.
	if err := safeio.WriteFileNoFollow(dstPath, output, filePerm); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	if chown != nil {
		if err := chown(dstPath, sandboxUID, sandboxGID); err != nil {
			return fmt.Errorf("chowning config: %w", err)
		}
	}

	m.logger.Debug("injected mcp config", "path", e.GuestPath, "format", e.Format)
	return nil
}

// expandBBOXEnv walks a decoded config tree and substitutes ${BBOX_*}
// references in every string leaf. Maps and slices are recursed; other scalar
// types are returned unchanged.
func expandBBOXEnv(v any, env map[string]string) any {
	switch val := v.(type) {
	case string:
		return expandBBOXString(val, env)
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, child := range val {
			out[k] = expandBBOXEnv(child, env)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, child := range val {
			out[i] = expandBBOXEnv(child, env)
		}
		return out
	default:
		return v
	}
}

// tokenPattern matches well-formed ${IDENTIFIER} substitution tokens. Anchoring
// on the identifier character class (rather than pairing the first "${" with
// the nearest following "}") keeps a malformed span earlier in the string from
// swallowing a legitimate token that appears later.
var tokenPattern = regexp.MustCompile(`\$\{[A-Za-z0-9_]+\}`)

// expandBBOXString replaces every ${BBOX_...} token in s with the matching env
// value. Tokens whose name is not BBOX_-prefixed, or that are absent from env,
// are left verbatim so a typo surfaces as an obvious literal rather than a
// silently blanked value.
func expandBBOXString(s string, env map[string]string) string {
	return tokenPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(match, "${"), "}")
		if val, ok := env[name]; ok && strings.HasPrefix(name, bboxEnvPrefix) {
			return val
		}
		// Preserve the literal ${...} token.
		return match
	})
}
