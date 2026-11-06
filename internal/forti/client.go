// Package forti implements the parts of the FortiGate SSL VPN protocol that
// MyVpn needs: SAML single sign-on (external browser flow), tunnel
// configuration download and the PPP-over-TLS tunnel transport.
//
// Protocol facts were recovered from the installed FortiClient 7.4 (see
// docs/protocolo.md):
//   - SAML start:   GET /remote/saml/start?redirect=1
//   - SSO callback: http://127.0.0.1:8020/?id=<id>   (sso_port from /remote/info)
//   - id exchange:  GET /remote/saml/auth_id?id=<id> -> Set-Cookie: SVPNCOOKIE
//   - config:       GET /remote/fortisslvpn, GET /remote/fortisslvpn_xml
//   - tunnel:       GET /remote/sslvpn-tunnel HTTP/1.1, Host: sslvpn, Cookie: SVPNCOOKIE=...
//   - framing:      6 byte header [total len][0x5050][payload len] + PPP frame
package forti

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// UserAgent mimics the official client; some FortiGates tailor responses to it.
const UserAgent = "FortiSSLVPN (Mac OS X; SV1 [SV{v=02.01; f=07;}])"

// DefaultSSOPort is the local port the FortiGate redirects the browser to after SAML.
const DefaultSSOPort = 8020

// Gateway identifies a FortiGate SSL VPN endpoint.
type Gateway struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	// TrustedCert is an optional SHA-256 fingerprint (hex) of the gateway
	// certificate. When set, it is accepted even if it does not chain to a
	// trusted root (useful for self-signed gateways).
	TrustedCert string `json:"trustedCert,omitempty"`
}

// Addr returns host:port.
func (g Gateway) Addr() string {
	port := g.Port
	if port == 0 {
		port = 443
	}
	return net.JoinHostPort(g.Host, strconv.Itoa(port))
}

// BaseURL returns https://host:port.
func (g Gateway) BaseURL() string { return "https://" + g.Addr() }

// SAMLStartURL is the URL to open in the user's browser.
func (g Gateway) SAMLStartURL() string { return g.BaseURL() + "/remote/saml/start?redirect=1" }

// CertError is returned when the gateway certificate is not trusted. It carries
// the fingerprint so the UI can offer to trust it explicitly.
type CertError struct {
	Fingerprint string
	Subject     string
	Err         error
}

func (e *CertError) Error() string {
	return fmt.Sprintf("certificado del gateway no confiable (%s, sha256=%s): %v", e.Subject, e.Fingerprint, e.Err)
}

// TLSConfig builds a TLS configuration that verifies the gateway normally and
// additionally accepts a pinned fingerprint.
func (g Gateway) TLSConfig() *tls.Config {
	pinned := strings.ToLower(strings.ReplaceAll(g.TrustedCert, ":", ""))
	return &tls.Config{
		ServerName:         g.Host,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // verification is done below so we can pin
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("el gateway no presentó certificado")
			}
			leaf := cs.PeerCertificates[0]
			sum := sha256.Sum256(leaf.Raw)
			fp := hex.EncodeToString(sum[:])
			if pinned != "" && fp == pinned {
				return nil
			}
			inter := x509.NewCertPool()
			for _, c := range cs.PeerCertificates[1:] {
				inter.AddCert(c)
			}
			_, err := leaf.Verify(x509.VerifyOptions{DNSName: g.Host, Intermediates: inter})
			if err != nil {
				return &CertError{Fingerprint: fp, Subject: leaf.Subject.CommonName, Err: err}
			}
			return nil
		},
	}
}

// HTTPClient returns a client that talks to the gateway without following redirects.
func (g Gateway) HTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig:     g.TLSConfig(),
			Proxy:               http.ProxyFromEnvironment,
			TLSHandshakeTimeout: 15 * time.Second,
			DisableKeepAlives:   true,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (g Gateway) get(path, cookie string) (*http.Response, []byte, error) {
	req, err := http.NewRequest(http.MethodGet, g.BaseURL()+path, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", UserAgent)
	if cookie != "" {
		req.Header.Set("Cookie", "SVPNCOOKIE="+cookie)
	}
	resp, err := g.HTTPClient().Do(req)
	if err != nil {
		var ce *CertError
		if errors.As(err, &ce) {
			return nil, nil, ce
		}
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp, body, err
}

// Probe checks that the gateway is reachable and returns the SSO port it
// advertises in /remote/info (defaults to 8020).
func (g Gateway) Probe() (ssoPort int, err error) {
	resp, body, err := g.get("/remote/info", "")
	if err != nil {
		return 0, err
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("/remote/info devolvió HTTP %d", resp.StatusCode)
	}
	ssoPort = DefaultSSOPort
	if i := strings.Index(string(body), "sso_port='"); i >= 0 {
		rest := string(body)[i+len("sso_port='"):]
		if j := strings.IndexByte(rest, '\''); j > 0 {
			if p, err := strconv.Atoi(rest[:j]); err == nil && p > 0 && p < 65536 {
				ssoPort = p
			}
		}
	}
	return ssoPort, nil
}

// ExchangeAuthID trades the id received on the SSO callback for an SVPNCOOKIE.
func (g Gateway) ExchangeAuthID(id string) (string, error) {
	resp, _, err := g.get("/remote/saml/auth_id?id="+urlQueryEscape(id), "")
	if err != nil {
		return "", err
	}
	for _, c := range resp.Cookies() {
		if c.Name == "SVPNCOOKIE" && c.Value != "" {
			return c.Value, nil
		}
	}
	return "", fmt.Errorf("el gateway no entregó SVPNCOOKIE (HTTP %d)", resp.StatusCode)
}

// Logout invalidates the session cookie on the gateway.
func (g Gateway) Logout(cookie string) {
	if cookie == "" {
		return
	}
	_, _, _ = g.get("/remote/logout", cookie)
}

func urlQueryEscape(s string) string {
	var b strings.Builder
	for _, r := range []byte(s) {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == '~' {
			b.WriteByte(r)
		} else {
			fmt.Fprintf(&b, "%%%02X", r)
		}
	}
	return b.String()
}
