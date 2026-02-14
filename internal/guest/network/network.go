// SPDX-FileCopyrightText: Copyright 2025 Stacklok, Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package network

import (
	"fmt"
	"net"
	"os"

	"github.com/vishvananda/netlink"
)

// Configure sets up networking inside the guest VM: loopback, eth0 with a
// static IP, a default route, and DNS resolution via the host gateway.
func Configure() error {
	lo, err := netlink.LinkByName("lo")
	if err != nil {
		return fmt.Errorf("finding loopback: %w", err)
	}
	if err := netlink.LinkSetUp(lo); err != nil {
		return fmt.Errorf("bringing up loopback: %w", err)
	}

	eth0, err := netlink.LinkByName("eth0")
	if err != nil {
		return fmt.Errorf("finding eth0: %w", err)
	}
	if err := netlink.LinkSetUp(eth0); err != nil {
		return fmt.Errorf("bringing up eth0: %w", err)
	}

	addr, err := netlink.ParseAddr("192.168.127.2/24")
	if err != nil {
		return fmt.Errorf("parsing address: %w", err)
	}
	if err := netlink.AddrAdd(eth0, addr); err != nil {
		return fmt.Errorf("adding address to eth0: %w", err)
	}

	route := &netlink.Route{
		Gw: net.ParseIP("192.168.127.1"),
	}
	if err := netlink.RouteAdd(route); err != nil {
		return fmt.Errorf("adding default route: %w", err)
	}

	if err := os.WriteFile("/etc/resolv.conf", []byte("nameserver 192.168.127.1\n"), 0o644); err != nil {
		return fmt.Errorf("writing resolv.conf: %w", err)
	}

	return nil
}
