// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

package vm

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/stacklok/go-microvm/image"
)

// knownHostsContent contains well-known SSH host keys for major Git
// hosting providers. These are public keys published by the providers
// and are NOT secrets — they are the same keys any SSH client receives
// on first connection and stores in ~/.ssh/known_hosts.
//
// Without these keys, git push/pull over SSH inside the guest VM fails
// with "Host key verification failed" because the ephemeral guest has
// no pre-existing known_hosts file and the SSH client cannot
// interactively prompt the user through the agent's PTY.
//
// Each provider publishes three key types (ed25519, ecdsa, rsa) to
// support hosts with different SSH client configurations. All three
// are included so the guest SSH client can negotiate with whichever
// algorithm the server prefers.
//
// To update these keys, run:
//
//	ssh-keyscan -t ed25519,ecdsa,rsa github.com gitlab.com bitbucket.org
//
// and cross-reference with the official documentation:
//   - GitHub:    https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/githubs-ssh-key-fingerprints
//   - GitLab:    https://docs.gitlab.com/ee/user/gitlab_com/#ssh-host-keys-fingerprints
//   - Bitbucket: https://bitbucket.org/blog/ssh-host-key-changes
//
// Last verified: 2026-03-17 via ssh-keyscan and official documentation.
const knownHostsContent = `github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl
github.com ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBEmKSENjQEezOmxkZMy7opKgwFB9nkt5YRrYMjNuG5N87uRgg6CLrbo5wAdT/y6v0mKV0U2w0WZ2YB/++Tpockg=
github.com ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCj7ndNxQowgcQnjshcLrqPEiiphnt+VTTvDP6mHBL9j1aNUkY4Ue1gvwnGLVlOhGeYrnZaMgRK6+PKCUXaDbC7qtbW8gIkhL7aGCsOr/C56SJMy/BCZfxd1nWzAOxSDPgVsmerOBYfNqltV9/hWCqBywINIR+5dIg6JTJ72pcEpEjcYgXkE2YEFXV1JHnsKgbLWNlhScqb2UmyRkQyytRLtL+38TGxkxCflmO+5Z8CSSNY7GidjMIZ7Q4zMjA2n1nGrlTDkzwDCsw+wqFPGQA179cnfGWOWRVruj16z6XyvxvjJwbz0wQZ75XK5tKSb7FNyeIEs4TT4jk+S4dhPeAUC5y+bDYirYgM4GC7uEnztnZyaVWQ7B381AK4Qdrwt51ZqExKbQpTUNn+EjqoTwvqNj4kqx5QUCI0ThS/YkOxJCXmPUWZbhjpCg56i+2aB6CmK2JGhn57K5mj0MNdBXA4/WnwH6XoPWJzK5Nyu2zB3nAZp+S5hpQs+p1vN1/wsjk=
gitlab.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAfuCHKVTjquxvt6CM6tdG4SLp1Btn/nOeHHE5UOzRdf
gitlab.com ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBFSMqzJeV9rUzU4kWitGjeR4PWSa29SPqJ1fVkhtj3Hw9xjLVXVYrU9QlYWrOLXBpQ6KWjbjTDTdDkoohFzgbEY=
gitlab.com ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCsj2bNKTBSpIYDEGk9KxsGh3mySTRgMtXL583qmBpzeQ+jqCMRgBqB98u3z++J1sKlXHWfM9dyhSevkMwSbhoR8XIq/U0tCNyokEi/ueaBMCvbcTHhO7FcwzY92WK4Yt0aGROY5qX2UKSeOvuP4D6TPqKF1onrSzH9bx9XUf2lEdWT/ia1NEKjunUqu1xOB/StKDHMoX4/OKyIzuS0q/T1zOATthvasJFoPrAjkohTyaDUz2LN5JoH839hViyEG82yB+MjcFV5MU3N1l1QL3cVUCh93xSaua1N85qivl+siMkPGbO5xR/En4iEY6K2XPASUEMaieWVNTRCtJ4S8H+9
bitbucket.org ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIazEu89wgQZ4bqs3d63QSMzYVa0MuJ2e2gKTKqu+UUO
bitbucket.org ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBPIQmuzMBuKdWeF4+a2sjSSpBK0iqitSQ+5BM9KhpexuGt20JpTVM7u5BDZngncgrqDMbWdxMWWOGtZ9UgbqgZE=
bitbucket.org ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQDQeJzhupRu0u0cdegZIa8e86EG2qOCsIsD1Xw0xSeiPDlCr7kq97NLmMbpKTX6Esc30NuoqEEHCuc7yWtwp8dI76EEEB1VqY9QJq6vk+aySyboD5QF61I/1WeTwu+deCbgKMGbUijeXhtfbxSxm6JwGrXrhBdofTsbKRUsrN1WoNgUa8uqN1Vx6WAJw1JHPhglEGGHea6QICwJOAr/6mrui/oB7pkaWKHj3z7d1IC4KWLtY47elvjbaTlkN04Kc/5LFEirorGYVbt15kAUlqGM65pk6ZBxtaO3+30LVlORZkxOh+LKL/BvbZ/iRNhItLqNyieoQj/uh/7Iv4uyH/cV/0b4WDSd3DptigWq84lJubb9t/DnZlrJazxyDCulTmKdOR7vs9gMTo+uoIrPSb8ScTtvw65+odKAlBj59dhnVp9zd7QUojOpXlL62Aw56U4oO+FALuevvMjiWeavKhJqlR7i5n9srYcrNV7ttmDw7kf/97P5zauIhxcjX+xHv4M=
`

// InjectSSHKnownHosts returns a RootFS hook that writes well-known SSH
// host keys for major Git hosting providers into the sandbox user's
// ~/.ssh/known_hosts file.
//
// This is required for git push/pull over SSH to work inside the guest
// VM. Without it, the SSH client rejects connections to GitHub, GitLab,
// and Bitbucket because their host keys are unknown. The guest VM's
// home directory is ephemeral (overlayfs on tmpfs), so even if a user
// accepts a host key interactively, it is lost on the next boot.
//
// The hook creates ~/.ssh/ (mode 0700) if it doesn't exist and writes
// known_hosts with mode 0600, both owned by the sandbox user (1000:1000).
// The base image already creates ~/.ssh/ in the Dockerfile, but the
// hook is idempotent and handles both cases.
func InjectSSHKnownHosts(chown ChownFunc) func(string, *image.OCIConfig) error {
	return func(rootfsPath string, _ *image.OCIConfig) error {
		sshDir := filepath.Join(rootfsPath, sandboxHome, ".ssh")
		if err := os.MkdirAll(sshDir, 0o700); err != nil {
			return fmt.Errorf("creating .ssh dir: %w", err)
		}
		if err := chown(sshDir, sandboxUID, sandboxGID); err != nil {
			return fmt.Errorf("chowning .ssh dir: %w", err)
		}

		knownHostsPath := filepath.Join(sshDir, "known_hosts")
		if err := os.WriteFile(knownHostsPath, []byte(knownHostsContent), 0o600); err != nil {
			return fmt.Errorf("writing known_hosts: %w", err)
		}
		return chown(knownHostsPath, sandboxUID, sandboxGID)
	}
}
