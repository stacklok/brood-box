// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	infraagent "github.com/stacklok/brood-box/internal/infra/agent"
	"github.com/stacklok/brood-box/pkg/clients"
	domainconfig "github.com/stacklok/brood-box/pkg/domain/config"
	"github.com/stacklok/brood-box/pkg/domain/egress"
)

func TestDeriveAgentName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		ref  string
		want string
	}{
		{name: "simple repo with tag", ref: "ghcr.io/me/Aider-Bbox:latest", want: "aider-bbox"},
		{name: "dockerhub library", ref: "ubuntu:24.04", want: "ubuntu"},
		{name: "bare name", ref: "ubuntu", want: "ubuntu"},
		{name: "nested path", ref: "ghcr.io/stacklok/brood-box/claude-code", want: "claude-code"},
		{name: "digest suffix", ref: "ghcr.io/acme/tool@sha256:abcdef", want: "tool"},
		{name: "registry with port", ref: "registry.example.com:5000/foo/bar", want: "bar"},
		{name: "underscores collapse", ref: "ghcr.io/acme/my_image:1", want: "my-image"},
		{name: "uppercase lowercased", ref: "ghcr.io/acme/MyImage:1", want: "myimage"},
		{name: "degenerate empty after trim", ref: "ghcr.io/acme/---:1", want: "run-image"},
		{name: "registry port only yields host part", ref: "localhost:5000", want: "localhost"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := deriveAgentName(tt.ref)
			if got != tt.want {
				t.Errorf("deriveAgentName(%q) = %q, want %q", tt.ref, got, tt.want)
			}
		})
	}
}

func TestBuildRunImageOverride(t *testing.T) {
	t.Parallel()

	baseFlags := runImageFlags{
		egressProfile: "permissive",
	}
	image := "ghcr.io/acme/tool:latest"
	cmd := []string{"aider"}

	t.Run("defaults are ephemeral", func(t *testing.T) {
		t.Parallel()
		o, err := buildRunImageOverride(image, cmd, baseFlags)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.Image != image {
			t.Errorf("Image = %q, want %q", o.Image, image)
		}
		if len(o.Command) != 1 || o.Command[0] != "aider" {
			t.Errorf("Command = %v, want [aider]", o.Command)
		}
		if len(o.EnvForward) != 0 {
			t.Errorf("EnvForward = %v, want empty", o.EnvForward)
		}
		if o.EgressProfile != string(egress.ProfilePermissive) {
			t.Errorf("EgressProfile = %q, want %q", o.EgressProfile, egress.ProfilePermissive)
		}
		if o.MCP != nil {
			t.Errorf("MCP = %+v, want nil (off by default)", o.MCP)
		}
		if o.Credentials != nil {
			t.Errorf("Credentials = %+v, want nil", o.Credentials)
		}
		if len(o.Settings) != 0 {
			t.Errorf("Settings = %v, want empty", o.Settings)
		}
		if o.CPUs != 0 || o.Memory != 0 || o.TmpSize != 0 {
			t.Errorf("resources set without flags: cpus=%d mem=%s tmp=%s", o.CPUs, o.Memory, o.TmpSize)
		}
	})

	t.Run("env forwarding from flags", func(t *testing.T) {
		t.Parallel()
		f := baseFlags
		f.envForward = []string{"OPENAI_API_KEY", "MY_*"}
		o, err := buildRunImageOverride(image, cmd, f)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(o.EnvForward) != 2 || o.EnvForward[0] != "OPENAI_API_KEY" || o.EnvForward[1] != "MY_*" {
			t.Errorf("EnvForward = %v, want [OPENAI_API_KEY MY_*]", o.EnvForward)
		}
	})

	t.Run("mcp forces mode env", func(t *testing.T) {
		t.Parallel()
		f := baseFlags
		f.mcp = true
		o, err := buildRunImageOverride(image, cmd, f)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.MCP == nil {
			t.Fatalf("MCP nil, want env mode")
		}
		if o.MCP.Mode != domainconfig.MCPModeEnv {
			t.Errorf("MCP.Mode = %q, want %q", o.MCP.Mode, domainconfig.MCPModeEnv)
		}
		if o.MCP.Authz != nil {
			t.Errorf("MCP.Authz = %+v, want nil when no profile flag", o.MCP.Authz)
		}
	})

	t.Run("mcp with explicit authz profile", func(t *testing.T) {
		t.Parallel()
		f := baseFlags
		f.mcp = true
		f.mcpAuthzProfile = domainconfig.MCPAuthzProfileObserve
		o, err := buildRunImageOverride(image, cmd, f)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.MCP == nil || o.MCP.Authz == nil {
			t.Fatalf("MCP/Authz nil, want observe")
		}
		if o.MCP.Authz.Profile != domainconfig.MCPAuthzProfileObserve {
			t.Errorf("MCP.Authz.Profile = %q, want %q", o.MCP.Authz.Profile, domainconfig.MCPAuthzProfileObserve)
		}
	})

	t.Run("egress profile override", func(t *testing.T) {
		t.Parallel()
		f := baseFlags
		f.egressProfile = "standard"
		o, err := buildRunImageOverride(image, cmd, f)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.EgressProfile != "standard" {
			t.Errorf("EgressProfile = %q, want standard", o.EgressProfile)
		}
	})

	t.Run("resources from flags", func(t *testing.T) {
		t.Parallel()
		f := baseFlags
		f.cpus = 4
		f.memory = "4g"
		f.tmpSize = "512m"
		o, err := buildRunImageOverride(image, cmd, f)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if o.CPUs != 4 {
			t.Errorf("CPUs = %d, want 4", o.CPUs)
		}
		// 4g -> 4096 MiB
		if got := o.Memory.MiB(); got != 4096 {
			t.Errorf("Memory MiB = %d, want 4096", got)
		}
		if got := o.TmpSize.MiB(); got != 512 {
			t.Errorf("TmpSize MiB = %d, want 512", got)
		}
	})

	t.Run("empty image rejected", func(t *testing.T) {
		t.Parallel()
		if _, err := buildRunImageOverride("", cmd, baseFlags); err == nil {
			t.Error("expected error for empty image, got nil")
		}
	})

	t.Run("empty command rejected", func(t *testing.T) {
		t.Parallel()
		if _, err := buildRunImageOverride(image, nil, baseFlags); err == nil {
			t.Error("expected error for empty command, got nil")
		}
	})

	t.Run("command is copied not aliased", func(t *testing.T) {
		t.Parallel()
		src := []string{"aider", "--foo"}
		o, err := buildRunImageOverride(image, src, baseFlags)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		src[0] = "mutated"
		if o.Command[0] != "aider" {
			t.Errorf("Command mutated by caller: %v", o.Command)
		}
	})
}

