// Package fakegw emulates a FortiGate SSL VPN gateway (SAML, XML config and a
// PPP peer that answers ICMP echo) for automated tests.
package fakegw

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"

	"myvpn/internal/ppp"
)

// Reporter is the subset of testing.T used by the fake gateway.
type Reporter interface {
	Errorf(format string, args ...any)
	Error(args ...any)
}

// TestCookie is the session cookie the fake gateway issues.
const TestCookie = "abc123cookie"

const tunnelXML = `<?xml version='1.0' encoding='utf-8'?>
<sslvpn-tunnel ver='2' dtls='1' patch='1'>
<tunnel-method value='ppp'/>
<fos platform='FG200F' major='7' minor='2' patch='10' build='1706' branch='1706'/>
<ipv4><dns ip='10.0.0.1'/><dns ip='10.0.0.2'/><dns-suffix domain='corp.example;lab.example'/>
<assigned-addr ipv4='10.212.134.200'/>
<split-tunnel-info><addr ip='10.0.0.0' mask='255.0.0.0'/><addr ip='172.16.5.0' mask='255.255.255.0'/></split-tunnel-info>
</ipv4>
<idle-timeout val='3600'/><auth-timeout val='28800'/>
</sslvpn-tunnel>`

// fakeGateway emulates the FortiGate endpoints MyVpn uses.
// Handler returns an http.Handler emulating the FortiGate endpoints MyVpn uses.
func Handler(t Reporter, ssoPort *int) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/remote/info", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "<?xml version='1.0' encoding='utf-8'?><info><api encmethod='0' salt='1' remoteauthtimeout='480' sso_port='%d' f='3cdf' /></info>", *ssoPort)
	})
	mux.HandleFunc("/remote/saml/start", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("redirect") != "1" {
			t.Errorf("saml start sin redirect=1")
		}
		// IdP login skipped: redirect straight to the local SSO listener.
		http.Redirect(w, r, fmt.Sprintf("http://127.0.0.1:%d/?id=XYZ987", *ssoPort), http.StatusFound)
	})
	mux.HandleFunc("/remote/saml/auth_id", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") != "XYZ987" {
			http.Error(w, "bad id", 403)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "SVPNCOOKIE", Value: TestCookie, Path: "/"})
		http.Redirect(w, r, "/sslvpn/portal.html", http.StatusFound)
	})
	auth := func(r *http.Request) bool {
		c, err := r.Cookie("SVPNCOOKIE")
		return err == nil && c.Value == TestCookie
	}
	mux.HandleFunc("/remote/fortisslvpn", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			http.Redirect(w, r, "/remote/login", http.StatusFound)
		}
	})
	mux.HandleFunc("/remote/fortisslvpn_xml", func(w http.ResponseWriter, r *http.Request) {
		if !auth(r) {
			http.Redirect(w, r, "/remote/login", http.StatusFound)
			return
		}
		io.WriteString(w, tunnelXML)
	})
	mux.HandleFunc("/remote/sslvpn-tunnel", func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "sslvpn" || !auth(r) {
			http.Error(w, "forbidden", 403)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		go pppPeer(t, conn, rw.Reader)
	})
	return mux
}

func frame(proto uint16, info []byte) []byte {
	p := append([]byte{0xff, 0x03, byte(proto >> 8), byte(proto)}, info...)
	h := []byte{byte((len(p) + 6) >> 8), byte(len(p) + 6), 0x50, 0x50, byte(len(p) >> 8), byte(len(p))}
	return append(h, p...)
}

func ctl(code, id byte, data []byte) []byte {
	b := []byte{code, id, 0, 0}
	binary.BigEndian.PutUint16(b[2:], uint16(4+len(data)))
	return append(b, data...)
}

