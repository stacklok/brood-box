// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	infraconfig "github.com/stacklok/brood-box/internal/infra/config"
	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
)

// writeManifest writes a manifest YAML file for tests.
func writeManifest(t *testing.T, path, yamlContent string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(yamlContent), 0o600))
}

const sampleManifestYAML = `name: aider
image: ghcr.io/acme/aider-bbox:latest
command: ["aider"]
description: ACME agent
env_forward: [OPENAI_API_KEY]
env_required: [OPENAI_API_KEY]
egress_profile: standard
egress_hosts:
  standard:
    - name: api.openai.com
      ports: [443]
mcp:
  enabled: true
  mode: env
  authz:
    profile: safe-tools
`

func TestAgentsImportEndToEnd(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "present-value")

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	manifestPath := filepath.Join(t.TempDir(), "aider.yaml")
	writeManifest(t, manifestPath, sampleManifestYAML)

	var out bytes.Buffer
	cmd := agentsCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"import", manifestPath, "--config", cfgPath, "--json"})
	require.NoError(t, cmd.Execute())

	var receipt agentReceipt
	require.NoError(t, json.Unmarshal(out.Bytes(), &receipt))
	assert.Equal(t, "agents import", receipt.Command)
	assert.True(t, receipt.OK)
	assert.Equal(t, "custom", receipt.Agent.Type)
	assert.Equal(t, "aider", receipt.Agent.Name)
	assert.Equal(t, "ghcr.io/acme/aider-bbox:latest", receipt.Agent.Image)
	assert.Equal(t, "standard", receipt.Agent.EgressProfile)
	assert.Equal(t, domainconfig.MCPModeEnv, receipt.Agent.MCPMode)
	require.NotNil(t, receipt.Write)
	assert.True(t, receipt.Write.Created)

	// doctor --json on the imported agent passes.
	var dout bytes.Buffer
	dcmd := agentsCmd()
	dcmd.SetOut(&dout)
	dcmd.SetErr(&dout)
	dcmd.SetArgs([]string{"doctor", "aider", "--config", cfgPath, "--json"})
	require.NoError(t, dcmd.Execute())

	var dr agentReceipt
	require.NoError(t, json.Unmarshal(dout.Bytes(), &dr))
	assert.True(t, dr.OK)

	// The written config round-trips through the loader.
	loaded, err := infraconfig.NewLoader(cfgPath).Load()
	require.NoError(t, err)
	custom, ok := loaded.Agents["aider"]
	require.True(t, ok)
	assert.Equal(t, "ghcr.io/acme/aider-bbox:latest", custom.Image)
	assert.Equal(t, []string{"aider"}, custom.Command)
	assert.Equal(t, []string{"OPENAI_API_KEY"}, custom.EnvForward)
	require.NotNil(t, custom.MCP)
	assert.Equal(t, domainconfig.MCPModeEnv, custom.MCP.Mode)
}

func TestAgentsImportRefusesExistingWithoutForce(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	manifestPath := filepath.Join(t.TempDir(), "aider.yaml")
	writeManifest(t, manifestPath, sampleManifestYAML)

	args := []string{"import", manifestPath, "--config", cfgPath}

	first := agentsCmd()
	first.SetOut(&bytes.Buffer{})
	first.SetErr(&bytes.Buffer{})
	first.SetArgs(args)
	require.NoError(t, first.Execute())

	second := agentsCmd()
	second.SetOut(&bytes.Buffer{})
	second.SetErr(&bytes.Buffer{})
	second.SetArgs(args)
	err := second.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")

	// --force allows the overwrite.
	third := agentsCmd()
	third.SetOut(&bytes.Buffer{})
	third.SetErr(&bytes.Buffer{})
	third.SetArgs(append(args, "--force"))
	require.NoError(t, third.Execute())
}

func TestAgentsImportRefusesBuiltin(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	manifestPath := filepath.Join(t.TempDir(), "claude.yaml")
	writeManifest(t, manifestPath, `name: claude-code
image: ghcr.io/acme/x:latest
command: ["x"]
egress_profile: permissive
`)

	cmd := agentsCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"import", manifestPath, "--config", cfgPath})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "built-in agent")
	_, statErr := os.Stat(cfgPath)
	assert.True(t, os.IsNotExist(statErr))
}