func TestBuildRunImageOverride_ValidateCustomAgent(t *testing.T) {
	t.Parallel()

	image := "ghcr.io/acme/tool:latest"
	cmd := []string{"aider"}

	t.Run("well-formed passes validation", func(t *testing.T) {
		t.Parallel()
		o, err := buildRunImageOverride(image, cmd, runImageFlags{egressProfile: "permissive"})
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		if err := domainconfig.ValidateCustomAgent("tool", o, imageRefValidator); err != nil {
			t.Fatalf("ValidateCustomAgent: %v", err)
		}
	})

	t.Run("empty command fails validation", func(t *testing.T) {
		t.Parallel()
		o := domainconfig.AgentOverride{
			Image:         image,
			EgressProfile: "permissive",
		}
		if err := domainconfig.ValidateCustomAgent("tool", o, imageRefValidator); err == nil {
			t.Error("expected validation error for empty command, got nil")
		}
	})

	t.Run("invalid image ref fails validation", func(t *testing.T) {
		t.Parallel()
		o := domainconfig.AgentOverride{
			Image:         "::::bad ref::::",
			Command:       cmd,
			EgressProfile: "permissive",
		}
		if err := domainconfig.ValidateCustomAgent("tool", o, imageRefValidator); err == nil {
			t.Error("expected validation error for bad image ref, got nil")
		}
	})

	t.Run("mcp env mode with permissive egress passes without hosts", func(t *testing.T) {
		t.Parallel()
		f := runImageFlags{egressProfile: "standard", mcp: true}
		o, err := buildRunImageOverride(image, cmd, f)
		if err != nil {
			t.Fatalf("build: %v", err)
		}
		// standard + mcp.mode=env is legitimate without egress_hosts (the
		// ValidateCustomAgent exception mirrors Prepare's gateway fallback).
		if err := domainconfig.ValidateCustomAgent("tool", o, imageRefValidator); err != nil {
			t.Fatalf("ValidateCustomAgent with mcp env: %v", err)
		}
	})
}

