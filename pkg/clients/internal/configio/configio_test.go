// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package configio

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stacklok/brood-box/pkg/domain/agent"
)

// chownCall records a single chown invocation.
type chownCall struct {
	Path string
	UID  int
	GID  int
}

func recordingChown() (agent.ChownFunc, func() []chownCall) {
	var mu sync.Mutex
	var calls []chownCall
	fn := func(path string, uid, gid int) error {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, chownCall{Path: path, UID: uid, GID: gid})
		return nil
	}
	get := func() []chownCall {
		mu.Lock()
		defer mu.Unlock()
		return append([]chownCall{}, calls...)
	}
	return fn, get
}

func TestMergeJSONMapEntries_CorruptFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	chown, _ := recordingChown()

	corruptPath := filepath.Join(dir, "corrupt.json")
	require.NoError(t, os.WriteFile(corruptPath, []byte(`{not valid json`), 0o644))

	err := MergeJSONMapEntries(dir, "corrupt.json", "key", map[string]any{
		"new-entry": map[string]any{"url": "http://localhost"},
	}, chown)
	assert.Error(t, err, "should fail on corrupt JSON")
	assert.Contains(t, err.Error(), "parsing existing")
}

func TestMergeJSONMapEntries_NonMapValue(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	chown, _ := recordingChown()

	filePath := filepath.Join(dir, "nonmap.json")
	require.NoError(t, os.WriteFile(filePath, []byte(`{"key": "string_not_map"}`), 0o644))

	err := MergeJSONMapEntries(dir, "nonmap.json", "key", map[string]any{
		"new-entry": map[string]any{"url": "http://localhost"},
	}, chown)
	require.NoError(t, err, "should succeed by replacing the non-map value")

	data, err := os.ReadFile(filePath)
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	var innerMap map[string]any
	require.NoError(t, json.Unmarshal(raw["key"], &innerMap))
	assert.Contains(t, innerMap, "new-entry")
}

func TestMkdirAndChown_ChownsCreatedDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	chown, getCalls := recordingChown()

	dir := filepath.Join(root, "deep", "tree")
	require.NoError(t, MkdirAndChown(dir, chown))

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	calls := getCalls()
	require.NotEmpty(t, calls)
	assert.Equal(t, dir, calls[len(calls)-1].Path)
	assert.Equal(t, SandboxUID, calls[len(calls)-1].UID)
	assert.Equal(t, SandboxGID, calls[len(calls)-1].GID)
}