func TestAgentsImportRejectsMissingName(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	manifestPath := filepath.Join(t.TempDir(), "noname.yaml")
	writeManifest(t, manifestPath, `image: ghcr.io/acme/x:latest
command: ["x"]
egress_profile: permissive
`)

	cmd := agentsCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"import", manifestPath, "--config", cfgPath})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"name" field is required`)
	_, statErr := os.Stat(cfgPath)
	assert.True(t, os.IsNotExist(statErr))
}

func TestAgentsImportRejectsInvalidManifest(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	manifestPath := filepath.Join(t.TempDir(), "bad.yaml")
	// Missing required command -> ValidateCustomAgent fails.
	writeManifest(t, manifestPath, `name: bad
image: ghcr.io/acme/x:latest
egress_profile: permissive
`)

	cmd := agentsCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"import", manifestPath, "--config", cfgPath})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "command is required")
	_, statErr := os.Stat(cfgPath)
	assert.True(t, os.IsNotExist(statErr))
}

func TestAgentsImportRejectsUnknownField(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	manifestPath := filepath.Join(t.TempDir(), "unknown.yaml")
	writeManifest(t, manifestPath, `name: x
image: ghcr.io/acme/x:latest
command: ["x"]
egress_profile: permissive
bogus_field: true
`)

	cmd := agentsCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"import", manifestPath, "--config", cfgPath})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bogus_field")
}

func TestAgentsExportEndToEnd(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "present-value")

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	manifestPath := filepath.Join(t.TempDir(), "aider.yaml")
	writeManifest(t, manifestPath, sampleManifestYAML)

	// Import first so the agent exists in the config.
	importCmd := agentsCmd()
	importCmd.SetOut(&bytes.Buffer{})
	importCmd.SetErr(&bytes.Buffer{})
	importCmd.SetArgs([]string{"import", manifestPath, "--config", cfgPath})
	require.NoError(t, importCmd.Execute())

	// Export it back out.
	var out bytes.Buffer
	expCmd := agentsCmd()
	expCmd.SetOut(&out)
	expCmd.SetErr(&out)
	expCmd.SetArgs([]string{"export", "aider", "--config", cfgPath})
	require.NoError(t, expCmd.Execute())

	exported := out.String()
	assert.Contains(t, exported, "name: aider")
	assert.Contains(t, exported, "image: ghcr.io/acme/aider-bbox:latest")
	assert.Contains(t, exported, "api.openai.com")
	assert.Contains(t, exported, "mode: env")

	// Re-import the exported manifest into a fresh config — round-trip no-op.
	cfgPath2 := filepath.Join(t.TempDir(), "config2.yaml")
	reExported := filepath.Join(t.TempDir(), "reexported.yaml")
	writeManifest(t, reExported, exported)

	var out2 bytes.Buffer
	reimportCmd := agentsCmd()
	reimportCmd.SetOut(&out2)
	reimportCmd.SetErr(&out2)
	reimportCmd.SetArgs([]string{"import", reExported, "--config", cfgPath2, "--json"})
	require.NoError(t, reimportCmd.Execute())

	var receipt agentReceipt
	require.NoError(t, json.Unmarshal(out2.Bytes(), &receipt))
	assert.True(t, receipt.OK)
	assert.Equal(t, "aider", receipt.Agent.Name)
	assert.Equal(t, "ghcr.io/acme/aider-bbox:latest", receipt.Agent.Image)

	// Both configs hold equivalent agent definitions.
	l1, err := infraconfig.NewLoader(cfgPath).Load()
	require.NoError(t, err)
	l2, err := infraconfig.NewLoader(cfgPath2).Load()
	require.NoError(t, err)
	assert.Equal(t, l1.Agents["aider"], l2.Agents["aider"])
}

func TestAgentsExportStripsDefaultEnvValues(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	// Seed a config with a custom agent that has default_env values (secrets).
	seed := `agents:
  sec:
    image: ghcr.io/acme/sec:latest
    command: ["sec"]
    egress_profile: permissive
    default_env:
      API_KEY: "super-secret-value"
      OTHER: "another-secret"
`
	require.NoError(t, os.WriteFile(cfgPath, []byte(seed), 0o600))

	var out bytes.Buffer
	cmd := agentsCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"export", "sec", "--config", cfgPath})
	require.NoError(t, cmd.Execute())

	exported := out.String()
	assert.NotContains(t, exported, "super-secret-value")
	assert.NotContains(t, exported, "another-secret")
	assert.NotContains(t, exported, "default_env")
}

func TestAgentsExportRefusesBuiltin(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	cmd := agentsCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"export", "claude-code", "--config", cfgPath})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "built-in agent")
}

func TestAgentsExportRefusesUnknown(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	cmd := agentsCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"export", "nope", "--config", cfgPath})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not declared")
}

func TestAgentsExportImportRoundTripsCredentialsAndSettings(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	manifestPath := filepath.Join(t.TempDir(), "aider.yaml")
	writeManifest(t, manifestPath, `name: aider
