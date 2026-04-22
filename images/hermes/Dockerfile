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

# Extra runtime deps the curl|bash installer would otherwise pull in.
# The base already ships python3, py3-pip, py3-virtualenv, nodejs, npm,
# ripgrep, git, and curl — so we only need ffmpeg here (voice/TTS audio
# conversion; harmless when those features are disabled).
RUN apk add --no-cache ffmpeg

# Clone a pinned tag instead of running `curl|bash`: deterministic builds,
# no surprise re-execution, no PATH mutation at image-build time.
RUN git clone --depth 1 --branch "${HERMES_VERSION}" \
    https://github.com/NousResearch/hermes-agent.git /opt/hermes/src && \
    rm -rf /opt/hermes/src/.git

# Create an isolated venv and install the minimal feature set: core CLI,
# MCP client (for the brood-box vmcp sandbox-tools endpoint), and ACP
# (Agent Client Protocol — editor integrations). Heavy optional extras
# (messaging, slack, matrix, voice, rl, bedrock, mistral, ...) can be
# layered on top of this image if a user needs them.
RUN python3 -m venv /opt/hermes/venv && \
    /opt/hermes/venv/bin/pip install --no-cache-dir --upgrade pip && \
    /opt/hermes/venv/bin/pip install --no-cache-dir "/opt/hermes/src[mcp,acp]"

# Expose the entry points on PATH for the sandbox user. Both `hermes` and
# `hermes-agent` scripts land in the venv bin dir via pyproject's
# [project.scripts]; link the primary CLI for user invocation.
RUN ln -s /opt/hermes/venv/bin/hermes /usr/local/bin/hermes && \
    ln -s /opt/hermes/venv/bin/hermes-agent /usr/local/bin/hermes-agent
