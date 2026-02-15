// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package sshd

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe" //nolint:gosec // TIOCSWINSZ ioctl requires unsafe pointer

	"github.com/creack/pty"
	"golang.org/x/crypto/ssh"
)

type ptyRequest struct {
	Term    string
	Columns uint32
	Rows    uint32
	Width   uint32 // pixel width (ignored)
	Height  uint32 // pixel height (ignored)
	Modes   string
}

type windowChangeRequest struct {
	Columns uint32
	Rows    uint32
	Width   uint32
	Height  uint32
}

type signalRequest struct {
	Signal string
}

// sshSignalToOS maps SSH signal names to OS signals.
var sshSignalToOS = map[string]syscall.Signal{
	"ABRT": syscall.SIGABRT,
	"FPE":  syscall.SIGFPE,
	"HUP":  syscall.SIGHUP,
	"ILL":  syscall.SIGILL,
	"INT":  syscall.SIGINT,
	"KILL": syscall.SIGKILL,
	"PIPE": syscall.SIGPIPE,
	"QUIT": syscall.SIGQUIT,
	"SEGV": syscall.SIGSEGV,
	"TERM": syscall.SIGTERM,
	"USR1": syscall.SIGUSR1,
	"USR2": syscall.SIGUSR2,
}

// sessionState tracks state for a single SSH session.
type sessionState struct {
	ptyReq *ptyRequest
	env    map[string]string // env vars set via SSH "env" requests
}