image: ghcr.io/acme/aider-bbox:latest
command: ["aider"]
egress_profile: permissive
credentials:
  persist:
    - ".gitconfig"
    - ".config/foo/"
settings:
  - category: settings
    host_path: ".aiderrc"
    guest_path: ".aiderrc"
    kind: merge-file
    format: json
    optional: true
    allow_keys: ["model", "theme"]
`)

	// Import into the first config.
	importCmd := agentsCmd()
	importCmd.SetOut(&bytes.Buffer{})
	importCmd.SetErr(&bytes.Buffer{})
	importCmd.SetArgs([]string{"import", manifestPath, "--config", cfgPath})
	require.NoError(t, importCmd.Execute())

	// Export it back out.
	var out bytes.Buffer
	expCmd := agentsCmd()
	expCmd.SetOut(&out)
	expCmd.SetErr(&out)
	expCmd.SetArgs([]string{"export", "aider", "--config", cfgPath})
	require.NoError(t, expCmd.Execute())

	exported := out.String()
	assert.Contains(t, exported, ".gitconfig")
	assert.Contains(t, exported, ".config/foo/")
	assert.Contains(t, exported, ".aiderrc")
	assert.Contains(t, exported, "merge-file")
	assert.Contains(t, exported, "model")
	assert.Contains(t, exported, "theme")

	// Re-import the exported manifest into a fresh config.
	cfgPath2 := filepath.Join(t.TempDir(), "config2.yaml")
	reExported := filepath.Join(t.TempDir(), "reexported.yaml")
	writeManifest(t, reExported, exported)

	reimportCmd := agentsCmd()
	reimportCmd.SetOut(&bytes.Buffer{})
	reimportCmd.SetErr(&bytes.Buffer{})
	reimportCmd.SetArgs([]string{"import", reExported, "--config", cfgPath2})
	require.NoError(t, reimportCmd.Execute())

	// Both configs hold equivalent agent definitions, including credentials/settings.
	l1, err := infraconfig.NewLoader(cfgPath).Load()
	require.NoError(t, err)
	l2, err := infraconfig.NewLoader(cfgPath2).Load()
	require.NoError(t, err)
	assert.Equal(t, l1.Agents["aider"], l2.Agents["aider"])

	custom := l1.Agents["aider"]
	require.NotNil(t, custom.Credentials)
	assert.Equal(t, []string{".gitconfig", ".config/foo/"}, custom.Credentials.Persist)
	require.Len(t, custom.Settings, 1)
	assert.Equal(t, "settings", custom.Settings[0].Category)
	assert.Equal(t, ".aiderrc", custom.Settings[0].HostPath)
	assert.Equal(t, "merge-file", custom.Settings[0].Kind)
	assert.Equal(t, "json", custom.Settings[0].Format)
	assert.True(t, custom.Settings[0].Optional)
	assert.Equal(t, []string{"model", "theme"}, custom.Settings[0].AllowKeys)
}

func TestAgentsExportConfigFileMissing(t *testing.T) {
	t.Parallel()

	cfgPath := filepath.Join(t.TempDir(), "does-not-exist.yaml")
	cmd := agentsCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"export", "aider", "--config", cfgPath})
	err := cmd.Execute()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not declared")
}

func TestLoadManifestOperatorPathAcceptsSymlink(t *testing.T) {
	t.Parallel()

	// Import paths are operator-supplied (like --config), not workspace-local,
	// so a symlink is accepted — matching the global config loader's behavior.
	dir := t.TempDir()
	target := filepath.Join(dir, "real.yaml")
	writeManifest(t, target, sampleManifestYAML)
	link := filepath.Join(dir, "link.yaml")
	require.NoError(t, os.Symlink(target, link))

	m, err := infraconfig.LoadManifest(link)
	require.NoError(t, err)
	assert.Equal(t, "aider", m.Name)
}

// fakeFetcher is an in-memory ocimage.Fetcher for e2e tests of the image
// import path. It returns canned manifest bytes and a fake digest-pinned ref,
// so the full CLI path is exercised without network.
type fakeFetcher struct {
	manifestBytes []byte
	pinnedRef     string
	err           error
	calledRef     string
}

func (f *fakeFetcher) FetchAgentManifest(_ context.Context, ref string) ([]byte, string, error) {
	f.calledRef = ref
	if f.err != nil {
		// Mirror RemoteFetcher's contract: every error is wrapped with the ref
		// so callers never need to add their own ref-identifying wrap.
		return nil, "", fmt.Errorf("importing agent from image %q: %w", ref, f.err)
	}
	return f.manifestBytes, f.pinnedRef, nil
}

// imageManifestYAML is a manifest as it would be embedded in an image. Its
// image field is intentionally the un-pinned tag; the import path overrides it
// with the digest-pinned ref returned by the fake fetcher.
const imageManifestYAML = `name: aider
image: ghcr.io/acme/aider-bbox:latest
command: ["aider"]
description: ACME agent
env_forward: [OPENAI_API_KEY]
env_required: [OPENAI_API_KEY]
egress_profile: standard
egress_hosts:
  standard:
    - name: api.openai.com
      ports: [443]
