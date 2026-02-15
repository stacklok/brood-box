// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

// Package env parses the /etc/sandbox-env file written by the host-side
// InjectEnvFile hook into KEY=value pairs suitable for os/exec.Cmd.Env.
package env

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// maxFileSize is the maximum size of the env file we will read (1 MB).
const maxFileSize = 1 << 20

// Load reads a sandbox-env file at path and returns environment entries
// as a slice of "KEY=value" strings. On missing file it returns (nil, nil).
func Load(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("opening env file: %w", err)
	}
	defer func() { _ = f.Close() }()

	reader := io.LimitReader(f, maxFileSize)
	scanner := bufio.NewScanner(reader)

	var result []string
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "export ") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")

		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		result = append(result, key+"="+unquote(val))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading env file: %w", err)
	}

	return result, nil
}

// unquote removes surrounding single quotes and resolves the shell escape
// sequence '\” (end quote, literal quote, start quote) back to a single
// quote character.
func unquote(s string) string {
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		s = s[1 : len(s)-1]
		s = strings.ReplaceAll(s, `'\''`, `'`)
	}
	return s
}
