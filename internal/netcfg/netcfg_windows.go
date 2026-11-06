package netcfg

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Apply configures the Wintun adapter on Windows using netsh/route.
func Apply(c Config, log Logf) (Reverter, error) {
	u := &undoStack{log: log}
	ifc, err := net.InterfaceByName(c.Iface)
	if err != nil {
		return u, fmt.Errorf("no se encontró la interfaz %s: %w", c.Iface, err)
	}
	idx := strconv.Itoa(ifc.Index)
	name := "name=" + c.Iface

	if _, err := run(log, "netsh", "interface", "ipv4", "set", "address", name, "source=static",
		"address="+c.LocalIP.String(), "mask=255.255.255.255", "gateway=none"); err != nil {
		return u, err
	}
	run(log, "netsh", "interface", "ipv4", "set", "subinterface", c.Iface, "mtu="+strconv.Itoa(c.MTU), "store=active")
	run(log, "netsh", "interface", "ipv4", "set", "interface", c.Iface, "metric=1")

	if nh, ifidx := bestRoute(log, firstServer(c)); nh != "" {
		for _, s := range c.ServerIPs {
			s := s.String()
			if _, err := run(log, "route", "add", s, "mask", "255.255.255.255", nh, "if", ifidx, "metric", "1"); err == nil {
				u.push(func() { run(log, "route", "delete", s, "mask", "255.255.255.255", nh) })
			}
		}
		for _, p := range c.Exclude {
			dst, mask := p.Addr().String(), maskString(p)
			if _, err := run(log, "route", "add", dst, "mask", mask, nh, "if", ifidx, "metric", "1"); err == nil {
				u.push(func() { run(log, "route", "delete", dst, "mask", mask, nh) })
			}
		}
	}
	for _, p := range tunnelRoutes(c) {
		pfx := "prefix=" + p.String()
		if _, err := run(log, "netsh", "interface", "ipv4", "add", "route", pfx, "interface="+idx, "nexthop=0.0.0.0", "metric=1", "store=active"); err == nil {
			u.push(func() {
				run(log, "netsh", "interface", "ipv4", "delete", "route", pfx, "interface="+idx, "nexthop=0.0.0.0", "store=active")
			})
		}
	}
	if len(c.DNS) > 0 {
		run(log, "netsh", "interface", "ipv4", "set", "dnsservers", name, "source=static", "address="+c.DNS[0].String(), "register=none", "validate=no")
		for i, d := range c.DNS[1:] {
			run(log, "netsh", "interface", "ipv4", "add", "dnsservers", name, "address="+d.String(), "index="+strconv.Itoa(i+2), "validate=no")
		}
		if len(c.DNSSuffix) > 0 {
			run(log, "powershell", "-NoProfile", "-NonInteractive", "-Command",
				fmt.Sprintf("Set-DnsClient -InterfaceIndex %s -ConnectionSpecificSuffix '%s'", idx, c.DNSSuffix[0]))
		}
		run(log, "ipconfig", "/flushdns")
		u.push(func() { run(log, "ipconfig", "/flushdns") })
	}
	return u, nil
}

func firstServer(c Config) string {
	if len(c.ServerIPs) > 0 {
		return c.ServerIPs[0].String()
	}
	return "8.8.8.8"
}

// bestRoute returns next hop and interface index of the current route to ip.
func bestRoute(log Logf, ip string) (string, string) {
	out, err := run(log, "powershell", "-NoProfile", "-NonInteractive", "-Command",
		fmt.Sprintf("$r = Find-NetRoute -RemoteIPAddress %s | Where-Object { $_.NextHop } | Select-Object -First 1; \"$($r.NextHop) $($r.InterfaceIndex)\"", ip))
	if err != nil {
		return "", ""
	}
	f := strings.Fields(out)
	if len(f) != 2 || f[0] == "0.0.0.0" {
		return "", ""
	}
	return f[0], f[1]
}