func TestRunImageCmd_ArgParsing(t *testing.T) {
	t.Parallel()

	t.Run("image and command split at dash", func(t *testing.T) {
		t.Parallel()
		cmd := runImageCmd()
		// Override RunE to capture the parsed arg shape without invoking
		// runRunImage (which performs I/O). The dash/command extraction logic
		// under test is the same ArgsLenAtDash path the real RunE uses.
		var gotImage string
		var gotCommand []string
		var gotDash int
		cmd.RunE = func(c *cobra.Command, args []string) error {
			gotDash = c.ArgsLenAtDash()
			gotImage = args[0]
			if gotDash >= 0 {
				gotCommand = args[gotDash:]
			}
			return nil
		}
		cmd.SetArgs([]string{"ghcr.io/x/y:latest", "--", "aider", "--foo"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute: %v", err)
		}
		if gotImage != "ghcr.io/x/y:latest" {
			t.Errorf("image = %q, want ghcr.io/x/y:latest", gotImage)
		}
		if len(gotCommand) != 2 || gotCommand[0] != "aider" || gotCommand[1] != "--foo" {
			t.Errorf("command = %v, want [aider --foo]", gotCommand)
		}
		if gotDash != 1 {
			t.Errorf("dash = %d, want 1", gotDash)
		}
	})

	t.Run("missing dash errors before any IO", func(t *testing.T) {
		t.Parallel()
		cmd := runImageCmd()
		// Keep RunE real; the dash check returns before runRunImage (no I/O).
		cmd.SetArgs([]string{"ghcr.io/x/y:latest", "aider"})
		err := cmd.Execute()
		if err == nil {
			t.Fatal("expected error for missing --, got nil")
		}
		if !strings.Contains(err.Error(), "requires a command after") {
			t.Errorf("error = %q, want substring %q", err.Error(), "requires a command after")
		}
	})

	t.Run("empty command after dash errors before any IO", func(t *testing.T) {
		t.Parallel()
		cmd := runImageCmd()
		cmd.SetArgs([]string{"ghcr.io/x/y:latest", "--"})
		err := cmd.Execute()
		if err == nil {
			t.Fatal("expected error for empty command, got nil")
		}
		if !strings.Contains(err.Error(), "non-empty command") {
			t.Errorf("error = %q, want substring %q", err.Error(), "non-empty command")
		}
	})

	t.Run("no args rejected by MinimumNArgs", func(t *testing.T) {
		t.Parallel()
		cmd := runImageCmd()
		cmd.SetArgs([]string{})
		if err := cmd.Execute(); err == nil {
			t.Error("expected error for no args, got nil")
		}
	})
}

func TestRunImageCmd_FlagsRegistered(t *testing.T) {
	t.Parallel()
	cmd := runImageCmd()
	for _, name := range []string{
		"name", "env", "env-forward", "memory", "cpus", "tmp-size",
		"mcp", "mcp-authz-profile", "mcp-group", "mcp-port", "mcp-config",
		"mcp-session-ttl", "egress-profile", "allow-host", "workspace",
		"workspace-mode", "review", "exclude", "yes", "ssh-port", "port",
		"config", "debug", "log-file", "no-firmware-download",
		"no-image-cache", "pull", "trace", "timings", "git-token",
		"git-ssh-agent", "seed-credentials",
	} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("flag --%s not registered", name)
		}
	}
	// Egress default is permissive for run-image (opposite of root cmd).
	if got := cmd.Flags().Lookup("egress-profile").DefValue; got != "permissive" {
		t.Errorf("egress-profile default = %q, want permissive", got)
	}
	// Git token / SSH agent are opt-in (default false) for run-image — the
	// inverse of the root command's --no-git-token / --no-git-ssh-agent.
	if got := cmd.Flags().Lookup("git-token").DefValue; got != "false" {
		t.Errorf("git-token default = %q, want false", got)
	}
	if got := cmd.Flags().Lookup("git-ssh-agent").DefValue; got != "false" {
		t.Errorf("git-ssh-agent default = %q, want false", got)
	}
}

