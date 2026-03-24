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
    ripgrep \
    sed \
    gawk \
    curl \
    openssh-client \
    make \
    gcc \
    glibc-dev \
    go \
    python3 \
    py3-pip \
    py3-virtualenv \
    nodejs \
    npm \
    rust

# Put Go and user-local binaries on PATH for all shells.
# NOTE: Docker ENV PATH is NOT enough — Wolfi's /etc/profile rebuilds PATH
# from scratch, and the SSH session sources /etc/profile. We must use a
# profile.d drop-in so the paths survive into the agent's shell.
ENV PATH="/usr/lib/go/bin:/home/sandbox/go/bin:/home/sandbox/.local/bin:${PATH}"
RUN printf '%s\n' \
    'export PATH="/usr/lib/go/bin:/home/sandbox/go/bin:/home/sandbox/.local/bin:${PATH}"' \
    > /etc/profile.d/broodbox-path.sh

# Ensure /usr/local/bin exists (not present in Wolfi by default)
RUN mkdir -p /usr/local/bin

# Install go-task from GitHub releases (not packaged in Wolfi).
ARG TASK_VERSION=v3.48.0
RUN ARCH="$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')" && \
    curl -fsSL "https://github.com/go-task/task/releases/download/${TASK_VERSION}/task_linux_${ARCH}.tar.gz" | \
    tar -xz -C /usr/local/bin task

# Install golangci-lint from GitHub releases (not packaged in Wolfi).
ARG GOLANGCI_LINT_VERSION=v2.10.1
RUN ARCH="$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')" && \
    VER="${GOLANGCI_LINT_VERSION#v}" && \
    curl -fsSL "https://github.com/golangci/golangci-lint/releases/download/${GOLANGCI_LINT_VERSION}/golangci-lint-${VER}-linux-${ARCH}.tar.gz" | \
    tar -xz --strip-components=1 -C /usr/local/bin "golangci-lint-${VER}-linux-${ARCH}/golangci-lint"

# Install goimports via go install (no binary releases available).
RUN GOBIN=/usr/local/bin go install golang.org/x/tools/cmd/goimports@latest

# Create non-root sandbox user for SSH sessions
RUN adduser -D -u 1000 -h /home/sandbox -s /bin/bash sandbox && \
    mkdir -p /home/sandbox/.ssh /home/sandbox/.local/bin && \
    chmod 700 /home/sandbox/.ssh && \
    chown -R sandbox:sandbox /home/sandbox/.ssh /home/sandbox/.local && \
    echo "/bin/bash" >> /etc/shells
