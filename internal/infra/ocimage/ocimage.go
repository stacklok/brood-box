// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package ocimage reads a broodbox agent manifest embedded in an OCI image.
//
// A self-describing agent image carries its agent.yaml either at a path named
// by the org.stacklok.broodbox.agent config label, or at the well-known path
// /usr/share/broodbox/agent.yaml when the label is absent. FetchAgentManifest
// pulls just enough of the image (config + the one layer holding the manifest)
// to read those bytes and resolve the digest the ref pinned to, so
// `bbox agents import IMAGE` can register a runnable agent with no local YAML
// authoring.
package ocimage

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/partial"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"

	"github.com/stacklok/brood-box/internal/infra/configfile"
)

const (
	// LabelAgent is the OCI config label naming the in-image path to the
	// broodbox agent manifest. When present, its value overrides the
	// well-known path.
	LabelAgent = "org.stacklok.broodbox.agent"

	// WellKnownManifestPath is the fallback location of the agent manifest
	// inside an image that does not set LabelAgent.
	WellKnownManifestPath = "/usr/share/broodbox/agent.yaml"
)

// Fetcher reads a broodbox agent manifest from an OCI image.
type Fetcher interface {
	// FetchAgentManifest pulls just enough of the image to read the embedded
	// broodbox agent manifest. It returns the raw manifest bytes and the
	// digest-pinned reference (repo@sha256:...) the source ref resolved to,
	// for pinning the agent's image field at import time.
	FetchAgentManifest(ctx context.Context, ref string) (manifest []byte, pinnedRef string, err error)
}

// RemoteFetcher pulls manifests from a remote registry via go-containerregistry.
// The zero value is usable but pulls anonymously; use NewRemoteFetcher to pick up
// host credentials from the default keychain (docker login/podman login). Remote
// options (auth, platform) may be supplied via NewRemoteFetcher, which stores
// them in the unexported remoteOptions field.
type RemoteFetcher struct {
	// remoteOptions are passed to remote.Image (auth, transport, platform
	// selection). nil/empty uses anonymous access and the host platform.
	remoteOptions []remote.Option
}

// NewRemoteFetcher returns a RemoteFetcher that pulls with the given
// remote.Options appended after the default keychain, so `docker login`/`podman
// login` credentials are picked up automatically. Callers that need fully
// custom auth can pass a remote.WithAuth option, which overrides the keychain
// for that request.
func NewRemoteFetcher(opts ...remote.Option) *RemoteFetcher {
	return &RemoteFetcher{remoteOptions: append([]remote.Option{remote.WithAuthFromKeychain(authn.DefaultKeychain)}, opts...)}
}

// FetchAgentManifest resolves ref, pulls the image config + the layer holding
// the manifest, and returns the manifest bytes plus the digest-pinned ref. The
// pull is bounded by a 60s timeout and uses the configured keychain for auth.
func (f *RemoteFetcher) FetchAgentManifest(ctx context.Context, ref string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	parsed, err := name.ParseReference(ref)
	if err != nil {
		return nil, "", fmt.Errorf("parsing image reference %q: %w", ref, err)
	}

	opts := append([]remote.Option{}, f.remoteOptions...)
	opts = append(opts, remote.WithContext(ctx))
	img, err := remote.Image(parsed, opts...)
	if err != nil {
		return nil, "", fmt.Errorf("importing agent from image %q: %w", ref, enrichRemoteError(err))
	}

	manifestBytes, err := extractFromImage(img, LabelAgent, WellKnownManifestPath)
	if err != nil {
		return nil, "", fmt.Errorf("importing agent from image %q: %w", ref, err)
	}

	pinned, err := pinnedDigestRef(parsed, img)
	if err != nil {
		return nil, "", fmt.Errorf("importing agent from image %q: resolving digest: %w", ref, err)
	}
	return manifestBytes, pinned, nil
}

// pinnedDigestRef returns the digest-pinned reference (repo@sha256:...) for the
// image the parsed ref resolved to. The repository is taken from the parsed
// ref's Context() (fully-qualified, no tag/digest); the digest comes from the
// image's manifest digest. Repository.Digest returns a name.Digest whose Name()
// is the canonical pinned ref.
func pinnedDigestRef(parsed name.Reference, img v1.Image) (string, error) {
	digest, err := img.Digest()
	if err != nil {
		return "", fmt.Errorf("computing image digest: %w", err)
	}
	return parsed.Context().Digest(digest.String()).Name(), nil
}

