// Package ipc exposes the privileged VPN service to the unprivileged GUI via
// HTTP over a Unix socket (macOS/Linux) or a named pipe (Windows).
package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"myvpn/internal/vpn"
)

// Version is the protocol/app version; the GUI reinstalls the service on mismatch.
var Version = "1.1.0"

// Serve runs the API on l until the listener is closed.
func Serve(l net.Listener, m *vpn.Manager) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"version": Version})
	})
	mux.HandleFunc("GET /v1/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, m.Status())
	})
	mux.HandleFunc("GET /v1/logs", func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("n"))
		writeJSON(w, m.Log.Tail(n))
	})
	mux.HandleFunc("POST /v1/connect", func(w http.ResponseWriter, r *http.Request) {
		var o vpn.Options
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&o); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := m.Connect(o); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, m.Status())
	})
	mux.HandleFunc("POST /v1/disconnect", func(w http.ResponseWriter, r *http.Request) {
		m.Disconnect()
		writeJSON(w, m.Status())
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	return srv.Serve(l)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// Client talks to the service.
type Client struct{ hc *http.Client }

// NewClient returns a client using the platform transport.
func NewClient() *Client {
	return &Client{hc: &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) },
		},
	}}
}

// ErrNoService means the service is not installed or not running.
var ErrNoService = errors.New("el servicio MyVpn no está en ejecución")

func (c *Client) do(method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, _ := json.Marshal(in)
		body = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, "http://myvpn"+path, body)
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrNoService, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return errors.New(string(bytes.TrimSpace(b)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Version of the running service.
func (c *Client) Version() (string, error) {
	var v map[string]string
	err := c.do(http.MethodGet, "/v1/version", nil, &v)
	return v["version"], err
}

// Status of the VPN.
func (c *Client) Status() (vpn.Status, error) {
	var s vpn.Status
	err := c.do(http.MethodGet, "/v1/status", nil, &s)
	return s, err
}

// Logs returns the last n log lines.
func (c *Client) Logs(n int) ([]string, error) {
	var l []string
	err := c.do(http.MethodGet, "/v1/logs?n="+strconv.Itoa(n), nil, &l)
	return l, err
}

// Connect asks the service to bring the tunnel up.
func (c *Client) Connect(o vpn.Options) error {
	return c.do(http.MethodPost, "/v1/connect", o, nil)
}

// Disconnect asks the service to bring the tunnel down.
func (c *Client) Disconnect() error {
	return c.do(http.MethodPost, "/v1/disconnect", nil, nil)
}