mcp:
  enabled: true
  mode: env
  authz:
    profile: safe-tools
`

// fakePinnedRef is a digest-pinned ref shape the fake fetcher returns. It must
// pass imageRefValidator (name.ParseReference), so it is a valid digest ref.
const fakePinnedRef = "ghcr.io/acme/aider-bbox@sha256:0000000000000000000000000000000000000000000000000000000000000000"

func TestAgentsImportImageEndToEnd(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "present-value")

	fetcher := &fakeFetcher{
		manifestBytes: []byte(imageManifestYAML),
		pinnedRef:     fakePinnedRef,
	}

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	imageRef := "ghcr.io/acme/aider-bbox:latest"

	var out, errBuf bytes.Buffer
	cmd := agentsImportCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"import", imageRef, "--config", cfgPath, "--json"})
	require.NoError(t, runAgentsImport(cmd, imageRef, cfgPath, "", false, true, fetcher))

	// The fetcher was invoked with the operator-typed ref.
	assert.Equal(t, imageRef, fetcher.calledRef)

	var receipt agentReceipt
	require.NoError(t, json.Unmarshal(out.Bytes(), &receipt))
	assert.Equal(t, "agents import", receipt.Command)
	assert.True(t, receipt.OK)
	assert.Equal(t, "custom", receipt.Agent.Type)
	assert.Equal(t, "aider", receipt.Agent.Name)
	// The image field is the digest-pinned ref, not the un-pinned tag.
	assert.Equal(t, fakePinnedRef, receipt.Agent.Image)
	assert.Equal(t, "standard", receipt.Agent.EgressProfile)
	assert.Equal(t, domainconfig.MCPModeEnv, receipt.Agent.MCPMode)
	require.NotNil(t, receipt.Write)
	assert.True(t, receipt.Write.Created)

	// doctor --json on the imported agent passes (the pinned ref is a valid ref).
	var dout bytes.Buffer
	dcmd := agentsCmd()
	dcmd.SetOut(&dout)
	dcmd.SetErr(&dout)
	dcmd.SetArgs([]string{"doctor", "aider", "--config", cfgPath, "--json"})
	require.NoError(t, dcmd.Execute())

	var dr agentReceipt
	require.NoError(t, json.Unmarshal(dout.Bytes(), &dr))
	assert.True(t, dr.OK)

	// The written config round-trips through the loader with the pinned image.
	loaded, err := infraconfig.NewLoader(cfgPath).Load()
	require.NoError(t, err)
	custom, ok := loaded.Agents["aider"]
	require.True(t, ok)
	assert.Equal(t, fakePinnedRef, custom.Image)
	assert.Equal(t, []string{"aider"}, custom.Command)
	assert.Equal(t, []string{"OPENAI_API_KEY"}, custom.EnvForward)
	require.NotNil(t, custom.MCP)
	assert.Equal(t, domainconfig.MCPModeEnv, custom.MCP.Mode)
}

func TestAgentsImportImageWarnsOnDeclaredImageMismatch(t *testing.T) {
	t.Parallel()

	// The embedded manifest declares a DIFFERENT repository than the imported
	// ref — this is a real mismatch and must warn. (A same-repo tag→digest pin
	// is silent, exercised by TestAgentsImportImageSameRepoNoWarning.)
	embeddedYAML := `name: aider
