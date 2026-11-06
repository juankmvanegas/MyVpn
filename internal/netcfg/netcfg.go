// Package netcfg applies and reverts the operating system network
// configuration for an established tunnel: interface address, routes and DNS.
package netcfg

import (
	"bytes"
	"fmt"
	"net/netip"
	"os/exec"
	"strings"
)

// Config describes the desired network state for a tunnel interface.
type Config struct {
	Iface      string
	LocalIP    netip.Addr
	MTU        int
	FullTunnel bool
	Routes     []netip.Prefix // include routes (split tunnel)
	Exclude    []netip.Prefix // routes that must bypass the tunnel
	ServerIPs  []netip.Addr   // VPN gateway public IPs (always bypass the tunnel)
	DNS        []netip.Addr
	DNSSuffix  []string
	// DNSSplit uses VPN DNS servers only for DNSSuffix domains instead of
	// for every query (FortiClient's default is every query).
	DNSSplit bool
}

// Logf is the logging callback used by this package.
type Logf func(format string, args ...any)

// Reverter undoes an Apply.
type Reverter interface{ Revert() }

type undoStack struct {
	log   Logf
	steps []func()
}

func (u *undoStack) push(f func()) { u.steps = append(u.steps, f) }

func (u *undoStack) Revert() {
	for i := len(u.steps) - 1; i >= 0; i-- {
		u.steps[i]()
	}
	u.steps = nil
}

func run(log Logf, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	s := strings.TrimSpace(out.String())
	if err != nil {
		log("cmd: %s %s -> %v %s", name, strings.Join(args, " "), err, s)
		return s, fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, s)
	}
	log("cmd: %s %s", name, strings.Join(args, " "))
	return s, nil
}

func runInput(log Logf, input, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(input)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	s := strings.TrimSpace(out.String())
	if err != nil {
		log("cmd: %s -> %v %s", name, err, s)
		return s, fmt.Errorf("%s: %v: %s", name, err, s)
	}
	return s, nil
}

// tunnelRoutes returns the prefixes routed into the tunnel.
func tunnelRoutes(c Config) []netip.Prefix {
	if c.FullTunnel {
		return []netip.Prefix{netip.MustParsePrefix("0.0.0.0/1"), netip.MustParsePrefix("128.0.0.0/1")}
	}
	return c.Routes
}

func maskString(p netip.Prefix) string {
	bits := p.Bits()
	var m [4]byte
	for i := 0; i < 4; i++ {
		n := bits - i*8
		switch {
		case n >= 8:
			m[i] = 0xff
		case n > 0:
			m[i] = byte(0xff << (8 - n))
		}
	}
	return netip.AddrFrom4(m).String()
}
