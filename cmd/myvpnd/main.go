// Command myvpnd is MyVpn's privileged service and command-line client.
//
//	myvpnd install            install & start the background service (admin)
//	myvpnd uninstall          remove the service (admin)
//	myvpnd run                run the service in the foreground (admin)
//	myvpnd connect [-profile NOMBRE] [-foreground]
//	myvpnd disconnect | status | logs | version
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"myvpn/internal/browser"
	"myvpn/internal/forti"
	"myvpn/internal/ipc"
	"myvpn/internal/profile"
	"myvpn/internal/service"
	"myvpn/internal/vpn"
)

func main() {
	log.SetFlags(log.LstdFlags)
	if service.RunAsService(runService(-1)) {
		return
	}
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "run":
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		uid := fs.Int("owner-uid", -1, "uid autorizado a controlar el servicio")
		fs.Parse(args)
		stop := make(chan struct{})
		go func() {
			c := make(chan os.Signal, 1)
			signal.Notify(c, os.Interrupt, syscall.SIGTERM)
			<-c
			close(stop)
		}()
		runService(*uid)(stop)
	case "install":
		fs := flag.NewFlagSet("install", flag.ExitOnError)
		uid := fs.Int("owner-uid", service.DefaultOwnerUID(), "uid autorizado a controlar el servicio")
		fs.Parse(args)
		err = service.Install(*uid)
		if err == nil {
			fmt.Println("Servicio MyVpn instalado y en ejecución.")
		}
	case "uninstall":
		err = service.Uninstall()
		if err == nil {
			fmt.Println("Servicio MyVpn eliminado.")
		}
	case "connect":
		err = connect(args)
	case "fetch-config":
		err = fetchConfig(args)
	case "disconnect":
		err = ipc.NewClient().Disconnect()
	case "status":
		var s vpn.Status
		s, err = ipc.NewClient().Status()
		if err == nil {
			b, _ := json.MarshalIndent(s, "", "  ")
			fmt.Println(string(b))
		}
	case "logs":
		var l []string
		l, err = ipc.NewClient().Logs(200)
		fmt.Println(strings.Join(l, "\n"))
	case "version":
		fmt.Println("MyVpn", ipc.Version)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `MyVpn %s - cliente SSL VPN para FortiGate con SAML

Uso:
  myvpnd install              instala el servicio en segundo plano (requiere administrador)
  myvpnd uninstall            elimina el servicio (requiere administrador)
  myvpnd connect [opciones]   inicia sesión SSO en el navegador y conecta
      -profile NOMBRE         perfil guardado (por defecto: el último usado)
      -host HOST -port PUERTO gateway explícito
      -cookie VALOR           usar una SVPNCOOKIE existente (sin navegador)
      -trust-cert SHA256      aceptar este certificado del gateway (autofirmado)
      -foreground             no usar el servicio: conectar en este proceso (requiere administrador)
  myvpnd fetch-config [-profile N]  diagnóstico: inicia sesión y muestra la configuración del
                              gateway sin crear el túnel ni tocar la red
  myvpnd disconnect           desconecta
  myvpnd status | logs        estado / registro del servicio
  myvpnd run                  ejecuta el servicio en primer plano (lo usa launchd/systemd)
`, ipc.Version)
}

func runService(ownerUID int) func(stop <-chan struct{}) {
	return func(stop <-chan struct{}) {
		m := vpn.NewManager()
		l, err := ipc.Listen(ownerUID)
		if err != nil {
			log.Fatalf("no se pudo abrir el canal de control: %v", err)
		}
		m.Log.Printf("servicio MyVpn %s iniciado", ipc.Version)
		go ipc.Serve(l, m)
		<-stop
		m.Log.Printf("deteniendo servicio")
		m.Disconnect()
		l.Close()
	}
}

