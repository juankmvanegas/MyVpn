// Package vpn orchestrates a FortiGate SSL VPN session: tunnel, PPP,
// TUN device, OS network configuration, keepalives and reconnection.
package vpn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.zx2c4.com/wireguard/tun"

	"myvpn/internal/forti"
	"myvpn/internal/netcfg"
	"myvpn/internal/ppp"
)

// State of the VPN.
type State string

const (
	Disconnected State = "disconnected"
	Connecting   State = "connecting"
	Connected    State = "connected"
	Reconnecting State = "reconnecting"
)

// Options for a connection.
type Options struct {
	Gateway  forti.Gateway `json:"gateway"`
	Cookie   string        `json:"cookie"`
	DNSSplit bool          `json:"dnsSplit"`
	MTU      int           `json:"mtu"`
}

// Status is reported to the UI.
type Status struct {
	State      State     `json:"state"`
	Gateway    string    `json:"gateway"`
	LocalIP    string    `json:"localIp"`
	Interface  string    `json:"interface"`
	DNS        []string  `json:"dns"`
	Routes     []string  `json:"routes"`
	FullTunnel bool      `json:"fullTunnel"`
	Since      time.Time `json:"since"`
	BytesIn    uint64    `json:"bytesIn"`
	BytesOut   uint64    `json:"bytesOut"`
	Error      string    `json:"error"`
	NeedsLogin bool      `json:"needsLogin"`
	FOSVersion string    `json:"fosVersion"`
	Reconnects int       `json:"reconnects"`
}

// Manager owns at most one active session.
type Manager struct {
	Log *LogBuffer

	mu       sync.Mutex
	status   Status
	cancel   context.CancelFunc
	done     chan struct{}
	bytesIn  atomic.Uint64
	bytesOut atomic.Uint64
}

// NewManager creates an idle manager.
func NewManager() *Manager {
	return &Manager{Log: NewLogBuffer(2000), status: Status{State: Disconnected}}
}

// Status returns a snapshot of the current status.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.status
	s.BytesIn, s.BytesOut = m.bytesIn.Load(), m.bytesOut.Load()
	return s
}

func (m *Manager) setStatus(f func(*Status)) {
	m.mu.Lock()
	f(&m.status)
	m.mu.Unlock()
}

func (m *Manager) logf(format string, args ...any) { m.Log.Printf(format, args...) }

// Connect starts a session in the background. Any existing session is stopped first.
func (m *Manager) Connect(opts Options) error {
	if opts.Gateway.Host == "" || opts.Cookie == "" {
		return errors.New("faltan el gateway o la cookie de sesión")
	}
	m.Disconnect()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	m.mu.Lock()
	m.cancel, m.done = cancel, done
	m.status = Status{State: Connecting, Gateway: opts.Gateway.Addr()}
	m.mu.Unlock()
	m.bytesIn.Store(0)
	m.bytesOut.Store(0)
	go func() {
		defer close(done)
		m.run(ctx, opts)
	}()
	return nil
}

// Disconnect stops the active session and waits for cleanup.
func (m *Manager) Disconnect() {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.cancel, m.done = nil, nil
	m.mu.Unlock()
	if cancel != nil {
		cancel()
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			m.logf("aviso: la limpieza tardó demasiado")
		}
	}
}

func (m *Manager) run(ctx context.Context, opts Options) {
	const maxRetries = 6
	retries := 0
	for {
		connectedAt, err := m.session(ctx, opts)
		if ctx.Err() != nil {
			m.logf("VPN desconectada por el usuario")
			opts.Gateway.Logout(opts.Cookie)
			m.setStatus(func(s *Status) { *s = Status{State: Disconnected} })
			return
		}
		m.logf("error de sesión: %v", err)
		var ce *forti.CertError
		fatal := errors.Is(err, forti.ErrSessionExpired) || errors.As(err, &ce)
		if !connectedAt.IsZero() && time.Since(connectedAt) > 2*time.Minute {
			retries = 0 // the link was healthy for a while: start counting again
		}
		retries++
		if fatal || retries > maxRetries {
			m.setStatus(func(s *Status) {
				*s = Status{State: Disconnected, Error: err.Error(), NeedsLogin: errors.Is(err, forti.ErrSessionExpired)}
			})
			return
		}
		wait := time.Duration(retries*retries) * time.Second
		m.setStatus(func(s *Status) {
			s.State, s.Error, s.Reconnects = Reconnecting, err.Error(), s.Reconnects+1
		})
		m.logf("reintentando en %s (intento %d/%d)", wait, retries, maxRetries)
		select {
		case <-ctx.Done():
			m.setStatus(func(s *Status) { *s = Status{State: Disconnected} })
			return
		case <-time.After(wait):
		}
	}
}

