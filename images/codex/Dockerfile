# SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
# SPDX-License-Identifier: Apache-2.0

# Codex guest image for Brood Box.
# Installs the Codex static binary (musl-linked) on top of the base image.
ARG BASE_IMAGE=ghcr.io/stacklok/brood-box/base:latest
FROM ${BASE_IMAGE}

# Install Codex static binary from GitHub Releases
ARG TARGETARCH
RUN ARCH=$([ "$TARGETARCH" = "arm64" ] && echo "aarch64" || echo "x86_64") && \
    curl -fsSL -o /tmp/codex.tar.gz \
    "https://github.com/openai/codex/releases/latest/download/codex-${ARCH}-unknown-linux-musl.tar.gz" && \
    tar -xzf /tmp/codex.tar.gz -C /tmp/ && \
    mv /tmp/codex-${ARCH}-unknown-linux-musl /usr/local/bin/codex && \
    rm /tmp/codex.tar.gz && \
    chmod +x /usr/local/bin/codex

# Allow running without Landlock sandbox (the VM IS the sandbox)
ENV CODEX_UNSAFE_ALLOW_NO_SANDBOX=1