func connect(args []string) error {
	fs := flag.NewFlagSet("connect", flag.ExitOnError)
	name := fs.String("profile", "", "perfil")
	host := fs.String("host", "", "gateway")
	port := fs.Int("port", 443, "puerto")
	cookie := fs.String("cookie", "", "SVPNCOOKIE existente")
	trust := fs.String("trust-cert", "", "huella SHA-256 del certificado del gateway a aceptar")
	fg := fs.Bool("foreground", false, "conectar en este proceso")
	fs.Parse(args)

	var p profile.Profile
	if *host != "" {
		p = profile.Profile{Name: *host, Host: *host, Port: *port}
	} else {
		st, err := profile.Load()
		if err != nil {
			return err
		}
		n := *name
		if n == "" {
			n = st.Last
		}
		var ok bool
		if p, ok = st.Get(n); !ok {
			if len(st.Profiles) == 0 {
				return errors.New("no hay conexiones guardadas: crea una en la aplicación o usa -host y -port")
			}
			p = st.Profiles[0]
		}
	}
	if *trust != "" {
		p.TrustedCert = *trust
	}
	g := forti.Gateway{Host: p.Host, Port: p.Port, TrustedCert: p.TrustedCert}

	client := ipc.NewClient()
	if !*fg {
		if _, err := client.Version(); err != nil {
			return fmt.Errorf("%v\nInstala el servicio con 'sudo myvpnd install' o usa -foreground", err)
		}
	}

	if *cookie == "" {
		fmt.Printf("Comprobando %s ...\n", g.Addr())
		ssoPort, err := g.Probe()
		if err != nil {
			return err
		}
		fmt.Println("Se abrirá el navegador para iniciar sesión (usuario, contraseña y código de doble factor).")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		*cookie, err = forti.SAMLLogin(ctx, g, ssoPort, func(u string) error {
			fmt.Println("Si el navegador no se abre, visita:\n  " + u)
			if err := browser.Open(u); err != nil {
				fmt.Println("(no se pudo abrir un navegador; abre la dirección anterior manualmente)")
			}
			return nil
		})
		if err != nil {
			return err
		}
		fmt.Println("Autenticación correcta.")
	}
	opts := vpn.Options{Gateway: g, Cookie: *cookie, DNSSplit: p.DNSSplit}

	if !*fg {
		if err := client.Connect(opts); err != nil {
			return err
		}
		return waitConnected(func() (vpn.Status, error) { return client.Status() })
	}

	m := vpn.NewManager()
	if err := m.Connect(opts); err != nil {
		return err
	}
	if err := waitConnected(func() (vpn.Status, error) { return m.Status(), nil }); err != nil {
		m.Disconnect()
		return err
	}
	fmt.Println("Pulsa Ctrl+C para desconectar.")
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
	fmt.Println("Desconectando...")
	m.Disconnect()
	return nil
}

func waitConnected(status func() (vpn.Status, error)) error {
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		s, err := status()
		if err != nil {
			return err
		}
		switch s.State {
		case vpn.Connected:
			fmt.Printf("Conectado: IP %s en %s (DNS %v)\n", s.LocalIP, s.Interface, s.DNS)
			return nil
		case vpn.Disconnected:
			if s.Error != "" {
				return errors.New(s.Error)
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return errors.New("tiempo agotado esperando la conexión")
}

func fetchConfig(args []string) error {
	fs := flag.NewFlagSet("fetch-config", flag.ExitOnError)
	name := fs.String("profile", "", "perfil")
	fs.Parse(args)
	st, err := profile.Load()
	if err != nil {
		return err
	}
	n := *name
	if n == "" {
		n = st.Last
	}
	p, ok := st.Get(n)
	if !ok {
		if len(st.Profiles) == 0 {
			return errors.New("no hay conexiones guardadas")
		}
		p = st.Profiles[0]
	}
	g := forti.Gateway{Host: p.Host, Port: p.Port, TrustedCert: p.TrustedCert}
	ssoPort, err := g.Probe()
	if err != nil {
		return err
	}
	fmt.Println("Se abrirá el navegador para iniciar sesión...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cookie, err := forti.SAMLLogin(ctx, g, ssoPort, browser.Open)
	if err != nil {
		return err
	}
	defer g.Logout(cookie)
	cfg, err := g.FetchTunnelConfig(cookie)
	if cfg != nil {
		fmt.Println("----- XML del gateway -----")
		fmt.Println(cfg.Raw)
		fmt.Println("----- interpretación -----")
		fmt.Printf("IP=%s DNS=%v sufijos=%v rutas=%v exclusión=%v túnel completo=%v\n",
			cfg.AssignedIP, cfg.DNS, cfg.DNSSuffix, cfg.Routes, cfg.ExcludeMode, cfg.FullTunnel())
	}
	return err
}
