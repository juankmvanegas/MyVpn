package forti

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const callbackPage = `<!doctype html><html lang="es"><head><meta charset="utf-8"><title>MyVpn</title>
<style>body{font-family:-apple-system,Segoe UI,Roboto,sans-serif;background:#0f172a;color:#e2e8f0;display:flex;align-items:center;justify-content:center;height:100vh;margin:0}
.c{background:#1e293b;padding:40px 48px;border-radius:16px;text-align:center}h1{margin:0 0 8px;font-size:22px;color:#4ade80}</style></head>
<body><div class="c"><h1>%s</h1><p>%s</p></div></body></html>`

// SAMLLogin runs the external-browser SAML flow:
//  1. listens on 127.0.0.1:<ssoPort>
//  2. calls openBrowser with /remote/saml/start?redirect=1
//  3. waits for the FortiGate redirect carrying ?id=
//  4. exchanges the id for the SVPNCOOKIE
func SAMLLogin(ctx context.Context, g Gateway, ssoPort int, openBrowser func(string) error) (string, error) {
	if ssoPort <= 0 {
		ssoPort = DefaultSSOPort
	}
	idCh := make(chan string, 1)
	var once sync.Once
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if id == "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprintf(w, callbackPage, "MyVpn", "Solicitud sin identificador de sesión.")
			return
		}
		fmt.Fprintf(w, callbackPage, "Autenticación completada", "Ya puedes cerrar esta pestaña y volver a MyVpn.")
		once.Do(func() { idCh <- id })
	})

	var listeners []net.Listener
	for _, host := range []string{"127.0.0.1", "::1"} {
		l, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(ssoPort)))
		if err == nil {
			listeners = append(listeners, l)
		}
	}
	if len(listeners) == 0 {
		return "", fmt.Errorf("no se pudo abrir el puerto local %d para el inicio de sesión SSO (¿FortiClient u otra app lo está usando?)", ssoPort)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	for _, l := range listeners {
		go srv.Serve(l)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()

	if err := openBrowser(g.SAMLStartURL()); err != nil {
		return "", fmt.Errorf("no se pudo abrir el navegador: %w", err)
	}

	select {
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", errors.New("tiempo de espera agotado para el inicio de sesión SSO")
		}
		return "", errors.New("inicio de sesión cancelado")
	case id := <-idCh:
		// give the browser a moment to receive the page before shutdown
		time.Sleep(300 * time.Millisecond)
		return g.ExchangeAuthID(id)
	}
}
