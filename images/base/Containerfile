# SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
# SPDX-License-Identifier: Apache-2.0

# Base image for apiary guest VMs.
# Networking, mounts, and SSH are handled by the embedded apiary-init binary.
# This image only needs: bash, git, and common CLI tools for agents.
FROM cgr.dev/chainguard/wolfi-base:latest

RUN apk add --no-cache \
    bash \
    git \
    ca-certificates-bundle \
    coreutils \
    findutils \
    grep \
    sed \
    gawk \
    curl

# Ensure /usr/local/bin exists (not present in Wolfi by default)
RUN mkdir -p /usr/local/bin

# Create non-root sandbox user for SSH sessions
RUN adduser -D -u 1000 -h /home/sandbox -s /bin/bash sandbox && \
    mkdir -p /home/sandbox/.ssh /home/sandbox/.local/bin && \
    chmod 700 /home/sandbox/.ssh && \
    chown -R sandbox:sandbox /home/sandbox/.ssh /home/sandbox/.local && \
    echo "/bin/bash" >> /etc/shells