func (s *Server) handleSession(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer func() { _ = ch.Close() }()

	state := &sessionState{
		env: make(map[string]string),
	}

	for req := range reqs {
		switch req.Type {
		case "pty-req":
			var pr ptyRequest
			if err := ssh.Unmarshal(req.Payload, &pr); err != nil {
				if req.WantReply {
					_ = req.Reply(false, nil)
				}
				continue
			}
			state.ptyReq = &pr
			if req.WantReply {
				_ = req.Reply(true, nil)
			}

		case "env":
			var envReq struct {
				Name  string
				Value string
			}
			if err := ssh.Unmarshal(req.Payload, &envReq); err == nil {
				if isAllowedEnvVar(envReq.Name) {
					state.env[envReq.Name] = envReq.Value
				}
			}
			if req.WantReply {
				_ = req.Reply(true, nil)
			}

		case "exec":
			var execReq struct {
				Command string
			}
			if err := ssh.Unmarshal(req.Payload, &execReq); err != nil {
				if req.WantReply {
					_ = req.Reply(false, nil)
				}
				continue
			}
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
			exitCode := s.executeCommand(ch, execReq.Command, state, reqs)
			sendExitStatus(ch, exitCode)
			return

		case "shell":
			if req.WantReply {
				_ = req.Reply(true, nil)
			}
			exitCode := s.executeCommand(ch, "", state, reqs)
			sendExitStatus(ch, exitCode)
			return

		case "window-change":
			if req.WantReply {
				_ = req.Reply(true, nil)
			}

		default:
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}
}

// allowedEnvVars is the set of environment variable names that clients
// may set via SSH "env" requests. This prevents overriding security-sensitive
// variables like PATH, LD_PRELOAD, or HOME.
var allowedEnvVars = map[string]bool{
	"TERM": true, "LANG": true, "LC_ALL": true, "LC_CTYPE": true,
	"COLORTERM": true, "EDITOR": true, "VISUAL": true,
}

func isAllowedEnvVar(name string) bool {
	return allowedEnvVars[name]
}

func (s *Server) executeCommand(ch ssh.Channel, command string, state *sessionState, reqs <-chan *ssh.Request) int {
	// Build environment.
	env := make([]string, 0, len(s.cfg.Env)+10)
	env = append(env, s.cfg.Env...)
	env = append(env,
		fmt.Sprintf("HOME=%s", s.cfg.DefaultHome),
		fmt.Sprintf("USER=%s", s.cfg.DefaultUser),
		fmt.Sprintf("LOGNAME=%s", s.cfg.DefaultUser),
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	)

	// Add TERM from pty request or session env.
	if state.ptyReq != nil && state.ptyReq.Term != "" {
		env = append(env, fmt.Sprintf("TERM=%s", state.ptyReq.Term))
	} else if t, ok := state.env["TERM"]; ok {
		env = append(env, fmt.Sprintf("TERM=%s", t))
	} else {
		env = append(env, "TERM=xterm-256color")
	}

	// Add any SSH env vars (TERM already handled above).
	for k, v := range state.env {
		if k != "TERM" {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
	}

	// Build command.
	var cmd *exec.Cmd
	if command == "" {
		cmd = exec.Command(s.cfg.DefaultShell, "-l")
	} else {
		cmd = exec.Command(s.cfg.DefaultShell, "-c", command)
	}
	cmd.Env = env

	dir := "/workspace"
	if _, err := os.Stat(dir); err != nil {
		dir = s.cfg.DefaultHome
	}
	cmd.Dir = dir

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
	// Only set credentials when the target UID differs from the current
	// process. Changing credentials requires CAP_SETUID/CAP_SETGID.
	if s.cfg.DefaultUID != uint32(os.Getuid()) || s.cfg.DefaultGID != uint32(os.Getgid()) {
		cmd.SysProcAttr.Credential = &syscall.Credential{
			Uid:    s.cfg.DefaultUID,
			Gid:    s.cfg.DefaultGID,
			Groups: []uint32{s.cfg.DefaultGID},
		}
	}

	if state.ptyReq != nil {
		return s.runWithPTY(ch, cmd, state, reqs)
	}

	// For non-PTY sessions, handle signal requests and drain the rest.
	go func() {
		for req := range reqs {
			if req.Type == "signal" {
				signalProcess(cmd.Process, req, s.logger)
				continue
			}
			if req.WantReply {
				_ = req.Reply(false, nil)
			}
		}
	}()
	return s.runWithoutPTY(ch, cmd)
}

func (s *Server) runWithPTY(ch ssh.Channel, cmd *exec.Cmd, state *sessionState, reqs <-chan *ssh.Request) int {
	winSize := &pty.Winsize{
		Rows: uint16(state.ptyReq.Rows),
		Cols: uint16(state.ptyReq.Columns),
	}

	ptmx, err := pty.StartWithSize(cmd, winSize)
	if err != nil {
		s.logger.Error("failed to start command with PTY", "error", err)
		return 1
	}
	defer func() { _ = ptmx.Close() }()

	// Handle window-change and signal requests while the command runs.
	go func() {
		for req := range reqs {
			switch req.Type {
			case "window-change":
				var wc windowChangeRequest
				if err := ssh.Unmarshal(req.Payload, &wc); err == nil {
					setWinsize(ptmx.Fd(), uint16(wc.Rows), uint16(wc.Columns))
				}
				if req.WantReply {
					_ = req.Reply(true, nil)
				}
			case "signal":
				signalProcess(cmd.Process, req, s.logger)
			default:
				if req.WantReply {
					_ = req.Reply(false, nil)
				}
			}
		}
	}()

	// Bidirectional copy.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(ptmx, ch)
	}()

	_, _ = io.Copy(ch, ptmx)

	// Wait for process to exit.
	err = cmd.Wait()
	wg.Wait()

	return exitCode(err)
}

func (s *Server) runWithoutPTY(ch ssh.Channel, cmd *exec.Cmd) int {
	cmd.Stdin = ch
	cmd.Stdout = ch
	cmd.Stderr = ch.Stderr()

	if err := cmd.Start(); err != nil {
		s.logger.Error("failed to start command", "error", err)
		return 1
	}

	// Close stdin when channel sends EOF.
	go func() {
		_, _ = io.Copy(io.Discard, ch)
	}()

	return exitCode(cmd.Wait())
}

// signalProcess forwards an SSH "signal" request to the running process.
func signalProcess(proc *os.Process, req *ssh.Request, logger *slog.Logger) {
	if proc == nil {
		if req.WantReply {
			_ = req.Reply(false, nil)
		}
		return
	}

	var sr signalRequest
	if err := ssh.Unmarshal(req.Payload, &sr); err != nil {
		if req.WantReply {
			_ = req.Reply(false, nil)
		}
		return
	}

	sig, ok := sshSignalToOS[sr.Signal]
	if !ok {
		logger.Warn("unknown SSH signal", "signal", sr.Signal)
		if req.WantReply {
			_ = req.Reply(false, nil)
		}
		return
	}

	if err := proc.Signal(sig); err != nil {
		logger.Warn("failed to signal process", "signal", sr.Signal, "error", err)
	}
	if req.WantReply {
		_ = req.Reply(true, nil)
	}
}

func sendExitStatus(ch ssh.Channel, code int) {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, uint32(code))
	_, _ = ch.SendRequest("exit-status", false, payload)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return 1
}

//nolint:gosec // TIOCSWINSZ ioctl requires unsafe pointer
func setWinsize(fd uintptr, rows, cols uint16) {
	ws := struct {
		Rows uint16
		Cols uint16
		X    uint16
		Y    uint16
	}{Rows: rows, Cols: cols}
	_, _, _ = syscall.Syscall(
		syscall.SYS_IOCTL,
		fd,
		uintptr(syscall.TIOCSWINSZ),
		uintptr(unsafe.Pointer(&ws)),
	)
}
