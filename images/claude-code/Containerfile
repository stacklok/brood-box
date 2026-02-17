# SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
# SPDX-License-Identifier: Apache-2.0

# Claude Code guest image for apiary.
# Installs the Claude Code native binary (Bun-based) on top of the base image.
FROM ghcr.io/stacklok/apiary/base:latest

# Install Claude Code native binary.
# The installer creates a symlink at /root/.local/bin/claude pointing to the
# versioned binary under /root/.local/share/claude/versions/. We must copy
# the real binary (not the symlink) so it works for the non-root sandbox user.
RUN export PATH="$HOME/.local/bin:$PATH" && \
    curl -fsSL https://claude.ai/install.sh | bash && \
    cp -L /root/.local/bin/claude /usr/local/bin/claude && \
    rm -rf /root/.local/share/claude /root/.local/bin/claude

# Set sandbox env vars that should be baked into the image
ENV IS_SANDBOX=1
ENV DISABLE_AUTOUPDATER=1
ENV DISABLE_TELEMETRY=1
ENV DISABLE_ERROR_REPORTING=1
