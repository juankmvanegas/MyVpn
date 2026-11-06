// Package login implements the user-side connect flow shared by the window
// and the tray: SSO in the browser, then asking the service to connect.
package login

import (
	"context"
	"errors"
	"fmt"
	"time"

	"myvpn/internal/forti"
	"myvpn/internal/ipc"
	"myvpn/internal/profile"
	"myvpn/internal/vpn"
)

// Connect runs SSO for profile name (or the last used one when empty) and
// hands the session cookie to the service.
func Connect(ctx context.Context, c *ipc.Client, name string, openBrowser func(string) error) (profile.Profile, error) {
	st, err := profile.Load()
	if err != nil {
		return profile.Profile{}, err
	}
	if name == "" {
		name = st.Last
	}
	p, ok := st.Get(name)
	if !ok {
		if len(st.Profiles) == 0 {
			return p, errors.New("no hay conexiones configuradas")
		}
		p = st.Profiles[0]
	}
	st.Last = p.Name
	st.Save()

	if v, err := c.Version(); err != nil {
		return p, errors.New("el servicio de MyVpn no está instalado o no se está ejecutando")
	} else if v != ipc.Version {
		return p, fmt.Errorf("el servicio instalado es la versión %s y la aplicación la %s: pulsa Actualizar", v, ipc.Version)
	}
	g := forti.Gateway{Host: p.Host, Port: p.Port, TrustedCert: p.TrustedCert}
	ssoPort, err := g.Probe()
	if err != nil {
		return p, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cookie, err := forti.SAMLLogin(ctx, g, ssoPort, openBrowser)
	if err != nil {
		return p, err
	}
	return p, c.Connect(vpn.Options{Gateway: g, Cookie: cookie, DNSSplit: p.DNSSplit})
}