// pppPeer acts as the FortiGate side of the PPP link.
func pppPeer(t Reporter, conn net.Conn, r *bufio.Reader) {
	defer conn.Close()
	// gateway LCP request with options a real FortiGate sends
	conn.Write(frame(ppp.ProtoLCP, ctl(1, 1, []byte{1, 4, 0x05, 0x4a, 5, 6, 0xde, 0xad, 0xbe, 0xef, 7, 2, 8, 2})))
	ipcpNaked := false
	for {
		var hdr [6]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return
		}
		if binary.BigEndian.Uint16(hdr[2:]) != 0x5050 {
			t.Errorf("magic incorrecto % x", hdr)
			return
		}
		n := int(binary.BigEndian.Uint16(hdr[4:]))
		body := make([]byte, n)
		io.ReadFull(r, body)
		if body[0] != 0xff || body[1] != 0x03 {
			t.Errorf("falta address/control: % x", body[:4])
		}
		proto := binary.BigEndian.Uint16(body[2:])
		info := body[4:]
		switch proto {
		case ppp.ProtoLCP:
			code, id := info[0], info[1]
			switch code {
			case 1: // client request -> ack, then start IPCP
				conn.Write(frame(ppp.ProtoLCP, ctl(2, id, info[4:])))
				conn.Write(frame(ppp.ProtoIPCP, ctl(1, 1, []byte{3, 6, 169, 254, 2, 1})))
			case 10: // echo reply
				conn.Write(frame(ppp.ProtoIPv4, TestPacket()))
			case 5:
				conn.Write(frame(ppp.ProtoLCP, ctl(6, id, nil)))
				return
			}
		case ppp.ProtoIPCP:
			code, id := info[0], info[1]
			if code != 1 {
				continue
			}
			if !ipcpNaked {
				ipcpNaked = true
				conn.Write(frame(ppp.ProtoIPCP, ctl(3, id, []byte{3, 6, 10, 212, 134, 200, 129, 6, 10, 0, 0, 1, 131, 6, 10, 0, 0, 2})))
			} else {
				conn.Write(frame(ppp.ProtoIPCP, ctl(2, id, info[4:])))
				// keepalive probe + an IPv6CP request that must be Protocol-Rejected
				conn.Write(frame(ppp.ProtoIPV6C, ctl(1, 9, []byte{1, 10, 0, 0, 0, 0, 0, 0, 0, 1})))
				conn.Write(frame(ppp.ProtoLCP, ctl(9, 7, []byte{0xde, 0xad, 0xbe, 0xef})))
			}
		case ppp.ProtoIPv4:
			conn.Write(frame(ppp.ProtoIPv4, respond(info)))
		}
	}
}

// TestPacket is a small IPv4/UDP packet addressed to the client.
func TestPacket() []byte {
	p := make([]byte, 28)
	p[0] = 0x45
	binary.BigEndian.PutUint16(p[2:], 28)
	p[9] = 17
	copy(p[12:], []byte{10, 0, 0, 5})
	copy(p[16:], []byte{10, 212, 134, 200})
	return p
}

// respond turns an ICMP echo request into a reply; other packets are echoed.
func respond(p []byte) []byte {
	out := append([]byte(nil), p...)
	if len(out) < 28 || out[0]>>4 != 4 || out[9] != 1 {
		return out
	}
	ihl := int(out[0]&0x0f) * 4
	if len(out) < ihl+8 || out[ihl] != 8 {
		return out
	}
	var src [4]byte
	copy(src[:], out[12:16])
	copy(out[12:16], out[16:20])
	copy(out[16:20], src[:])
	out[ihl] = 0 // echo reply
	out[ihl+2], out[ihl+3] = 0, 0
	binary.BigEndian.PutUint16(out[ihl+2:], checksum(out[ihl:]))
	out[10], out[11] = 0, 0
	binary.BigEndian.PutUint16(out[10:], checksum(out[:ihl]))
	return out
}

func checksum(b []byte) uint16 {
	var s uint32
	for i := 0; i+1 < len(b); i += 2 {
		s += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		s += uint32(b[len(b)-1]) << 8
	}
	for s>>16 != 0 {
		s = s&0xffff + s>>16
	}
	return ^uint16(s)
}
