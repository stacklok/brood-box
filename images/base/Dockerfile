# SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
# SPDX-License-Identifier: Apache-2.0

# Base image for Brood Box guest VMs.
# Batteries-included: ships all common toolchains so per-project flavour
# detection is unnecessary. Custom images can still be specified via
# agents.<name>.image in .broodbox.yaml.
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
    curl \
    openssh-client \
    make \
    go \
    python3 \
    py3-pip \
    py3-virtualenv \
    nodejs \
    npm \
    rust

# Ensure /usr/local/bin exists (not present in Wolfi by default)
RUN mkdir -p /usr/local/bin

# Install go-task from GitHub releases (not packaged in Wolfi).
ARG TASK_VERSION=v3.48.0
RUN ARCH="$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')" && \
    curl -fsSL "https://github.com/go-task/task/releases/download/${TASK_VERSION}/task_linux_${ARCH}.tar.gz" | \
    tar -xz -C /usr/local/bin task

# Create non-root sandbox user for SSH sessions
RUN adduser -D -u 1000 -h /home/sandbox -s /bin/bash sandbox && \
    mkdir -p /home/sandbox/.ssh /home/sandbox/.local/bin && \
    chmod 700 /home/sandbox/.ssh && \
    chown -R sandbox:sandbox /home/sandbox/.ssh /home/sandbox/.local && \
    echo "/bin/bash" >> /etc/shells