image: ghcr.io/acme/other-bbox:latest
command: ["aider"]
egress_profile: permissive
`
	fetcher := &fakeFetcher{
		manifestBytes: []byte(embeddedYAML),
		pinnedRef:     fakePinnedRef,
	}

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	imageRef := "ghcr.io/acme/aider-bbox:latest"

	var out, errBuf bytes.Buffer
	cmd := agentsImportCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"import", imageRef, "--config", cfgPath})
	require.NoError(t, runAgentsImport(cmd, imageRef, cfgPath, "", false, false, fetcher))

	// The warning names the declared image and the overriding pinned ref.
	warning := errBuf.String()
	assert.Contains(t, warning, "Warning: image manifest declares image")
	assert.Contains(t, warning, "ghcr.io/acme/other-bbox:latest")
	assert.Contains(t, warning, fakePinnedRef)

	// The persisted image is the pinned ref, not the declared tag.
	loaded, err := infraconfig.NewLoader(cfgPath).Load()
	require.NoError(t, err)
	assert.Equal(t, fakePinnedRef, loaded.Agents["aider"].Image)
}

func TestAgentsImportImageSameRepoNoWarning(t *testing.T) {
	t.Parallel()

	// The embedded manifest declares the same repository as the imported ref
	// (only the tag differs). Pinning the tag to a digest is the normal case —
	// no warning should be emitted.
	fetcher := &fakeFetcher{
		manifestBytes: []byte(imageManifestYAML),
		pinnedRef:     fakePinnedRef,
	}

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	imageRef := "ghcr.io/acme/aider-bbox:latest"

	var out, errBuf bytes.Buffer
	cmd := agentsImportCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"import", imageRef, "--config", cfgPath, "--json"})
	require.NoError(t, runAgentsImport(cmd, imageRef, cfgPath, "", false, true, fetcher))

	// No mismatch warning — the declared tag and pinned digest share a repo.
	assert.NotContains(t, errBuf.String(), "Warning: image manifest declares image")
}

func TestAgentsImportImageMalformedDeclaredImageWarns(t *testing.T) {
	t.Parallel()

	// The embedded manifest declares a malformed image ref — the import path
	// cannot compare repositories, so it warns that the declared image is
	// malformed.
	embeddedYAML := `name: aider
image: "not a valid ref with spaces"
command: ["aider"]
egress_profile: permissive
`
	fetcher := &fakeFetcher{
		manifestBytes: []byte(embeddedYAML),
		pinnedRef:     fakePinnedRef,
	}

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	imageRef := "ghcr.io/acme/aider-bbox:latest"

	var out, errBuf bytes.Buffer
	cmd := agentsImportCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"import", imageRef, "--config", cfgPath})
	require.NoError(t, runAgentsImport(cmd, imageRef, cfgPath, "", false, false, fetcher))

	assert.Contains(t, errBuf.String(), "malformed image")

	loaded, err := infraconfig.NewLoader(cfgPath).Load()
	require.NoError(t, err)
	assert.Equal(t, fakePinnedRef, loaded.Agents["aider"].Image)
}

func TestAgentsImportImageNameOverride(t *testing.T) {
	t.Parallel()

	fetcher := &fakeFetcher{
		manifestBytes: []byte(imageManifestYAML),
		pinnedRef:     fakePinnedRef,
	}

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	imageRef := "ghcr.io/acme/aider-bbox:latest"

	var out, errBuf bytes.Buffer
	cmd := agentsImportCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"import", imageRef, "--config", cfgPath, "--json", "--name", "aider2"})
	require.NoError(t, runAgentsImport(cmd, imageRef, cfgPath, "aider2", false, true, fetcher))

	var receipt agentReceipt
	require.NoError(t, json.Unmarshal(out.Bytes(), &receipt))
	assert.Equal(t, "aider2", receipt.Agent.Name)

	loaded, err := infraconfig.NewLoader(cfgPath).Load()
	require.NoError(t, err)
	_, ok := loaded.Agents["aider2"]
	assert.True(t, ok)
	_, originalOk := loaded.Agents["aider"]
	assert.False(t, originalOk, "the --name override renamed the agent, not duplicated it")
}

func TestAgentsImportImageRefusesBuiltinWithoutForce(t *testing.T) {
	t.Parallel()

	// The embedded manifest names a built-in; without --force this is rejected
	// before any config is written.
	fetcher := &fakeFetcher{
		manifestBytes: []byte(`name: claude-code