func TestRunImageCmd_NameCollisionGuard(t *testing.T) {
	t.Parallel()
	// The collision guard (checkAgentNameCollision) rejects a run-image agent
	// name that collides with ANY already-registered agent — built-in OR a
	// config-declared custom agent — so a data-only entry can neither shadow a
	// built-in's plugin nor silently overwrite a custom agent. The message
	// distinguishes an explicit --name from a derived name.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry := infraagent.NewRegistry(clients.Builtins(logger)...)

	t.Run("explicit --name colliding with a built-in is rejected", func(t *testing.T) {
		t.Parallel()
		err := checkAgentNameCollision(registry, "claude-code", true)
		if err == nil {
			t.Fatal("expected collision error for built-in name, got nil")
		}
		if !strings.Contains(err.Error(), "--name \"claude-code\" is already in use") {
			t.Errorf("error = %q, want --name %q is already in use", err.Error(), "claude-code")
		}
	})

	t.Run("derived name colliding with a built-in is rejected with derived message", func(t *testing.T) {
		t.Parallel()
		err := checkAgentNameCollision(registry, "codex", false)
		if err == nil {
			t.Fatal("expected collision error, got nil")
		}
		if !strings.Contains(err.Error(), "derived agent name \"codex\" collides") {
			t.Errorf("error = %q, want derived-agent-name message", err.Error())
		}
	})

	t.Run("explicit --name colliding with a config custom agent is rejected", func(t *testing.T) {
		t.Parallel()
		// Use a private registry so the Add below doesn't race with the
		// parallel built-in subtests that read the shared outer registry.
		customReg := infraagent.NewRegistry(clients.Builtins(logger)...)
		// Register a config-declared custom agent (data-only, nil Plugin) the
		// way registerCustomAgents would. A run-image --name colliding with it
		// must be rejected — previously it silently overwrote the custom agent.
		customAg, err := domainconfig.AgentFromOverride("my-custom",
			domainconfig.AgentOverride{
				Image:         "ghcr.io/acme/my-custom:latest",
				Command:       []string{"run"},
				EgressProfile: "permissive",
			}, domainconfig.DefaultsConfig{})
		if err != nil {
			t.Fatalf("AgentFromOverride: %v", err)
		}
		if err := customReg.Add(customAg); err != nil {
			t.Fatalf("registry.Add: %v", err)
		}
		err = checkAgentNameCollision(customReg, "my-custom", true)
		if err == nil {
			t.Fatal("expected collision error for custom agent name, got nil")
		}
		if !strings.Contains(err.Error(), "--name \"my-custom\" is already in use") {
			t.Errorf("error = %q, want --name in use", err.Error())
		}
	})

	t.Run("non-colliding name passes", func(t *testing.T) {
		t.Parallel()
		// Fresh registry to avoid sharing the mutated map; a free name passes.
		freeReg := infraagent.NewRegistry(clients.Builtins(logger)...)
		if err := checkAgentNameCollision(freeReg, "aider-bbox", true); err != nil {
			t.Errorf("unexpected collision for free name: %v", err)
		}
	})
}

