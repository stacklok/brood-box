// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux && (amd64 || arm64)

// Package seccomp applies a seccomp BPF filter that blocks dangerous syscalls
// inside the guest VM. The filter is a blocklist — everything not explicitly
// blocked is allowed. It is applied after boot (mounts, networking, SSH are
// already up) and inherited by all child processes via fork+exec.
//
// The blocklist is split into two tiers:
//   - Exploitation indicators (ActionKillProcess): syscalls that legitimate
//     coding agents never call — any attempt strongly indicates exploitation.
//   - Operational blocks (ActionErrno/EPERM): syscalls that might be innocuously
//     probed by runtimes or build tools — fail gracefully.
package seccomp

import (
	"fmt"
	"syscall"

	secbpf "github.com/elastic/go-seccomp-bpf"
)

// Socket address families not defined in Go's syscall package.
const (
	afKEY   = 15 // AF_KEY — IPsec key management
	afALG   = 38 // AF_ALG — Kernel crypto API
	afVSOCK = 40 // AF_VSOCK — VM-host communication
)

// Apply installs a seccomp BPF blocklist filter on all OS threads.
// It sets no_new_privs and uses FilterFlagTSync to synchronize the filter
// across all threads in the process.
func Apply() error {
	filter := secbpf.Filter{
		NoNewPrivs: true,
		Flag:       secbpf.FilterFlagTSync | secbpf.FilterFlagLog,
		Policy: secbpf.Policy{
			DefaultAction: secbpf.ActionAllow,
			Syscalls:      blockedSyscalls(),
		},
	}

	if err := secbpf.LoadFilter(filter); err != nil {
		return fmt.Errorf("loading seccomp filter: %w", err)
	}
	return nil
}

// blockedSyscalls returns the syscall groups that comprise the guest blocklist.
func blockedSyscalls() []secbpf.SyscallGroup {
	return []secbpf.SyscallGroup{
		exploitIndicatorBlocks(),
		operationalBlocks(),
		cloneNamespaceBlock(),
		blockedSocketFamilies(),
	}
}

// exploitIndicatorBlocks returns syscalls that are killed on sight.
// These are never called by legitimate coding agents — any attempt strongly
// indicates active exploitation.
func exploitIndicatorBlocks() secbpf.SyscallGroup {
	return secbpf.SyscallGroup{
		Action: secbpf.ActionKillProcess,
		Names: []string{
			// io_uring — prolific source of kernel CVEs.
			"io_uring_setup",
			"io_uring_enter",
			"io_uring_register",

			// Process debugging / cross-process memory access.
			"ptrace",
			"process_vm_readv",
			"process_vm_writev",

			// Kernel replacement.
			"kexec_load",
			"kexec_file_load",

			// Kernel module loading.
			"init_module",
			"finit_module",
			"delete_module",

			// eBPF — kernel attack surface.
			"bpf",

			// Seccomp — prevent installing additional filters (USER_NOTIF,
			// TRACE exploits, kernel struct size leaks).
			"seccomp",
		},
	}
}

// operationalBlocks returns syscalls blocked with EPERM. These might be
// innocuously probed by runtimes or build tools and should fail gracefully.
func operationalBlocks() secbpf.SyscallGroup {
	return secbpf.SyscallGroup{
		Action: secbpf.ActionErrno,
		Names: []string{
			// Filesystem manipulation — boot is already done.
			"mount",
			"umount2",
			"pivot_root",
			"chroot",

			// New mount API (Linux 5.2+) — bypasses mount() block.
			"fsopen",
			"fsconfig",
			"fspick",
			"move_mount",
			"open_tree",

			// Namespace manipulation — #1 entry point for kernel privesc.
			// Note: clone3 is NOT blocked because glibc 2.34+ uses it for
			// fork(). Its namespace flags live inside a struct pointer (arg0)
			// which seccomp BPF cannot inspect.
			"unshare",
			"setns",

			// Side-channel / race condition attack surface.
			"perf_event_open",
			"userfaultfd",

			// Execution domain switching.
			"personality",

			// Kernel keyring — CVE-prone, no agent need.
			"add_key",
			"request_key",
			"keyctl",

			// Landlock — prevent agents from installing restrictive policies
			// that could interfere with workspace review/flush.
			"landlock_create_ruleset",
			"landlock_add_rule",
			"landlock_restrict_self",

			// Miscellaneous — no legitimate agent use.
			"acct",
			"swapon",
			"swapoff",
			"quotactl",
			"settimeofday",
			"clock_adjtime",
			"lookup_dcookie",
			"kcmp",
			"nfsservctl",
		},
	}
}

// cloneNamespaceMask is the combined bitmask of all CLONE_NEW* flags.
// clone() with any of these bits set is blocked (EPERM) to prevent namespace
// creation — the #1 entry point for container-style privilege escalation.
const cloneNamespaceMask = syscall.CLONE_NEWUSER |
	syscall.CLONE_NEWNS |
	syscall.CLONE_NEWNET |
	syscall.CLONE_NEWPID |
	syscall.CLONE_NEWIPC |
	syscall.CLONE_NEWUTS |
	syscall.CLONE_NEWCGROUP |
	syscall.CLONE_NEWTIME

// cloneNamespaceBlock blocks clone() calls that set any namespace flag.
// This closes the gap where unshare/setns are blocked but clone() with
// CLONE_NEWUSER|CLONE_NEWNS could still create namespaces.
//
// Note: clone3 is NOT blocked — glibc 2.34+ uses it for fork(), and its
// namespace flags live inside a struct pointer that BPF cannot inspect
// (same rationale as the existing comment in operationalBlocks).
func cloneNamespaceBlock() secbpf.SyscallGroup {
	return secbpf.SyscallGroup{
		Action: secbpf.ActionErrno,
		// NamesWithCondtions has a typo in the upstream library (missing 'i').
		NamesWithCondtions: []secbpf.NameWithConditions{
			{
				Name: "clone",
				Conditions: secbpf.ArgumentConditions{
					{
						Argument:  0,
						Operation: secbpf.BitsSet,
						Value:     cloneNamespaceMask,
					},
				},
			},
		},
	}
}

// blockedSocketFamilies blocks socket() calls for dangerous address families.
// AF_INET, AF_INET6, and AF_UNIX remain allowed for normal agent operation.
func blockedSocketFamilies() secbpf.SyscallGroup {
	families := []struct {
		value uint64
	}{
		{uint64(syscall.AF_NETLINK)}, // Network/routing config manipulation
		{uint64(syscall.AF_PACKET)},  // Raw packet sniffing/injection
		{afKEY},                      // IPsec key management
		{afALG},                      // Kernel crypto API — CVE-prone
		{afVSOCK},                    // VM-host communication bypass
	}

	entries := make([]secbpf.NameWithConditions, 0, len(families))
	for _, f := range families {
		entries = append(entries, secbpf.NameWithConditions{
			Name: "socket",
			Conditions: secbpf.ArgumentConditions{
				{
					Argument:  0,
					Operation: secbpf.Equal,
					Value:     f.value,
				},
			},
		})
	}

	return secbpf.SyscallGroup{
		Action: secbpf.ActionErrno,
		// NamesWithCondtions has a typo in the upstream library (missing 'i').
		NamesWithCondtions: entries,
	}
}