image: ghcr.io/acme/x:latest
command: ["x"]
egress_profile: permissive
`),
		pinnedRef: "ghcr.io/acme/x@sha256:1111111111111111111111111111111111111111111111111111111111111111",
	}

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	imageRef := "ghcr.io/acme/x:latest"

	cmd := agentsImportCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"import", imageRef, "--config", cfgPath})
	err := runAgentsImport(cmd, imageRef, cfgPath, "", false, false, fetcher)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "built-in agent")
	_, statErr := os.Stat(cfgPath)
	assert.True(t, os.IsNotExist(statErr))
}

func TestAgentsImportImageRejectsMalformedManifest(t *testing.T) {
	t.Parallel()

	fetcher := &fakeFetcher{
		manifestBytes: []byte("name: : not yaml {"),
		pinnedRef:     fakePinnedRef,
	}

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	imageRef := "ghcr.io/acme/aider-bbox:latest"

	cmd := agentsImportCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"import", imageRef, "--config", cfgPath})
	err := runAgentsImport(cmd, imageRef, cfgPath, "", false, false, fetcher)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing manifest from image")
}

func TestAgentsImportImageFetcherErrorPropagation(t *testing.T) {
	t.Parallel()

	// The fetcher returns an error; the CLI surfaces it wrapped with the image
	// ref and the fetcher's error message.
	fetchErr := fmt.Errorf("boom: connection refused")
	fetcher := &fakeFetcher{
		err: fetchErr,
	}

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	imageRef := "ghcr.io/acme/aider-bbox:latest"

	cmd := agentsImportCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"import", imageRef, "--config", cfgPath})
	err := runAgentsImport(cmd, imageRef, cfgPath, "", false, false, fetcher)
	require.Error(t, err)
	assert.Contains(t, err.Error(), imageRef)
	assert.Contains(t, err.Error(), "connection refused")

	// No config written on fetch failure.
	_, statErr := os.Stat(cfgPath)
	assert.True(t, os.IsNotExist(statErr))
}

func TestAgentsImportImageNameOverrideToBuiltin(t *testing.T) {
	t.Parallel()

	// --name overriding to a built-in name is rejected even though the
	// embedded manifest's name is a custom one.
	fetcher := &fakeFetcher{
		manifestBytes: []byte(imageManifestYAML),
		pinnedRef:     fakePinnedRef,
	}

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	imageRef := "ghcr.io/acme/aider-bbox:latest"

	cmd := agentsImportCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"import", imageRef, "--config", cfgPath, "--name", "claude-code"})
	err := runAgentsImport(cmd, imageRef, cfgPath, "claude-code", false, false, fetcher)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "built-in agent")

	_, statErr := os.Stat(cfgPath)
	assert.True(t, os.IsNotExist(statErr))
}

func TestAgentsImportImageCollidesWithExistingCustom(t *testing.T) {
	t.Parallel()

	fetcher := &fakeFetcher{
		manifestBytes: []byte(imageManifestYAML),
		pinnedRef:     fakePinnedRef,
	}

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	imageRef := "ghcr.io/acme/aider-bbox:latest"

	// First import succeeds.
	cmd1 := agentsImportCmd()
	cmd1.SetOut(&bytes.Buffer{})
	cmd1.SetErr(&bytes.Buffer{})
	cmd1.SetArgs([]string{"import", imageRef, "--config", cfgPath})
	require.NoError(t, runAgentsImport(cmd1, imageRef, cfgPath, "", false, false, fetcher))

	// Second import without --force fails with "already exists".
	cmd2 := agentsImportCmd()
	cmd2.SetOut(&bytes.Buffer{})
	cmd2.SetErr(&bytes.Buffer{})
	cmd2.SetArgs([]string{"import", imageRef, "--config", cfgPath})
	err := runAgentsImport(cmd2, imageRef, cfgPath, "", false, false, fetcher)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")

	// Second import with --force succeeds.
	cmd3 := agentsImportCmd()
	cmd3.SetOut(&bytes.Buffer{})
	cmd3.SetErr(&bytes.Buffer{})
	cmd3.SetArgs([]string{"import", imageRef, "--config", cfgPath})
	require.NoError(t, runAgentsImport(cmd3, imageRef, cfgPath, "", true, false, fetcher))
}

func TestAgentsImportImageNoImageField(t *testing.T) {
	t.Parallel()

	// An image manifest with no `image` field: import succeeds, no warning is
	// printed, and the config holds the digest-pinned ref from the import.
	embeddedYAML := `name: aider