// extractFromImage reads the broodbox agent manifest from img. The manifest
// path is the value of the LabelAgent config label when set, otherwise
// WellKnownManifestPath (defaultPath). Layers are walked topmost-first; the
// first layer containing the target path wins (later layers shadow earlier
// ones, matching how a container's filesystem is assembled). It is pure-ish —
// it only reads the in-memory/passed v1.Image, so tests can feed a hand-built
// image built with crane.Layer + mutate.Append + mutate.ConfigFile.
func extractFromImage(img v1.Image, label, defaultPath string) ([]byte, error) {
	target := defaultPath
	if cfg, err := img.ConfigFile(); err == nil && cfg != nil {
		if p, ok := cfg.Config.Labels[label]; ok && strings.TrimSpace(p) != "" {
			target = p
		}
	}

	layers, err := img.Layers()
	if err != nil {
		return nil, fmt.Errorf("listing image layers: %w", err)
	}

	// Layers() returns base-first (oldest first). The topmost layer is last,
	// and shadows earlier layers for a given path — so walk newest-first and
	// return the first hit.
	for i := len(layers) - 1; i >= 0; i-- {
		data, found, err := readLayerPath(layers[i], target)
		if err != nil {
			return nil, fmt.Errorf("reading layer: %w", err)
		}
		if found {
			return data, nil
		}
	}

	return nil, fmt.Errorf(
		"image does not contain a broodbox agent manifest (label %q missing and well-known path %q not found in any layer)",
		label, target)
}

// readLayerPath scans a single uncompressed layer's tar for an entry at path
// and returns its bytes. found is false (with nil error) when the layer does
// not contain the path.
func readLayerPath(layer v1.Layer, path string) (data []byte, found bool, err error) {
	// Reject foreign-layer URLs (OCI "foreign" blobs) before any I/O. A remote
	// layer whose descriptor carries a `urls` field would otherwise cause
	// go-containerregistry's Uncompressed()/Compressed() to fetch those
	// attacker-controlled URLs on the host, bypassing the sandbox VM egress
	// policy (DNS-rebinding to cloud metadata is possible). partial.Descriptor
	// returns the original manifest descriptor (with URLs) for remote layers
	// and computes one for in-memory layers (no URLs); if it errors or returns
	// nil, proceed normally.
	if desc, derr := partial.Descriptor(layer); derr == nil && desc != nil && len(desc.URLs) > 0 {
		label := path
		if desc.Digest.String() != "" {
			label = desc.Digest.String()
		}
		return nil, false, fmt.Errorf("foreign-layer URLs are not permitted for the agent manifest (layer %s declares %d external URL(s))", label, len(desc.URLs))
	}

	rc, err := layer.Uncompressed()
	if err != nil {
		return nil, false, fmt.Errorf("opening uncompressed layer: %w", err)
	}
	defer func() { _ = rc.Close() }()

	target := cleanTarPath(path)
	tr := tar.NewReader(rc)
	for {
		hdr, err := tr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, false, nil
			}
			return nil, false, fmt.Errorf("reading tar entry: %w", err)
		}
		if cleanTarPath(hdr.Name) != target {
			continue
		}
		// A directory entry (or other non-regular type) at the target path is
		// not the manifest — skip it so a later layer or the not-found error
		// surfaces.
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		buf, err := io.ReadAll(io.LimitReader(tr, configfile.MaxSize))
		if err != nil {
			return nil, false, fmt.Errorf("reading %q from layer: %w", path, err)
		}
		return buf, true, nil
	}
}

// cleanTarPath normalizes a tar entry name or a target manifest path for
// comparison. docker build/podman build commonly emit entries with a leading
// "./" (e.g. "./usr/share/broodbox/agent.yaml"), and the target path may be
// absolute (e.g. "/usr/share/broodbox/agent.yaml"). Stripping a leading "/" and
// a leading "./" from both sides makes the two forms comparable.
func cleanTarPath(s string) string {
	s = strings.TrimPrefix(s, "/")
	s = strings.TrimPrefix(s, "./")
	return s
}

// enrichRemoteError appends a registry hint when the error is an HTTP 401/403/404
// from the registry, so the operator gets an actionable message.
func enrichRemoteError(err error) error {
	var terr *transport.Error
	if errors.As(err, &terr) {
		switch terr.StatusCode {
		case 401, 403:
			return fmt.Errorf("%w (registry returned %d — authenticate, e.g. `docker login`/`podman login` to the registry)", err, terr.StatusCode)
		case 404:
			return fmt.Errorf("%w (image or tag not found; confirm the reference and that you have access)", err)
		}
	}
	return err
}
