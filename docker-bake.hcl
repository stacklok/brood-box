# SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
# SPDX-License-Identifier: Apache-2.0

variable "REGISTRY" {
  default = "ghcr.io/stacklok/brood-box"
}

group "default" {
  targets = ["base", "claude-code", "codex", "opencode"]
}

target "base" {
  context    = "images/base/"
  platforms  = ["linux/amd64", "linux/arm64"]
  tags       = ["${REGISTRY}/base:latest"]
  cache-from = ["type=gha,scope=base"]
  cache-to   = ["type=gha,mode=max,scope=base"]
}

target "claude-code" {
  context    = "images/claude-code/"
  platforms  = ["linux/amd64", "linux/arm64"]
  tags       = ["${REGISTRY}/claude-code:latest"]
  cache-from = ["type=gha,scope=claude-code"]
  cache-to   = ["type=gha,mode=max,scope=claude-code"]
  args = {
    BASE_IMAGE = "brood-box-base"
  }
  contexts = {
    "brood-box-base" = "target:base"
  }
}

target "codex" {
  context    = "images/codex/"
  platforms  = ["linux/amd64", "linux/arm64"]
  tags       = ["${REGISTRY}/codex:latest"]
  cache-from = ["type=gha,scope=codex"]
  cache-to   = ["type=gha,mode=max,scope=codex"]
  args = {
    BASE_IMAGE = "brood-box-base"
  }
  contexts = {
    "brood-box-base" = "target:base"
  }
}

target "opencode" {
  context    = "images/opencode/"
  platforms  = ["linux/amd64", "linux/arm64"]
  tags       = ["${REGISTRY}/opencode:latest"]
  cache-from = ["type=gha,scope=opencode"]
  cache-to   = ["type=gha,mode=max,scope=opencode"]
  args = {
    BASE_IMAGE = "brood-box-base"
  }
  contexts = {
    "brood-box-base" = "target:base"
  }
}
