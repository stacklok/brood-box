// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package sshd

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net"
	"sync"

	"golang.org/x/crypto/ssh"
)

// Config holds the configuration for the embedded SSH server.
type Config struct {
	Port           int
	AuthorizedKeys []ssh.PublicKey
	Env            []string // base environment from /etc/sandbox-env
	DefaultUID     uint32   // 1000
	DefaultGID     uint32   // 1000
	DefaultUser    string   // "sandbox"
	DefaultHome    string   // "/home/sandbox"
	DefaultShell   string   // "/bin/bash"
	Logger         *slog.Logger
}

// Server is a minimal SSH server that executes commands inside the guest VM.
type Server struct {
	cfg      Config
	sshCfg   *ssh.ServerConfig
	listener net.Listener
	wg       sync.WaitGroup
	quit     chan struct{}
	logger   *slog.Logger
}

// New creates a new SSH server with an ephemeral ECDSA host key.
func New(cfg Config) (*Server, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating host key: %w", err)
	}

	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		return nil, fmt.Errorf("creating host key signer: %w", err)
	}

	sshCfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			for _, ak := range cfg.AuthorizedKeys {
				if subtle.ConstantTimeCompare(key.Marshal(), ak.Marshal()) == 1 {
					return &ssh.Permissions{}, nil
				}
			}
			return nil, fmt.Errorf("unknown public key")
		},
	}
	sshCfg.AddHostKey(signer)

	return &Server{
		cfg:    cfg,
		sshCfg: sshCfg,
		quit:   make(chan struct{}),
		logger: cfg.Logger,
	}, nil
}

// Port returns the actual port the server is listening on.
// This is useful when the server was configured with port 0.
func (s *Server) Port() int {
	if s.listener != nil {
		return s.listener.Addr().(*net.TCPAddr).Port
	}
	return s.cfg.Port
}

// ListenAndServe creates a TCP listener and starts accepting SSH connections.
// It blocks until Close is called or a fatal listener error occurs.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.cfg.Port))
	if err != nil {
		return fmt.Errorf("listening on port %d: %w", s.cfg.Port, err)
	}
	return s.Serve(ln)
}

// Serve accepts SSH connections on the given listener. It blocks until
// Close is called or a fatal listener error occurs.
func (s *Server) Serve(ln net.Listener) error {
	s.listener = ln

	sem := make(chan struct{}, 4) // max 4 concurrent connections
	for {
		conn, err := s.listener.Accept()
		select {
		case <-s.quit:
			return nil
		default:
		}
		if err != nil {
			select {
			case <-s.quit:
				return nil
			default:
				s.logger.Warn("accept error", "error", err)
				continue
			}
		}

		sem <- struct{}{}
		s.wg.Add(1)
		go func() {
			defer func() {
				s.wg.Done()
				<-sem
			}()
			s.handleConnection(conn)
		}()
	}
}

// Close shuts down the server gracefully, waiting for active connections
// to finish.
func (s *Server) Close() {
	close(s.quit)
	if s.listener != nil {
		_ = s.listener.Close()
	}
	s.wg.Wait()
}

func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, s.sshCfg)
	if err != nil {
		s.logger.Warn("SSH handshake failed", "error", err)
		return
	}
	defer sshConn.Close()

	// Discard all global requests.
	go ssh.DiscardRequests(reqs)

	for newCh := range chans {
		// SECURITY: Reject non-session channels.
		if newCh.ChannelType() != "session" {
			_ = newCh.Reject(ssh.UnknownChannelType, "only session channels are supported")
			continue
		}
		ch, requests, err := newCh.Accept()
		if err != nil {
			s.logger.Warn("channel accept failed", "error", err)
			continue
		}
		go s.handleSession(ch, requests)
	}
}
