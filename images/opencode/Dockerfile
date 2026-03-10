# SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
# SPDX-License-Identifier: Apache-2.0

# OpenCode guest image for Brood Box.
# Installs the OpenCode binary (anomalyco/opencode, Bun-based, musl variant).
ARG BASE_IMAGE=ghcr.io/stacklok/brood-box/base:latest
FROM ${BASE_IMAGE}

# Install OpenCode binary from GitHub Releases (glibc variant, matches Wolfi)
ARG TARGETARCH
RUN ARCH=$([ "$TARGETARCH" = "arm64" ] && echo "arm64" || echo "x64") && \
    curl -fsSL -o /tmp/opencode.tar.gz \
    "https://github.com/anomalyco/opencode/releases/latest/download/opencode-linux-${ARCH}.tar.gz" && \
    tar -xzf /tmp/opencode.tar.gz -C /usr/local/bin/ && \
    rm /tmp/opencode.tar.gz && \
    chmod +x /usr/local/bin/opencode