func tunName() string {
	switch runtime.GOOS {
	case "darwin":
		return "utun"
	case "windows":
		return "MyVpn"
	default:
		return "myvpn0"
	}
}

const tunOffset = 16

// session runs one tunnel until it fails or ctx is cancelled.
func (m *Manager) session(ctx context.Context, opts Options) (connectedAt time.Time, err error) {
	g := opts.Gateway
	m.logf("conectando a %s", g.Addr())
	cfg, err := g.FetchTunnelConfig(opts.Cookie)
	if err != nil {
		return time.Time{}, err
	}
	m.logf("XML del gateway: %s", strings.Join(strings.Fields(cfg.Raw), " "))
	m.logf("configuración: ip=%s dns=%v sufijos=%v rutas=%d túnel completo=%v FortiOS=%s",
		cfg.AssignedIP, cfg.DNS, cfg.DNSSuffix, len(cfg.Routes), cfg.FullTunnel(), cfg.FOSVersion)

	var serverIPs []netip.Addr
	if ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", g.Host); err == nil {
		for _, ip := range ips {
			if ip = ip.Unmap(); ip.Is4() {
				serverIPs = append(serverIPs, ip)
			}
		}
	}

	t, err := forti.OpenTunnel(g, opts.Cookie)
	if err != nil {
		return time.Time{}, err
	}
	stop := make(chan struct{})
	var stopOnce sync.Once
	closeAll := func() { stopOnce.Do(func() { close(stop); t.Close() }) }
	defer closeAll()

	mtu := opts.MTU
	if mtu <= 0 {
		mtu = 1300 // same as FortiClient ("Configure tunnel interface with MTU Size: 1300")
	}
	p := ppp.New(t, mtu, m.logf)
	var dev atomic.Pointer[tun.Device]
	errCh := make(chan error, 4)

	// Tunnel -> TUN
	go func() {
		buf := make([]byte, 65536)
		wbuf := make([]byte, 65536+tunOffset)
		for {
			frame, err := t.ReadFrame(buf)
			if err != nil {
				errCh <- fmt.Errorf("lectura del túnel: %w", err)
				return
			}
			proto, info, err := ppp.Decode(frame)
			if err != nil {
				continue
			}
			switch proto {
			case ppp.ProtoIPv4:
				p.NoteRx()
				if d := dev.Load(); d != nil && len(info) > 0 {
					n := copy(wbuf[tunOffset:], info)
					if _, err := (*d).Write([][]byte{wbuf[:tunOffset+n]}, tunOffset); err == nil {
						m.bytesIn.Add(uint64(n))
					}
				}
			case ppp.ProtoIPv6:
				p.NoteRx()
			default:
				if err := p.HandleControl(proto, info); err != nil {
					errCh <- err
					return
				}
			}
		}
	}()

	p.Start(stop)
	var res ppp.Result
	select {
	case <-ctx.Done():
		p.Terminate()
		return time.Time{}, ctx.Err()
	case err := <-errCh:
		return time.Time{}, err
	case res = <-p.Up():
	case <-time.After(30 * time.Second):
		return time.Time{}, errors.New("tiempo agotado negociando PPP con el gateway")
	}

	localIP := res.LocalIP
	if !localIP.IsValid() || localIP.IsUnspecified() {
		localIP = cfg.AssignedIP
	}
	dns := cfg.DNS
	if len(dns) == 0 {
		dns = res.DNS
	}
	if res.MRU > 0 && res.MRU < mtu {
		mtu = res.MRU
	}

	d, err := tun.CreateTUN(tunName(), mtu)
	if err != nil {
		return time.Time{}, fmt.Errorf("no se pudo crear la interfaz TUN (¿permisos de administrador?): %w", err)
	}
	defer d.Close()
	go func() {
		for range d.Events() {
		}
	}()
	ifName, _ := d.Name()
	m.logf("interfaz %s creada (MTU %d)", ifName, mtu)

	nc := netcfg.Config{
		Iface: ifName, LocalIP: localIP, MTU: mtu, FullTunnel: cfg.FullTunnel(),
		ServerIPs: serverIPs, DNS: dns, DNSSuffix: cfg.DNSSuffix,
		DNSSplit: opts.DNSSplit || (!cfg.FullTunnel() && len(cfg.DNSSuffix) > 0),
	}
	// Gateway-pushed <split-dns> domains always resolve with the VPN DNS. As
	// FortiClient does, a full tunnel also sends every other query to the VPN
	// DNS (internal-only names exist only there).
	for _, sd := range cfg.SplitDNS {
		for _, d := range sd.Domains {
			if !slices.Contains(nc.DNSSuffix, d) {
				nc.DNSSuffix = append(nc.DNSSuffix, d)
			}
		}
		if nc.DNSSplit && len(sd.Servers) > 0 {
			nc.DNS = sd.Servers
		}
	}
	m.logf("DNS: %s para %v (servidores %v)", map[bool]string{true: "dividido", false: "todo el tráfico DNS"}[nc.DNSSplit], nc.DNSSuffix, nc.DNS)
	if cfg.ExcludeMode {
		nc.Exclude = cfg.Routes
	} else {
		nc.Routes = cfg.Routes
	}
	rev, err := netcfg.Apply(nc, m.logf)
	if rev != nil {
		defer rev.Revert()
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("configurando la red: %w", err)
	}
	dev.Store(&d)

	connectedAt = time.Now()
	var routes []string
	for _, r := range cfg.Routes {
		routes = append(routes, r.String())
	}
	var dnsS []string
	for _, a := range dns {
		dnsS = append(dnsS, a.String())
	}
	m.setStatus(func(s *Status) {
		s.State, s.Error, s.NeedsLogin = Connected, "", false
		s.LocalIP, s.Interface, s.DNS, s.Routes = localIP.String(), ifName, dnsS, routes
		s.FullTunnel, s.FOSVersion, s.Gateway = cfg.FullTunnel(), cfg.FOSVersion, g.Addr()
		if s.Since.IsZero() {
			s.Since = connectedAt
		}
	})
	m.logf("VPN conectada: %s en %s", localIP, ifName)

	// TUN -> tunnel
	go func() {
		batch := d.BatchSize()
		bufs := make([][]byte, batch)
		sizes := make([]int, batch)
		for i := range bufs {
			bufs[i] = make([]byte, 65536+tunOffset)
		}
		for {
			n, err := d.Read(bufs, sizes, tunOffset)
			for i := 0; i < n; i++ {
				pkt := bufs[i][tunOffset : tunOffset+sizes[i]]
				if len(pkt) == 0 || pkt[0]>>4 != 4 {
					continue // only IPv4 is tunnelled
				}
				if err := t.WritePPP(ppp.ProtoIPv4, pkt); err != nil {
					errCh <- fmt.Errorf("escritura en el túnel: %w", err)
					return
				}
				m.bytesOut.Add(uint64(len(pkt)))
			}
			if err != nil {
				select {
				case <-stop:
				default:
					errCh <- fmt.Errorf("lectura de la interfaz: %w", err)
				}
				return
			}
		}
	}()

	ka := time.NewTicker(5 * time.Second)
	defer ka.Stop()
	stats := time.NewTicker(30 * time.Second)
	defer stats.Stop()
	defer func() { m.logf("tráfico total: recibido %d B, enviado %d B", m.bytesIn.Load(), m.bytesOut.Load()) }()
	for {
		select {
		case <-stats.C:
			m.logf("tráfico: recibido %d B, enviado %d B", m.bytesIn.Load(), m.bytesOut.Load())
		case <-ctx.Done():
			p.Terminate()
			time.Sleep(200 * time.Millisecond)
			return connectedAt, ctx.Err()
		case err := <-errCh:
			return connectedAt, err
		case <-ka.C:
			if err := p.Keepalive(15*time.Second, 4); err != nil {
				return connectedAt, err
			}
		}
	}
}
