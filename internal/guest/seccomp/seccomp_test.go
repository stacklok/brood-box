// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && (amd64 || arm64)

package seccomp

import (
	"os"
	"os/exec"
	"syscall"
	"testing"

	secbpf "github.com/elastic/go-seccomp-bpf"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExploitIndicatorBlocks(t *testing.T) {
	t.Parallel()

	group := exploitIndicatorBlocks()
	assert.Equal(t, secbpf.ActionKillProcess, group.Action)

	expected := []string{
		"io_uring_setup",
		"io_uring_enter",
		"io_uring_register",
		"ptrace",
		"process_vm_readv",
		"process_vm_writev",
		"kexec_load",
		"kexec_file_load",
		"init_module",
		"finit_module",
		"delete_module",
		"bpf",
		"seccomp",
	}
	assert.Equal(t, expected, group.Names)
}

func TestOperationalBlocks(t *testing.T) {
	t.Parallel()

	group := operationalBlocks()
	assert.Equal(t, secbpf.ActionErrno, group.Action)

	expected := []string{
		"mount",
		"umount2",
		"pivot_root",
		"chroot",
		"fsopen",
		"fsconfig",
		"fspick",
		"move_mount",
		"open_tree",
		"unshare",
		"setns",
		"perf_event_open",
		"userfaultfd",
		"personality",
		"add_key",
		"request_key",
		"keyctl",
		"landlock_create_ruleset",
		"landlock_add_rule",
		"landlock_restrict_self",
		"acct",
		"swapon",
		"swapoff",
		"quotactl",
		"settimeofday",
		"clock_adjtime",
		"lookup_dcookie",
		"kcmp",
		"nfsservctl",
	}
	assert.Equal(t, expected, group.Names)
}

func TestBlockedSocketFamilies(t *testing.T) {
	t.Parallel()

	group := blockedSocketFamilies()
	assert.Equal(t, secbpf.ActionErrno, group.Action)
	require.Len(t, group.NamesWithCondtions, 5)

	expectedFamilies := []uint64{
		uint64(syscall.AF_NETLINK),
		uint64(syscall.AF_PACKET),
		afKEY,
		afALG,
		afVSOCK,
	}
	for i, entry := range group.NamesWithCondtions {
		assert.Equal(t, "socket", entry.Name, "entry %d", i)
		require.Len(t, entry.Conditions, 1, "entry %d", i)
		assert.Equal(t, uint32(0), entry.Conditions[0].Argument, "entry %d", i)
		assert.Equal(t, secbpf.Equal, entry.Conditions[0].Operation, "entry %d", i)
		assert.Equal(t, expectedFamilies[i], entry.Conditions[0].Value, "entry %d", i)
	}
}

func TestCloneNamespaceBlock(t *testing.T) {
	t.Parallel()

	group := cloneNamespaceBlock()
	assert.Equal(t, secbpf.ActionErrno, group.Action)
	require.Len(t, group.NamesWithCondtions, 1)

	entry := group.NamesWithCondtions[0]
	assert.Equal(t, "clone", entry.Name)
	require.Len(t, entry.Conditions, 1)
	assert.Equal(t, uint32(0), entry.Conditions[0].Argument)
	assert.Equal(t, secbpf.BitsSet, entry.Conditions[0].Operation)
	assert.Equal(t, uint64(cloneNamespaceMask), entry.Conditions[0].Value)
}

func TestBlockedSyscallsReturnsFourGroups(t *testing.T) {
	t.Parallel()

	groups := blockedSyscalls()
	assert.Len(t, groups, 4, "expected exploit + operational + clone-ns + socket groups")
}

// TestApplyBlocksSyscalls verifies the seccomp filter in an isolated subprocess.
// Seccomp filters are irreversible and process-wide, so we re-exec the test
// binary as a child process — the filter only contaminates the subprocess.
func TestApplyBlocksSyscalls(t *testing.T) {
	t.Parallel()

	if os.Getenv("APIARY_SECCOMP_CHILD") == "1" {
		// Child process: apply filter, probe blocked syscalls, print results.
		runSeccompChild()
		return
	}

	// Parent process: re-exec ourselves running only this test.
	//nolint:gosec // Test helper — arguments are not user-controlled.
	cmd := exec.Command(os.Args[0], "-test.run=^TestApplyBlocksSyscalls$", "-test.v")
	cmd.Env = append(os.Environ(), "APIARY_SECCOMP_CHILD=1")

	out, err := cmd.CombinedOutput()
	output := string(out)

	require.NoError(t, err, "subprocess failed:\n%s", output)
	assert.Contains(t, output, "SECCOMP_OK")
	assert.Contains(t, output, "MOUNT_BLOCKED")
	assert.Contains(t, output, "NETLINK_BLOCKED")
	assert.Contains(t, output, "PACKET_BLOCKED")
	assert.Contains(t, output, "INET_ALLOWED")
}

// runSeccompChild is the subprocess entry point. It applies the filter and
// probes blocked syscalls, printing markers the parent asserts on.
func runSeccompChild() {
	if err := Apply(); err != nil {
		_, _ = os.Stderr.WriteString("apply failed: " + err.Error() + "\n")
		os.Exit(1)
	}
	emit("SECCOMP_OK")

	// Probe operational block: mount (EPERM).
	if err := syscall.Mount("none", "/tmp", "tmpfs", 0, ""); err == syscall.EPERM {
		emit("MOUNT_BLOCKED")
	}

	// Probe blocked socket families.
	probeBlockedSocket(syscall.AF_NETLINK, "NETLINK_BLOCKED")
	probeBlockedSocket(syscall.AF_PACKET, "PACKET_BLOCKED")

	// Verify allowed socket families still work.
	probeAllowedSocket(syscall.AF_INET, "INET_ALLOWED")
}

func probeBlockedSocket(family int, marker string) {
	fd, err := syscall.Socket(family, syscall.SOCK_STREAM, 0)
	switch err {
	case syscall.EPERM:
		emit(marker)
	case nil:
		_ = syscall.Close(fd)
	}
}

func probeAllowedSocket(family int, marker string) {
	fd, err := syscall.Socket(family, syscall.SOCK_STREAM, 0)
	if err == nil {
		emit(marker)
		_ = syscall.Close(fd)
	}
}

func emit(marker string) {
	_, _ = os.Stdout.WriteString(marker + "\n")
}