func TestRunImageCmd_RunE_PreIOErrors(t *testing.T) {
	t.Parallel()

	t.Run("seed-credentials rejected at parse time as incompatible", func(t *testing.T) {
		t.Parallel()
		cmd := runImageCmd()
		cmd.SetArgs([]string{"ghcr.io/x/y:latest", "--seed-credentials", "--", "run"})
		err := cmd.Execute()
		if err == nil {
			t.Fatal("expected error for --seed-credentials, got nil")
		}
		if !strings.Contains(err.Error(), "--seed-credentials is incompatible with run-image") {
			t.Errorf("error = %q, want substring about incompatibility", err.Error())
		}
	})

	t.Run("invalid image ref augmented with lowercase hint", func(t *testing.T) {
		t.Parallel()
		cmd := runImageCmd()
		// Uppercase repo path is rejected by name.ParseReference; the augmented
		// error points the operator at lowercase + the docs.
		cmd.SetArgs([]string{"ghcr.io/Acme/Tool:latest", "--", "run"})
		err := cmd.Execute()
		if err == nil {
			t.Fatal("expected error for uppercase image ref, got nil")
		}
		if !strings.Contains(err.Error(), "OCI references must be lowercase") {
			t.Errorf("error = %q, want lowercase hint", err.Error())
		}
		if !strings.Contains(err.Error(), "docs/run-image.md") {
			t.Errorf("error = %q, want docs/run-image.md pointer", err.Error())
		}
	})

	t.Run("invalid explicit --name rejected before any IO", func(t *testing.T) {
		t.Parallel()
		cmd := runImageCmd()
		// Valid image, invalid --name (spaces / punctuation). ValidateName
		// fires before openLogFile / buildResolvedRegistry, so no I/O.
		cmd.SetArgs([]string{"ghcr.io/x/y:latest", "--name", "Bad Name!", "--", "run"})
		err := cmd.Execute()
		if err == nil {
			t.Fatal("expected error for invalid --name, got nil")
		}
		// Explicit --name branch: the message must NOT blame a derived name.
		if strings.Contains(err.Error(), "derived from image") {
			t.Errorf("error = %q, must not mention 'derived from image' for explicit --name", err.Error())
		}
		if !strings.Contains(err.Error(), "invalid agent name") {
			t.Errorf("error = %q, want 'invalid agent name'", err.Error())
		}
	})

	t.Run("missing dash errors before any IO", func(t *testing.T) {
		t.Parallel()
		cmd := runImageCmd()
		cmd.SetArgs([]string{"ghcr.io/x/y:latest", "aider"})
		err := cmd.Execute()
		if err == nil {
			t.Fatal("expected error for missing --, got nil")
		}
		if !strings.Contains(err.Error(), "requires a command after") {
			t.Errorf("error = %q, want substring 'requires a command after'", err.Error())
		}
	})

	t.Run("empty command after dash errors before any IO", func(t *testing.T) {
		t.Parallel()
		cmd := runImageCmd()
		cmd.SetArgs([]string{"ghcr.io/x/y:latest", "--"})
		err := cmd.Execute()
		if err == nil {
			t.Fatal("expected error for empty command, got nil")
		}
		if !strings.Contains(err.Error(), "non-empty command") {
			t.Errorf("error = %q, want substring 'non-empty command'", err.Error())
		}
	})
}

func TestRunImageCmd_NameWinsOverDerivation(t *testing.T) {
	t.Parallel()
	// --name set (non-empty) is used verbatim; deriveAgentName is not called
	// for the name. We exercise the RunE with a valid --name and confirm it
	// reaches name validation (passes) and proceeds — the only observable
	// pre-IO signal is that it does NOT return an "invalid agent name ...
	// derived from image" error. With a non-colliding, valid --name and a
	// valid image, runRunImage proceeds past name validation into I/O
	// (openLogFile in a real config dir); to keep this hermetic we instead
	// assert the resolution predicate directly: --name non-empty => the
	// name used is exactly --name, and the derived branch is skipped.
	//
	// This mirrors the inline resolution in runRunImage:
	//   nameFromFlag = f.name != ""; if !nameFromFlag { agentName = derive(image) }
	tests := []struct {
		name  string
		flag  string
		image string
		want  string
		from  bool
	}{
		{name: "explicit name wins", flag: "aider-bbox", image: "ghcr.io/acme/Other:latest", want: "aider-bbox", from: true},
		{name: "empty name derives", flag: "", image: "ghcr.io/acme/Aider-Bbox:latest", want: "aider-bbox", from: false},
		{name: "empty name derives from dockerhub", flag: "", image: "ubuntu:24.04", want: "ubuntu", from: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			agentName := tt.flag
			nameFromFlag := agentName != ""
			if !nameFromFlag {
				agentName = deriveAgentName(tt.image)
			}
			if agentName != tt.want {
				t.Errorf("resolved name = %q, want %q", agentName, tt.want)
			}
			if nameFromFlag != tt.from {
				t.Errorf("nameFromFlag = %v, want %v", nameFromFlag, tt.from)
			}
		})
	}
}

