// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package ocimage

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validManifestYAML is a minimal, schema-valid agent manifest used as the
// embedded file content in the in-memory test images. extractFromImage only
// reads bytes; it does not parse the manifest, so the content is exercised by
// the e2e tests in cmd/bbox, but it must be non-empty and shaped like a real
// manifest.
const validManifestYAML = `name: aider
image: ghcr.io/acme/aider-bbox:latest
command: ["aider"]
egress_profile: permissive
`

// buildImage builds an in-memory v1.Image with the given file contents across
// the given layers (each map is appended as one layer, in order — first map is
// the base, last is topmost) and the given config labels. No network is used.
func buildImage(t *testing.T, layers []map[string][]byte, labels map[string]string) v1.Image {
	t.Helper()
	img := empty.Image
	for _, files := range layers {
		layer, err := crane.Layer(files)
		require.NoError(t, err)
		img, err = mutate.AppendLayers(img, layer)
		require.NoError(t, err)
	}
	if labels != nil {
		cf, err := img.ConfigFile()
		require.NoError(t, err)
		cfg := cf.DeepCopy()
		if cfg.Config.Labels == nil {
			cfg.Config.Labels = map[string]string{}
		}
		for k, v := range labels {
			cfg.Config.Labels[k] = v
		}
		img, err = mutate.ConfigFile(img, cfg)
		require.NoError(t, err)
	}
	return img
}

func TestExtractFromImage(t *testing.T) {
	t.Parallel()

	customPath := "/etc/aider/agent.yaml"
	topmost := "name: topmost\nimage: ghcr.io/acme/topmost:latest\ncommand: [\"t\"]\negress_profile: permissive\n"
	bad := "name: : not yaml {"

	tests := []struct {
		name        string
		layers      []map[string][]byte
		labels      map[string]string
		wantData    string
		wantErr     bool
		errContains []string
	}{
		{
			name: "LabelPointsToPath",
			layers: []map[string][]byte{
				{customPath: []byte(validManifestYAML)},
			},
			labels:   map[string]string{LabelAgent: customPath},
			wantData: validManifestYAML,
		},
		{
			name: "LabelAbsentFallsBackToWellKnown",
			layers: []map[string][]byte{
				{WellKnownManifestPath: []byte(validManifestYAML)},
			},
			wantData: validManifestYAML,
		},
		{
			name: "NeitherLabelNorWellKnown",
			layers: []map[string][]byte{
				{"/usr/bin/aider": []byte("binary")},
			},
			wantErr:     true,
			errContains: []string{"does not contain a broodbox agent manifest", LabelAgent, WellKnownManifestPath},
		},
		{
			name: "ManifestInNonTopmostLayer",
			layers: []map[string][]byte{
				{WellKnownManifestPath: []byte(validManifestYAML)},
				{"/usr/bin/aider": []byte("binary")},
			},
			wantData: validManifestYAML,
		},
		{
			name: "TopmostLayerShadowsBase",
			layers: []map[string][]byte{
				{WellKnownManifestPath: []byte(validManifestYAML)},
				{WellKnownManifestPath: []byte(topmost)},
			},
			wantData: topmost,
		},
		{
			name: "MalformedManifestBytesSurface",
			layers: []map[string][]byte{
				{WellKnownManifestPath: []byte(bad)},
			},
			wantData: bad,
		},
		{
			name: "EmptyLabelValueIgnored",
			layers: []map[string][]byte{
				{WellKnownManifestPath: []byte(validManifestYAML)},
			},
			labels:   map[string]string{LabelAgent: "  "},
			wantData: validManifestYAML,
		},
		{
			name: "LabelPointsToMissingFile",
			layers: []map[string][]byte{
				{WellKnownManifestPath: []byte(validManifestYAML)},
			},
			labels:      map[string]string{LabelAgent: customPath},
			wantErr:     true,
			errContains: []string{customPath},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			img := buildImage(t, tc.layers, tc.labels)

			data, err := extractFromImage(img, LabelAgent, WellKnownManifestPath)
			if tc.wantErr {
				require.Error(t, err)
				for _, s := range tc.errContains {
					assert.Contains(t, err.Error(), s)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantData, string(data))
		})
	}
}

func TestPinnedDigestRef_Normalizes(t *testing.T) {
	t.Parallel()

	// Build an image and resolve its digest-pinned ref from a tag ref. We
	// cannot use a real registry, but we can use a fully-qualified repo name
	// (ghcr.io/acme/aider-bbox) and assert the pinned ref parses as a Digest
	// and round-trips the repository.
	img := buildImage(t, []map[string][]byte{
		{WellKnownManifestPath: []byte(validManifestYAML)},
	}, nil)

	parsed, err := name.ParseReference("ghcr.io/acme/aider-bbox:latest")
	require.NoError(t, err)
	pinned, err := pinnedDigestRef(parsed, img)
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(pinned, "ghcr.io/acme/aider-bbox@sha256:"))
	assert.NotContains(t, pinned, ":latest")

	// name.NewDigest accepts the result (validates algorithm + hex).
	dig, err := name.NewDigest(pinned)
	require.NoError(t, err)
	assert.Equal(t, "ghcr.io/acme/aider-bbox", dig.Repository.Name())
}

func TestEnrichRemoteError(t *testing.T) {
	t.Parallel()

	base := errors.New("some failure")
	tests := []struct {
		name       string
		err        error
		wantSubstr string
	}{
		{
			name:       "401 surfaces authenticate hint",
			err:        &transport.Error{StatusCode: 401},
			wantSubstr: "authenticate",
		},
		{
			name:       "403 surfaces authenticate hint",
			err:        &transport.Error{StatusCode: 403},
			wantSubstr: "authenticate",
		},
		{
			name:       "404 surfaces not-found hint",
			err:        &transport.Error{StatusCode: 404},
			wantSubstr: "not found",
		},
		{
			name:       "non-transport error is unchanged",
			err:        base,
			wantSubstr: "some failure",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := enrichRemoteError(tc.err)
			require.Error(t, got)
			assert.Contains(t, got.Error(), tc.wantSubstr)
		})
	}
}
