# SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
# SPDX-License-Identifier: Apache-2.0

# Gemini CLI guest image for Brood Box.
# Installs Google's @google/gemini-cli (Node/Ink-based) on top of the base
# image, which already provides nodejs and npm from the Wolfi repo.
ARG BASE_IMAGE=ghcr.io/stacklok/brood-box/base:latest
FROM ${BASE_IMAGE}

# Install Gemini CLI globally. Requires Node.js 20+; the Wolfi base
# `nodejs` apk satisfies this as of 2026. If a future Wolfi default
# regresses below 20, pin a versioned package (e.g. nodejs-22) here.
RUN npm install -g --omit=dev @google/gemini-cli && \
    npm cache clean --force
