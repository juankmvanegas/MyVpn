package forti

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Tunnel is an established PPP-over-TLS transport. Each frame carries one PPP
// packet (address/control + protocol + information).
type Tunnel struct {
	conn *tls.Conn
	r    *bufio.Reader
	wmu  sync.Mutex
	wbuf []byte
}

// OpenTunnel connects to the gateway and requests the SSL VPN tunnel.
func OpenTunnel(g Gateway, cookie string) (*Tunnel, error) {
	d := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 20 * time.Second}
	raw, err := d.Dial("tcp", g.Addr())
	if err != nil {
		return nil, err
	}
	conn := tls.Client(raw, g.TLSConfig())
	conn.SetDeadline(time.Now().Add(20 * time.Second))
	if err := conn.Handshake(); err != nil {
		raw.Close()
		var ce *CertError
		if errors.As(err, &ce) {
			return nil, ce
		}
		return nil, err
	}
	// Exactly what FortiClient's network extension sends (Host is literally "sslvpn").
	req := "GET /remote/sslvpn-tunnel HTTP/1.1\r\nHost: sslvpn\r\nUser-Agent: " + UserAgent + "\r\nCookie: SVPNCOOKIE=" + cookie + "\r\n\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		conn.Close()
		return nil, err
	}
	conn.SetDeadline(time.Time{})
	return &Tunnel{conn: conn, r: bufio.NewReaderSize(conn, 64<<10), wbuf: make([]byte, 0, 2048)}, nil
}

// CheckHTTPError peeks at the stream: if the gateway answered with HTTP
// instead of PPP, the cookie was rejected.
func (t *Tunnel) CheckHTTPError(timeout time.Duration) error {
	t.conn.SetReadDeadline(time.Now().Add(timeout))
	defer t.conn.SetReadDeadline(time.Time{})
	b, err := t.r.Peek(5)
	if err != nil {
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			return nil // nothing yet: server waits for our LCP request, which is fine
		}
		return err
	}
	if bytes.Equal(b, []byte("HTTP/")) {
		line, _ := t.r.ReadString('\n')
		return fmt.Errorf("%w (%s)", ErrSessionExpired, bytes.TrimSpace([]byte(line)))
	}
	return nil
}

// ReadFrame returns the next PPP packet. The returned slice is only valid
// until the next call.
func (t *Tunnel) ReadFrame(buf []byte) ([]byte, error) {
	var hdr [6]byte
	if _, err := io.ReadFull(t.r, hdr[:]); err != nil {
		return nil, err
	}
	total := int(binary.BigEndian.Uint16(hdr[0:2]))
	magic := binary.BigEndian.Uint16(hdr[2:4])
	plen := int(binary.BigEndian.Uint16(hdr[4:6]))
	if magic != 0x5050 || total != plen+6 {
		if bytes.HasPrefix(hdr[:], []byte("HTTP/")) {
			return nil, ErrSessionExpired
		}
		return nil, fmt.Errorf("trama inválida del gateway (% x)", hdr)
	}
	if plen > len(buf) {
		buf = make([]byte, plen)
	}
	if _, err := io.ReadFull(t.r, buf[:plen]); err != nil {
		return nil, err
	}
	return buf[:plen], nil
}

// WritePPP sends one PPP packet with uncompressed address/control and protocol fields.
func (t *Tunnel) WritePPP(proto uint16, info []byte) error {
	t.wmu.Lock()
	defer t.wmu.Unlock()
	n := len(info) + 4
	b := t.wbuf[:0]
	b = append(b, byte((n+6)>>8), byte(n+6), 0x50, 0x50, byte(n>>8), byte(n))
	b = append(b, 0xff, 0x03, byte(proto>>8), byte(proto))
	b = append(b, info...)
	t.wbuf = b
	t.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	_, err := t.conn.Write(b)
	return err
}

// Close tears the connection down.
func (t *Tunnel) Close() error { return t.conn.Close() }
