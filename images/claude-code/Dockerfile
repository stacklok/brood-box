# SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
# SPDX-License-Identifier: Apache-2.0

# Claude Code guest image for Brood Box.
# Installs the Claude Code native binary (Bun-based) on top of the base image.
ARG BASE_IMAGE=ghcr.io/stacklok/brood-box/base:latest
FROM ${BASE_IMAGE}

# Install Claude Code native binary.
# The installer creates a symlink at /root/.local/bin/claude pointing to the
# versioned binary under /root/.local/share/claude/versions/. We copy the real
# binary to /usr/local/bin so it works for the non-root sandbox user, and also
# symlink it to /home/sandbox/.local/bin/claude — the path the native installer
# records as installMethod "native". Without this symlink Claude Code emits:
#   "installMethod is native, but claude command not found at
#    /home/sandbox/.local/bin/claude"
RUN export PATH="$HOME/.local/bin:$PATH" && \
    curl -fsSL https://claude.ai/install.sh | bash && \
    cp -L /root/.local/bin/claude /usr/local/bin/claude && \
    rm -rf /root/.local/share/claude /root/.local/bin/claude && \
    ln -s /usr/local/bin/claude /home/sandbox/.local/bin/claude

# Set sandbox env vars that should be baked into the image
ENV IS_SANDBOX=1
ENV DISABLE_TELEMETRY=1
ENV DISABLE_ERROR_REPORTING=1
