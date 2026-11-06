//go:build !windows

package ipc

import (
	"context"
	"net"
	"os"
	"runtime"
)

// SocketPath is where the service listens.
func SocketPath() string {
	if runtime.GOOS == "linux" {
		return "/run/myvpn.sock"
	}
	return "/var/run/myvpn.sock"
}

// Listen creates the service socket, accessible only to root and ownerUID.
func Listen(ownerUID int) (net.Listener, error) {
	p := SocketPath()
	os.Remove(p)
	l, err := net.Listen("unix", p)
	if err != nil {
		return nil, err
	}
	mode := os.FileMode(0o600)
	if ownerUID < 0 {
		mode = 0o666 // no owner configured: any local user may control the VPN
	} else if ownerUID > 0 {
		os.Chown(p, ownerUID, -1)
	}
	os.Chmod(p, mode)
	return l, nil
}

func dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", SocketPath())
}
