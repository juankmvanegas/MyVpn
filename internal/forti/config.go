package forti

import (
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// TunnelConfig is the subset of /remote/fortisslvpn_xml MyVpn uses.
type TunnelConfig struct {
	AssignedIP  netip.Addr     `json:"assignedIp"`
	DNS         []netip.Addr   `json:"dns"`
	DNSSuffix   []string       `json:"dnsSuffix"`
	Routes      []netip.Prefix `json:"routes"`      // split-tunnel include routes; empty => full tunnel
	ExcludeMode bool           `json:"excludeMode"` // split-tunnel-info negate='1': Routes are excluded
	// SplitDNS lists domains that must be resolved by specific VPN DNS
	// servers (<split-dns>); everything else keeps the local resolver.
	SplitDNS    []SplitDNS `json:"splitDns"`
	IdleTimeout int        `json:"idleTimeout"`
	Raw         string     `json:"-"`
	AuthTimeout int        `json:"authTimeout"`
	FOSVersion  string     `json:"fosVersion"`
}

// FullTunnel reports whether all traffic should go through the VPN.
func (c *TunnelConfig) FullTunnel() bool { return len(c.Routes) == 0 || c.ExcludeMode }

// SplitDNS is one <split-dns domains=... dnsserver1=... dnsserver2=...> entry.
type SplitDNS struct {
	Domains []string     `json:"domains"`
	Servers []netip.Addr `json:"servers"`
}

type xmlAddr struct {
	IP   string `xml:"ip,attr"`
	Mask string `xml:"mask,attr"`
}

type xmlSplit struct {
	Negate string    `xml:"negate,attr"`
	Addrs  []xmlAddr `xml:"addr"`
}

type xmlIPv4 struct {
	DNS []struct {
		IP     string `xml:"ip,attr"`
		Domain string `xml:"domain,attr"`
	} `xml:"dns"`
	DNSSuffix []struct {
		Domain string `xml:"domain,attr"`
	} `xml:"dns-suffix"`
	Assigned struct {
		IPv4 string `xml:"ipv4,attr"`
	} `xml:"assigned-addr"`
	Split    []xmlSplit `xml:"split-tunnel-info"`
	SplitDNS []struct {
		Domains string `xml:"domains,attr"`
		DNS1    string `xml:"dnsserver1,attr"`
		DNS2    string `xml:"dnsserver2,attr"`
	} `xml:"split-dns"`
}

type xmlTunnel struct {
	XMLName xml.Name `xml:"sslvpn-tunnel"`
	FOS     struct {
		Major string `xml:"major,attr"`
		Minor string `xml:"minor,attr"`
		Patch string `xml:"patch,attr"`
	} `xml:"fos"`
	IPv4 xmlIPv4 `xml:"ipv4"`
	// Older FortiOS releases put these at the top level.
	Assigned struct {
		IPv4 string `xml:"ipv4,attr"`
	} `xml:"assigned-addr"`
	Split []xmlSplit `xml:"split-tunnel-info"`
	Idle  struct {
		Val int `xml:"val,attr"`
	} `xml:"idle-timeout"`
	Auth struct {
		Val int `xml:"val,attr"`
	} `xml:"auth-timeout"`
}

// ParseTunnelConfig parses the XML document returned by /remote/fortisslvpn_xml.
func ParseTunnelConfig(data []byte) (*TunnelConfig, error) {
	var x xmlTunnel
	if err := xml.Unmarshal(data, &x); err != nil {
		return nil, fmt.Errorf("configuración XML inválida: %w", err)
	}
	cfg := &TunnelConfig{IdleTimeout: x.Idle.Val, AuthTimeout: x.Auth.Val}
	if x.FOS.Major != "" {
		cfg.FOSVersion = x.FOS.Major + "." + x.FOS.Minor + "." + x.FOS.Patch
	}
	for _, s := range []string{x.IPv4.Assigned.IPv4, x.Assigned.IPv4} {
		if a, err := netip.ParseAddr(s); err == nil && a.Is4() {
			cfg.AssignedIP = a
			break
		}
	}
	for _, d := range x.IPv4.DNS {
		if a, err := netip.ParseAddr(d.IP); err == nil && a.Is4() && !a.IsUnspecified() {
			cfg.DNS = append(cfg.DNS, a)
		}
		if d.Domain != "" {
			cfg.DNSSuffix = append(cfg.DNSSuffix, splitDomains(d.Domain)...)
		}
	}
	for _, d := range x.IPv4.DNSSuffix {
		cfg.DNSSuffix = append(cfg.DNSSuffix, splitDomains(d.Domain)...)
	}
	cfg.DNSSuffix = dedupe(cfg.DNSSuffix)
	for _, sd := range x.IPv4.SplitDNS {
		e := SplitDNS{Domains: splitDomains(sd.Domains)}
		for _, ip := range []string{sd.DNS1, sd.DNS2} {
			if a, err := netip.ParseAddr(ip); err == nil && a.Is4() && !a.IsUnspecified() {
				e.Servers = append(e.Servers, a)
			}
		}
		if len(e.Servers) == 0 {
			e.Servers = cfg.DNS
		}
		if len(e.Domains) > 0 && len(e.Servers) > 0 {
			cfg.SplitDNS = append(cfg.SplitDNS, e)
		}
	}
	splits := append(append([]xmlSplit{}, x.IPv4.Split...), x.Split...)
	for _, s := range splits {
		if s.Negate == "1" || strings.EqualFold(s.Negate, "true") {
			cfg.ExcludeMode = true
		}
		for _, a := range s.Addrs {
			p, err := prefixFromMask(a.IP, a.Mask)
			if err != nil {
				continue
			}
			if p.Bits() == 0 { // 0.0.0.0/0 means full tunnel
				continue
			}
			cfg.Routes = append(cfg.Routes, p)
		}
	}
	return cfg, nil
}

func splitDomains(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == ',' || r == ' ' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func prefixFromMask(ip, mask string) (netip.Prefix, error) {
	a, err := netip.ParseAddr(ip)
	if err != nil || !a.Is4() {
		return netip.Prefix{}, errors.New("ip inválida")
	}
	bits := 32
	if mask != "" {
		if m := net.ParseIP(mask).To4(); m != nil {
			ones, total := net.IPMask(m).Size()
			if total == 0 { // non-contiguous mask
				return netip.Prefix{}, errors.New("máscara no contigua")
			}
			bits = ones
		}
	}
	return netip.PrefixFrom(a, bits).Masked(), nil
}

// FetchTunnelConfig allocates the tunnel and downloads its configuration.
func (g Gateway) FetchTunnelConfig(cookie string) (*TunnelConfig, error) {
	// FortiClient/openfortivpn call /remote/fortisslvpn first to allocate resources.
	if resp, _, err := g.get("/remote/fortisslvpn", cookie); err != nil {
		return nil, err
	} else if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, ErrSessionExpired
	}
	resp, body, err := g.get("/remote/fortisslvpn_xml", cookie)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusFound {
			return nil, ErrSessionExpired
		}
		return nil, fmt.Errorf("/remote/fortisslvpn_xml devolvió HTTP %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "sslvpn-tunnel") {
		return nil, ErrSessionExpired
	}
	cfg, err := ParseTunnelConfig(body)
	if cfg != nil {
		cfg.Raw = string(body)
	}
	return cfg, err
}

// ErrSessionExpired signals that the SVPNCOOKIE is no longer valid.
var ErrSessionExpired = errors.New("la sesión VPN expiró o no es válida; inicia sesión de nuevo")
