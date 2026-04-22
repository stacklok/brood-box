# SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
# SPDX-License-Identifier: Apache-2.0

# Hermes Agent guest image for Brood Box.
# Installs the Nous Research Hermes Agent (Python CLI) from a pinned GitHub
# tag. Hermes is a client, not a model — it dispatches to third-party LLM
# providers (Anthropic, OpenAI, OpenRouter, ...) using a user-provided key.
ARG BASE_IMAGE=ghcr.io/stacklok/brood-box/base:latest
FROM ${BASE_IMAGE}

# Pin to a calendar-versioned Hermes Agent tag. Bump deliberately; releases
# are roughly weekly. Override with --build-arg HERMES_VERSION=... for dev.
ARG HERMES_VERSION=v2026.4.16

LABEL org.opencontainers.image.source="https://github.com/NousResearch/hermes-agent" \
      org.opencontainers.image.description="Hermes Agent guest image for Brood Box" \
      org.opencontainers.image.licenses="Apache-2.0"

# Clone a pinned tag (deterministic, no curl|bash), build an isolated venv
# with the minimal feature set — core CLI + MCP client (for the brood-box
# vmcp sandbox-tools endpoint) + ACP (Agent Client Protocol for editors).
# Heavy optional extras (messaging, slack, matrix, voice, rl, bedrock,
# mistral, ...) are intentionally excluded here; a future hermes-voice /
# hermes-messaging variant can layer them on.
#
# The source tree is deleted after `pip install` — installed package code
# lives in the venv's site-packages, so the clone is dead weight.
#
# Known drift: Hermes's ~100 transitive PyPI wheels are not hash-locked.
# Two rebuilds of the same HERMES_VERSION can produce byte-different
# images. Acceptable for a weekly-refresh agent guest; tighten with a
# constraints.txt if this becomes a problem.
RUN git clone --depth 1 --branch "${HERMES_VERSION}" \
        https://github.com/NousResearch/hermes-agent.git /opt/hermes/src && \
    python3 -m venv /opt/hermes/venv && \
    /opt/hermes/venv/bin/pip install --no-cache-dir "/opt/hermes/src[mcp,acp]" && \
    ln -s /opt/hermes/venv/bin/hermes /usr/local/bin/hermes && \
    rm -rf /opt/hermes/src
