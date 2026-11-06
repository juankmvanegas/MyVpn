package netcfg

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// Apply configures a utun interface on macOS.
func Apply(c Config, log Logf) (Reverter, error) {
	u := &undoStack{log: log}
	ip := c.LocalIP.String()
	if _, err := run(log, "/sbin/ifconfig", c.Iface, "inet", ip, ip, "netmask", "255.255.255.255", "mtu", strconv.Itoa(c.MTU), "up"); err != nil {
		return u, err
	}

	// Keep the VPN server (and excluded networks) on the physical gateway.
	defGW, _ := defaultGateway(log)
	if defGW != "" {
		for _, s := range c.ServerIPs {
			if _, err := run(log, "/sbin/route", "-n", "add", "-host", s.String(), defGW); err == nil {
				s := s
				u.push(func() { run(log, "/sbin/route", "-n", "delete", "-host", s.String(), defGW) })
			}
		}
		for _, p := range c.Exclude {
			if _, err := run(log, "/sbin/route", "-n", "add", "-net", p.String(), defGW); err == nil {
				p := p
				u.push(func() { run(log, "/sbin/route", "-n", "delete", "-net", p.String(), defGW) })
			}
		}
	}
	for _, p := range tunnelRoutes(c) {
		if _, err := run(log, "/sbin/route", "-n", "add", "-net", p.String(), "-interface", c.Iface); err != nil {
			// the route may already exist (e.g. stale from a crash): replace it
			run(log, "/sbin/route", "-n", "delete", "-net", p.String())
			if _, err := run(log, "/sbin/route", "-n", "add", "-net", p.String(), "-interface", c.Iface); err != nil {
				continue
			}
		}
		p := p
		u.push(func() { run(log, "/sbin/route", "-n", "delete", "-net", p.String(), "-interface", c.Iface) })
	}

	if len(c.DNS) > 0 {
		if err := applyDNS(c, u, log); err != nil {
			log("DNS: %v", err)
		}
	}
	return u, nil
}

func defaultGateway(log Logf) (string, error) {
	out, err := run(log, "/sbin/route", "-n", "get", "default")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "gateway:") {
			gw := strings.TrimSpace(strings.TrimPrefix(line, "gateway:"))
			if _, err := netip.ParseAddr(gw); err == nil {
				return gw, nil
			}
		}
	}
	return "", fmt.Errorf("no se encontró la puerta de enlace predeterminada")
}

const (
	vpnDNSKey  = "State:/Network/Service/com.myvpn.tunnel/DNS"
	vpnIPv4Key = "State:/Network/Service/com.myvpn.tunnel/IPv4"
)

func applyDNS(c Config, u *undoStack, log Logf) error {
	var servers []string
	for _, d := range c.DNS {
		servers = append(servers, d.String())
	}
	if c.DNSSplit && len(c.DNSSuffix) > 0 {
		// Supplemental resolver: only the VPN domains go to the VPN DNS.
		script := fmt.Sprintf("d.init\nd.add ServerAddresses * %s\nd.add SupplementalMatchDomains * %s\nd.add SearchDomains * %s\nset %s\nquit\n",
			strings.Join(servers, " "), strings.Join(c.DNSSuffix, " "), strings.Join(c.DNSSuffix, " "), vpnDNSKey)
		if _, err := runInput(log, script, "/usr/sbin/scutil"); err != nil {
			return err
		}
		u.push(func() { runInput(log, "remove "+vpnDNSKey+"\nquit\n", "/usr/sbin/scutil"); flushDNS(log) })
		flushDNS(log)
		return nil
	}

	if !c.FullTunnel {
		// Split tunnel: making the VPN the primary service would also move the
		// default route, so restrict the VPN DNS to the VPN domains.
		log("DNS: túnel dividido; el DNS de la VPN se usará solo para %v", c.DNSSuffix)
		c.DNSSplit = true
		if len(c.DNSSuffix) == 0 {
			return nil
		}
		return applyDNS(c, u, log)
	}

	// Full tunnel: publish MyVpn as its own network service and mark it as
	// primary (OverridePrimary). macOS then binds the primary resolver to the
	// utun interface, so every DNS query goes to the VPN DNS *through the
	// tunnel* (overriding the Wi-Fi service's DNS instead would bind the
	// queries to en0 and they would never reach the VPN DNS).
	ip := c.LocalIP.String()
	ipv4 := fmt.Sprintf("d.init\nd.add Addresses * %s\nd.add SubnetMasks * 255.255.255.255\nd.add DestAddresses * %s\nd.add Router %s\nd.add InterfaceName %s\nd.add OverridePrimary # 1\nset %s\nquit\n",
		ip, ip, ip, c.Iface, vpnIPv4Key)
	dns := fmt.Sprintf("d.init\nd.add ServerAddresses * %s\n", strings.Join(servers, " "))
	if len(c.DNSSuffix) > 0 {
		dns += fmt.Sprintf("d.add SearchDomains * %s\nd.add DomainName %s\n", strings.Join(c.DNSSuffix, " "), c.DNSSuffix[0])
	}
	dns += "set " + vpnDNSKey + "\nquit\n"
	if _, err := runInput(log, dns, "/usr/sbin/scutil"); err != nil {
		return err
	}
	u.push(func() {
		runInput(log, "remove "+vpnDNSKey+"\nquit\n", "/usr/sbin/scutil")
		flushDNS(log)
	})
	if _, err := runInput(log, ipv4, "/usr/sbin/scutil"); err != nil {
		return err
	}
	u.push(func() { runInput(log, "remove "+vpnIPv4Key+"\nquit\n", "/usr/sbin/scutil") })
	log("DNS: servicio MyVpn publicado como principal (%s) con DNS %v", c.Iface, servers)
	flushDNS(log)
	return nil
}

func flushDNS(log Logf) {
	run(log, "/usr/bin/dscacheutil", "-flushcache")
	run(log, "/usr/bin/killall", "-HUP", "mDNSResponder")
}
