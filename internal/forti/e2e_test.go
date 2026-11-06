package forti_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"myvpn/internal/fakegw"
	"myvpn/internal/forti"
	"myvpn/internal/ppp"
)

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func fakeGateway(t *testing.T, ssoPort *int) *httptest.Server {
	return httptest.NewTLSServer(fakegw.Handler(t, ssoPort))
}

func gatewayFor(t *testing.T, srv *httptest.Server) forti.Gateway {
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())
	sum := sha256.Sum256(srv.Certificate().Raw)
	return forti.Gateway{Host: "127.0.0.1", Port: port, TrustedCert: hex.EncodeToString(sum[:])}
}

func TestEndToEnd(t *testing.T) {
	ssoPort := freePort(t)
	srv := fakeGateway(t, &ssoPort)
	defer srv.Close()
	g := gatewayFor(t, srv)

	// 1. probe
	port, err := g.Probe()
	if err != nil || port != ssoPort {
		t.Fatalf("probe: %v %d", err, port)
	}

	// 2. SAML: the "browser" follows the gateway redirect to our listener
	browser := &http.Client{Transport: &http.Transport{TLSClientConfig: g.TLSConfig()}}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cookie, err := forti.SAMLLogin(ctx, g, port, func(u string) error {
		go func() {
			resp, err := browser.Get(u)
			if err != nil {
				t.Errorf("browser: %v", err)
				return
			}
			b, _ := io.ReadAll(resp.Body)
			if !strings.Contains(string(b), "Autenticación completada") {
				t.Errorf("página de callback inesperada: %s", b)
			}
		}()
		return nil
	})
	if err != nil || cookie != fakegw.TestCookie {
		t.Fatalf("saml: %v %q", err, cookie)
	}

	// 3. config
	cfg, err := g.FetchTunnelConfig(cookie)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AssignedIP != netip.MustParseAddr("10.212.134.200") || len(cfg.DNS) != 2 || len(cfg.Routes) != 2 ||
		cfg.Routes[1].String() != "172.16.5.0/24" || cfg.FullTunnel() || len(cfg.DNSSuffix) != 2 || cfg.FOSVersion != "7.2.10" {
		t.Fatalf("config inesperada: %+v", cfg)
	}
	if _, err := g.FetchTunnelConfig("bad"); err != forti.ErrSessionExpired {
		t.Fatalf("cookie inválida debería expirar: %v", err)
	}

	// 4. tunnel + PPP
	tun, err := forti.OpenTunnel(g, cookie)
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Close()
	p := ppp.New(tun, 1354, t.Logf)
	stop := make(chan struct{})
	defer close(stop)
	got := make(chan []byte, 4)
	go func() {
		buf := make([]byte, 65536)
		for {
			f, err := tun.ReadFrame(buf)
			if err != nil {
				return
			}
			proto, info, _ := ppp.Decode(f)
			if proto == ppp.ProtoIPv4 {
				got <- append([]byte(nil), info...)
				continue
			}
			p.HandleControl(proto, info)
		}
	}()
	p.Start(stop)
	var res ppp.Result
	select {
	case res = <-p.Up():
	case <-time.After(5 * time.Second):
		t.Fatal("PPP no se estableció")
	}
	if res.LocalIP.String() != "10.212.134.200" || len(res.DNS) != 2 || res.PeerIP.String() != "169.254.2.1" {
		t.Fatalf("resultado PPP: %v", res)
	}
	// the echo request from the peer is answered; the peer then sends a packet
	select {
	case pkt := <-got:
		if len(pkt) != 28 || pkt[0] != 0x45 {
			t.Fatalf("paquete inesperado % x", pkt)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no llegó el paquete IPv4 tras el keepalive")
	}
	// client -> gateway -> echoed back
	if err := tun.WritePPP(ppp.ProtoIPv4, fakegw.TestPacket()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("no volvió el paquete enviado")
	}
}

func TestTunnelRejectsBadCookie(t *testing.T) {
	ssoPort := freePort(t)
	srv := fakeGateway(t, &ssoPort)
	defer srv.Close()
	g := gatewayFor(t, srv)
	tun, err := forti.OpenTunnel(g, "bad")
	if err != nil {
		t.Fatal(err)
	}
	defer tun.Close()
	if _, err := tun.ReadFrame(make([]byte, 2048)); err != forti.ErrSessionExpired {
		t.Fatalf("esperaba ErrSessionExpired, obtuve %v", err)
	}
}

func TestUntrustedCert(t *testing.T) {
	ssoPort := freePort(t)
	srv := fakeGateway(t, &ssoPort)
	defer srv.Close()
	g := gatewayFor(t, srv)
	g.TrustedCert = ""
	_, err := g.Probe()
	var ce *forti.CertError
	if err == nil || !strings.Contains(err.Error(), "no confiable") {
		t.Fatalf("esperaba error de certificado: %v", err)
	}
	_ = ce
}

func TestParseFullTunnelAndLegacy(t *testing.T) {
	cfg, err := forti.ParseTunnelConfig([]byte(`<?xml version='1.0'?><sslvpn-tunnel><assigned-addr ipv4='10.1.1.1'/><ipv4><dns ip='1.1.1.1'/></ipv4></sslvpn-tunnel>`))
	if err != nil || !cfg.FullTunnel() || cfg.AssignedIP.String() != "10.1.1.1" {
		t.Fatalf("%v %+v", err, cfg)
	}
	cfg, _ = forti.ParseTunnelConfig([]byte(`<sslvpn-tunnel><ipv4><split-tunnel-info negate='1'><addr ip='192.168.0.0' mask='255.255.0.0'/></split-tunnel-info></ipv4></sslvpn-tunnel>`))
	if !cfg.ExcludeMode || !cfg.FullTunnel() || cfg.Routes[0].String() != "192.168.0.0/16" {
		t.Fatalf("%+v", cfg)
	}
}

// Document shape returned by a FortiOS 7.x gateway configured for full
// tunnel with gateway-pushed split DNS (values anonymised).
func TestParseSplitDNSFullTunnel(t *testing.T) {
	x := `<?xml version='1.0' encoding='utf-8'?><sslvpn-tunnel ver='2' dtls='1' patch='1'><dtls-config ver='2' heartbeat-interval='10' heartbeat-fail-count='3' heartbeat-idle-timeout='10' client-hello-timeout='10' dtls-accept-check-time ='1'/><tunnel-method value='ppp' /><tunnel-method value='tun' /><tunnel-method value='websocket' /><auth-ses check-src-ip='1' tun-connect-without-reauth='0' tun-user-ses-timeout='30' /><client-config save-password='off' keep-alive='off' auto-connect='off' /><ipv4><split-dns domains='corp.example' dnsserver1='10.20.0.12' dnsserver2='10.20.0.11' /><dns domain='corp.example' /><dns ip='10.20.0.12' /><dns ip='10.20.0.11' /><assigned-addr ipv4='10.30.0.58' /></ipv4><idle-timeout val='3600' /><auth-timeout val='28800' /></sslvpn-tunnel>`
	cfg, err := forti.ParseTunnelConfig([]byte(x))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.FullTunnel() || cfg.AssignedIP.String() != "10.30.0.58" || len(cfg.DNS) != 2 {
		t.Fatalf("%+v", cfg)
	}
	if len(cfg.SplitDNS) != 1 || cfg.SplitDNS[0].Domains[0] != "corp.example" || len(cfg.SplitDNS[0].Servers) != 2 ||
		cfg.SplitDNS[0].Servers[0].String() != "10.20.0.12" {
		t.Fatalf("split-dns: %+v", cfg.SplitDNS)
	}
}
