package ipc

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
)

// PipeName is the named pipe the service listens on.
const PipeName = `\\.\pipe\myvpn`

// Listen creates the named pipe. SYSTEM and Administrators get full control,
// interactively logged-on users may read/write (control the VPN).
func Listen(ownerUID int) (net.Listener, error) {
	return winio.ListenPipe(PipeName, &winio.PipeConfig{
		SecurityDescriptor: "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;IU)",
	})
}

func dial(ctx context.Context) (net.Conn, error) {
	return winio.DialPipeContext(ctx, PipeName)
}
