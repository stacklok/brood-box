// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package sshd

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

func generateTestKeyPair(t *testing.T) (ssh.Signer, ssh.PublicKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	signer, err := ssh.NewSignerFromKey(key)
	require.NoError(t, err)
	return signer, signer.PublicKey()
}

func startTestServer(t *testing.T, authorizedKeys ...ssh.PublicKey) (*Server, int) {
	t.Helper()
	cfg := Config{
		Port:           0, // random port
		AuthorizedKeys: authorizedKeys,
		Env:            []string{"TEST_VAR=hello"},
		DefaultUID:     uint32(os.Getuid()),
		DefaultGID:     uint32(os.Getgid()),
		DefaultUser:    "test",
		DefaultHome:    os.TempDir(),
		DefaultShell:   "/bin/bash",
		Logger:         slog.Default(),
	}
	srv, err := New(cfg)
	require.NoError(t, err)

	// Create the listener in the test goroutine to avoid a data race
	// between ListenAndServe writing s.listener and Port() reading it.
	ln, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port

	go func() { _ = srv.Serve(ln) }()

	t.Cleanup(func() { srv.Close() })
	return srv, port
}

func dialSSH(t *testing.T, port int, signer ssh.Signer) *ssh.Client {
	t.Helper()
	clientCfg := &ssh.ClientConfig{
		User:            "test",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // test only
	}
	client, err := ssh.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port), clientCfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestAuthorizedKeyAccepted(t *testing.T) {
	signer, pubKey := generateTestKeyPair(t)
	_, port := startTestServer(t, pubKey)

	client := dialSSH(t, port, signer)

	session, err := client.NewSession()
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	out, err := session.CombinedOutput("echo connected")
	require.NoError(t, err)
	assert.Contains(t, string(out), "connected")
}

func TestUnauthorizedKeyRejected(t *testing.T) {
	_, pubKey1 := generateTestKeyPair(t)
	signer2, _ := generateTestKeyPair(t)
	_, port := startTestServer(t, pubKey1)

	clientCfg := &ssh.ClientConfig{
		User:            "test",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer2)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // test only
		Timeout:         2 * time.Second,
	}
	_, err := ssh.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port), clientCfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unable to authenticate")
}

func TestExecCommand(t *testing.T) {
	signer, pubKey := generateTestKeyPair(t)
	_, port := startTestServer(t, pubKey)

	client := dialSSH(t, port, signer)

	session, err := client.NewSession()
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	out, err := session.CombinedOutput("echo hello")
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(out))
}

func TestExecCommandEnv(t *testing.T) {
	signer, pubKey := generateTestKeyPair(t)
	_, port := startTestServer(t, pubKey)

	client := dialSSH(t, port, signer)

	session, err := client.NewSession()
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	out, err := session.CombinedOutput("echo $TEST_VAR")
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(out))
}

func TestExitCode(t *testing.T) {
	signer, pubKey := generateTestKeyPair(t)
	_, port := startTestServer(t, pubKey)

	client := dialSSH(t, port, signer)

	session, err := client.NewSession()
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	err = session.Run("exit 42")
	require.Error(t, err)

	var exitErr *ssh.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 42, exitErr.ExitStatus())
}

func TestNonSessionChannelRejected(t *testing.T) {
	signer, pubKey := generateTestKeyPair(t)
	_, port := startTestServer(t, pubKey)

	client := dialSSH(t, port, signer)

	_, _, err := client.OpenChannel("direct-tcpip", ssh.Marshal(struct {
		HostToConnect       string
		PortToConnect       uint32
		OriginatorIPAddress string
		OriginatorPort      uint32
	}{
		HostToConnect:       "localhost",
		PortToConnect:       80,
		OriginatorIPAddress: "127.0.0.1",
		OriginatorPort:      12345,
	}))
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "session") || strings.Contains(err.Error(), "UnknownChannelType"))
}
