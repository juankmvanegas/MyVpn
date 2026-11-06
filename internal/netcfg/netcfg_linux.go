package netcfg

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Apply configures a TUN interface on Linux using iproute2 and
// systemd-resolved (falling back to /etc/resolv.conf).
func Apply(c Config, log Logf) (Reverter, error) {
	u := &undoStack{log: log}
	if _, err := run(log, "ip", "addr", "add", c.LocalIP.String()+"/32", "dev", c.Iface); err != nil {
		return u, err
	}
	if _, err := run(log, "ip", "link", "set", "dev", c.Iface, "mtu", strconv.Itoa(c.MTU), "up"); err != nil {
		return u, err
	}

	for _, s := range c.ServerIPs {
		if args := viaArgs(log, s.String()); args != nil {
			dst := s.String() + "/32"
			if _, err := run(log, "ip", append([]string{"route", "replace", dst}, args...)...); err == nil {
				u.push(func() { run(log, "ip", "route", "del", dst) })
			}
		}
	}
	for _, p := range c.Exclude {
		if args := viaArgs(log, p.Addr().String()); args != nil {
			dst := p.String()
			if _, err := run(log, "ip", append([]string{"route", "replace", dst}, args...)...); err == nil {
				u.push(func() { run(log, "ip", "route", "del", dst) })
			}
		}
	}
	for _, p := range tunnelRoutes(c) {
		dst := p.String()
		if _, err := run(log, "ip", "route", "replace", dst, "dev", c.Iface); err == nil {
			u.push(func() { run(log, "ip", "route", "del", dst, "dev", c.Iface) })
		}
	}
	if len(c.DNS) > 0 {
		applyDNS(c, u, log)
	}
	return u, nil
}

// viaArgs returns the "via X dev Y" arguments of the current route to ip.
func viaArgs(log Logf, ip string) []string {
	out, err := run(log, "ip", "-4", "route", "get", ip)
	if err != nil {
		return nil
	}
	f := strings.Fields(out)
	var args []string
	for i := 0; i+1 < len(f); i++ {
		if f[i] == "via" || f[i] == "dev" {
			args = append(args, f[i], f[i+1])
		}
	}
	return args
}

func applyDNS(c Config, u *undoStack, log Logf) {
	var servers []string
	for _, d := range c.DNS {
		servers = append(servers, d.String())
	}
	if _, err := exec.LookPath("resolvectl"); err == nil {
		if _, err := run(log, "resolvectl", append([]string{"dns", c.Iface}, servers...)...); err == nil {
			domains := append([]string{}, c.DNSSuffix...)
			if !c.DNSSplit || len(c.DNSSuffix) == 0 {
				domains = append(domains, "~.") // route every query to the VPN DNS
			}
			run(log, "resolvectl", append([]string{"domain", c.Iface}, domains...)...)
			if !c.DNSSplit {
				run(log, "resolvectl", "default-route", c.Iface, "true")
			}
			run(log, "resolvectl", "flush-caches")
			u.push(func() { run(log, "resolvectl", "revert", c.Iface) })
			return
		}
	}
	// Fallback: rewrite /etc/resolv.conf and restore it afterwards.
	const path = "/etc/resolv.conf"
	orig, err := os.ReadFile(path)
	if err != nil {
		log("DNS: no se pudo leer %s: %v", path, err)
		return
	}
	var b strings.Builder
	b.WriteString("# generado por MyVpn\n")
	for _, s := range servers {
		b.WriteString("nameserver " + s + "\n")
	}
	if len(c.DNSSuffix) > 0 {
		b.WriteString("search " + strings.Join(c.DNSSuffix, " ") + "\n")
	}
	b.Write(orig)
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		log("DNS: no se pudo escribir %s: %v", path, err)
		return
	}
	u.push(func() { os.WriteFile(path, orig, 0o644) })
}