command: ["aider"]
egress_profile: permissive
`
	fetcher := &fakeFetcher{
		manifestBytes: []byte(embeddedYAML),
		pinnedRef:     fakePinnedRef,
	}

	cfgPath := filepath.Join(t.TempDir(), "config.yaml")
	imageRef := "ghcr.io/acme/aider-bbox:latest"

	var out, errBuf bytes.Buffer
	cmd := agentsImportCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"import", imageRef, "--config", cfgPath, "--json"})
	require.NoError(t, runAgentsImport(cmd, imageRef, cfgPath, "", false, true, fetcher))

	// No warning when the declared image is absent.
	assert.NotContains(t, errBuf.String(), "Warning:")

	var receipt agentReceipt
	require.NoError(t, json.Unmarshal(out.Bytes(), &receipt))
	assert.True(t, receipt.OK)
	assert.Equal(t, fakePinnedRef, receipt.Agent.Image)

	// The persisted image is the digest-pinned ref.
	loaded, err := infraconfig.NewLoader(cfgPath).Load()
	require.NoError(t, err)
	assert.Equal(t, fakePinnedRef, loaded.Agents["aider"].Image)
}

func TestIsImageRef(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	existingFile := filepath.Join(dir, "aider.yaml")
	require.NoError(t, os.WriteFile(existingFile, []byte("name: x\n"), 0o600))

	tests := []struct {
		name   string
		source string
		want   bool
	}{
		{
			name:   "existing local file is not an image",
			source: existingFile,
			want:   false,
		},
		{
			name:   "fully-qualified image ref is an image",
			source: "ghcr.io/acme/aider-bbox:latest",
			want:   true,
		},
		{
			name:   "image ref by digest is an image",
			source: "ghcr.io/acme/aider-bbox@sha256:0000000000000000000000000000000000000000000000000000000000000000",
			want:   true,
		},
		{
			name:   "nonexistent path that does not parse as a ref falls through to file",
			source: filepath.Join(dir, "does-not-exist-and-not-a-ref.yaml"),
			want:   false,
		},
		{
			name:   "docker hub short ref with no slash is treated as a file",
			source: "ubuntu:24.04",
			want:   false,
		},
		{
			name:   "fully-qualified docker hub library ref is an image",
			source: "docker.io/library/ubuntu:24.04",
			want:   true,
		},
		{
			name:   "misspelled bare manifest path is not an image",
			source: "aider.yaml",
			want:   false,
		},
		{
			name:   "misspelled bare relative manifest path is not an image",
			source: "./aider.yaml",
			want:   false,
		},
		{
			name:   "bare name with no extension is not an image",
			source: "aider",
			want:   false,
		},
		{
			name:   "image ref by digest with slash is an image",
			source: "ghcr.io/acme/x@sha256:0000000000000000000000000000000000000000000000000000000000000000",
			want:   true,
		},
		{
			name:   "parent-dir relative path is not an image",
			source: "../aider.yaml",
			want:   false,
		},
		{
			name:   "absolute path is not an image even if it parses as a ref",
			source: "/nonexistent/absolute/path.yaml",
			want:   false,
		},
		{
			name:   "subdirectory manifest path is not an image",
			source: "manifests/aider.yaml",
			want:   false,
		},
		{
			name:   "another subdirectory manifest path is not an image",
			source: "subdir/foo.yaml",
			want:   false,
		},
		{
			name:   "localhost with port and repo is an image",
			source: "localhost:5001/mecatui:latest",
			want:   true,
		},
		{
			name:   "ghcr.io ref is an image",
			source: "ghcr.io/acme/x:latest",
			want:   true,
		},
		{
			name:   "docker.io fully-qualified ref is an image",
			source: "docker.io/library/ubuntu:24.04",
			want:   true,
		},
		// A permission-denied stat error (non-ErrNotExist) is intentionally not
		// tested here: it is filesystem- and privilege-dependent (root bypasses
		// it, some CI runners run as root, some filesystems don't honor 0o000),
		// making a hermetic, non-flaky subtest impractical. The branch is covered
		// by inspection of isImageRef: any non-ErrNotExist stat error returns
		// false and falls through to the file path.
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isImageRef(tc.source))
		})
	}
}