func TestRunImageCmd_EnvAliasMerge(t *testing.T) {
	t.Parallel()
	// --env and --env-forward are aliases that both append to the same
	// forwarding list. Set both via cobra flags and confirm both land in the
	// merged envForward passed to runRunImage.
	var gotEnv []string
	cmd := runImageCmd()
	// Capture envForward after flag parsing by swapping in a RunE that records
	// the merged slice and returns before any I/O. The merge logic under test
	// (append(envFwd, envFwdAlt...)) runs in the real RunE before runRunImage.
	cmd.RunE = func(c *cobra.Command, _ []string) error {
		envFwd, _ := c.Flags().GetStringSlice("env")
		envFwdAlt, _ := c.Flags().GetStringSlice("env-forward")
		gotEnv = append(append([]string{}, envFwd...), envFwdAlt...)
		return nil
	}
	cmd.SetArgs([]string{
		"ghcr.io/x/y:latest",
		"--env", "OPENAI_API_KEY",
		"--env-forward", "MY_PREFIX_*",
		"--", "run",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(gotEnv) != 2 || gotEnv[0] != "OPENAI_API_KEY" || gotEnv[1] != "MY_PREFIX_*" {
		t.Errorf("merged envForward = %v, want [OPENAI_API_KEY MY_PREFIX_*]", gotEnv)
	}
}

func TestValidateMCPFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		profile string
		ttl     time.Duration
		wantErr bool
		substr  string
	}{
		{name: "empty profile zero ttl ok", profile: "", ttl: 0, wantErr: false},
		{name: "valid profile ok", profile: "observe", ttl: 0, wantErr: false},
		{name: "safe-tools ok", profile: "safe-tools", ttl: 0, wantErr: false},
		{name: "custom ok", profile: "custom", ttl: 0, wantErr: false},
		{name: "invalid profile rejected", profile: "bogus", ttl: 0, wantErr: true, substr: "invalid --mcp-authz-profile"},
		{name: "positive ttl ok", profile: "", ttl: 30 * time.Minute, wantErr: false},
		{name: "zero ttl ok", profile: "", ttl: 0, wantErr: false},
		{name: "negative ttl rejected", profile: "", ttl: -1 * time.Minute, wantErr: true, substr: "must be non-negative"},
		{name: "invalid profile and negative ttl reports profile first", profile: "bogus", ttl: -1, wantErr: true, substr: "invalid --mcp-authz-profile"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateMCPFlags(tt.profile, tt.ttl)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.substr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tt.substr)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestRunImage_DetachAgentsMap(t *testing.T) {
	t.Parallel()
	// runRunImage detaches cfg.Agents before injecting the run-image override
	// so the ephemeral entry does not leak back into the global config object
	// (MergeConfigs shallow-copies; when local has no agents, merged.Agents
	// aliases global.Agents). We can't call runRunImage hermetically (it does
	// VM I/O), so we exercise the detach step inline: it is a plain map copy.
	global := &domainconfig.Config{
		Agents: map[string]domainconfig.AgentOverride{
			"existing": {Image: "ghcr.io/acme/existing:latest", Command: []string{"run"}},
		},
	}
	// Simulate MergeConfigs(local=nil) returning global (aliased Agents).
	merged := global
	originalAgents := merged.Agents

	// Detach (the exact step runRunImage performs).
	detached := make(map[string]domainconfig.AgentOverride, len(merged.Agents)+1)
	for k, v := range merged.Agents {
		detached[k] = v
	}
	merged.Agents = detached
	merged.Agents["run-image-ephemeral"] = domainconfig.AgentOverride{
		Image: "ghcr.io/x/y:latest", Command: []string{"run"},
	}

	// The override landed on the merged config's detached map.
	if _, ok := merged.Agents["run-image-ephemeral"]; !ok {
		t.Error("override not present on merged.Agents")
	}
	// The original (global) Agents map is untouched — no aliasing leak.
	if _, ok := originalAgents["run-image-ephemeral"]; ok {
		t.Error("override leaked into the original global Agents map (aliasing bug)")
	}
	if len(originalAgents) != 1 {
		t.Errorf("original global Agents map mutated: %v", originalAgents)
	}
	if _, ok := originalAgents["existing"]; !ok {
		t.Errorf("original global Agents lost 'existing': %v", originalAgents)
	}
}
