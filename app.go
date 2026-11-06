package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"myvpn/internal/forti"
	"myvpn/internal/ipc"
	"myvpn/internal/login"
	"myvpn/internal/profile"
	"myvpn/internal/vpn"
)

// App is bound to the frontend.
type App struct {
	ctx    context.Context
	client *ipc.Client

	mu          sync.Mutex
	loginCancel context.CancelFunc
	phase       string // "", "auth": SAML in progress (before the service takes over)
	lastErr     string
	certFP      string
	certProfile string
}

// NewApp creates the application state.
func NewApp() *App { return &App{client: ipc.NewClient()} }

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	go startTray()
}

// Info describes the app for the about box.
type Info struct {
	Version string `json:"version"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

// GetInfo returns version and platform.
func (a *App) GetInfo() Info {
	return Info{Version: ipc.Version, OS: runtime.GOOS, Arch: runtime.GOARCH}
}

// Profiles is returned to the UI.
type Profiles struct {
	Profiles []profile.Profile `json:"profiles"`
	Last     string            `json:"last"`
}

// GetProfiles loads the saved profiles.
func (a *App) GetProfiles() (Profiles, error) {
	s, err := profile.Load()
	if err != nil {
		return Profiles{}, err
	}
	return Profiles{Profiles: s.Profiles, Last: s.Last}, nil
}

// SaveProfile creates or updates a profile.
func (a *App) SaveProfile(oldName string, p profile.Profile) error {
	s, err := profile.Load()
	if err != nil {
		return err
	}
	if err := s.Upsert(oldName, p); err != nil {
		return err
	}
	s.Last = p.Name
	return s.Save()
}

// DeleteProfile removes a profile.
func (a *App) DeleteProfile(name string) error {
	s, err := profile.Load()
	if err != nil {
		return err
	}
	return s.Delete(name)
}

// ServiceInfo describes the privileged service.
type ServiceInfo struct {
	Running  bool   `json:"running"`
	Version  string `json:"version"`
	Outdated bool   `json:"outdated"`
}

// GetService reports whether the service is reachable.
func (a *App) GetService() ServiceInfo {
	v, err := a.client.Version()
	if err != nil {
		return ServiceInfo{}
	}
	return ServiceInfo{Running: true, Version: v, Outdated: v != ipc.Version}
}

func daemonPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	name := "myvpnd"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join(filepath.Dir(exe), name)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("no se encontró %s junto a la aplicación", name)
	}
	return p, nil
}

// InstallService installs/updates the service, asking for admin rights.
func (a *App) InstallService() error {
	p, err := daemonPath()
	if err != nil {
		return err
	}
	if err := elevate(p, "install", "--owner-uid", fmt.Sprint(os.Getuid())); err != nil {
		return err
	}
	for i := 0; i < 40; i++ {
		if s := a.GetService(); s.Running && !s.Outdated {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return errors.New("el servicio se instaló pero no responde todavía; inténtalo de nuevo en unos segundos")
}

// UninstallService removes the service.
func (a *App) UninstallService() error {
	p, err := daemonPath()
	if err != nil {
		return err
	}
	return elevate(p, "uninstall")
}

// UIStatus merges service status with the local login phase.
type UIStatus struct {
	vpn.Status
	Phase          string `json:"phase"`
	ServiceRunning bool   `json:"serviceRunning"`
	LocalError     string `json:"localError"`
	CertFP         string `json:"certFingerprint"`
	CertProfile    string `json:"certProfile"`
}

// GetStatus is polled by the UI.
func (a *App) GetStatus() UIStatus {
	s, err := a.client.Status()
	a.mu.Lock()
	defer a.mu.Unlock()
	u := UIStatus{Status: s, Phase: a.phase, ServiceRunning: err == nil, LocalError: a.lastErr, CertFP: a.certFP, CertProfile: a.certProfile}
	if err != nil {
		u.State = vpn.Disconnected
	}
	return u
}

// Connect performs SSO in the browser and asks the service to connect.
func (a *App) Connect(name string) error {
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	if a.loginCancel != nil {
		a.loginCancel()
	}
	a.loginCancel, a.phase, a.lastErr, a.certFP, a.certProfile = cancel, "auth", "", "", ""
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		a.phase, a.loginCancel = "", nil
		a.mu.Unlock()
	}()
	p, err := login.Connect(ctx, a.client, name, func(u string) error {
		wruntime.BrowserOpenURL(a.ctx, u)
		return nil
	})
	if err != nil {
		a.mu.Lock()
		a.lastErr = err.Error()
		var ce *forti.CertError
		if errors.As(err, &ce) {
			a.certFP, a.certProfile = ce.Fingerprint, p.Name
		}
		a.mu.Unlock()
		return err
	}
	if a.ctx != nil {
		wruntime.WindowShow(a.ctx) // bring MyVpn back to the front after the browser
	}
	return nil
}

// CancelLogin aborts a pending SSO login.
func (a *App) CancelLogin() {
	a.mu.Lock()
	if a.loginCancel != nil {
		a.loginCancel()
	}
	a.mu.Unlock()
}

// Disconnect brings the tunnel down.
func (a *App) Disconnect() error {
	a.CancelLogin()
	return a.client.Disconnect()
}

// ClearError dismisses the last local error.
func (a *App) ClearError() {
	a.mu.Lock()
	a.lastErr, a.certFP, a.certProfile = "", "", ""
	a.mu.Unlock()
}

// TrustCertificate pins the gateway certificate fingerprint in the profile.
func (a *App) TrustCertificate(name, fp string) error {
	st, err := profile.Load()
	if err != nil {
		return err
	}
	p, ok := st.Get(name)
	if !ok {
		return fmt.Errorf("perfil %q no encontrado", name)
	}
	p.TrustedCert = fp
	a.ClearError()
	return st.Upsert(name, p)
}

// GetLogs returns recent service log lines.
func (a *App) GetLogs() []string {
	l, err := a.client.Logs(400)
	if err != nil {
		return []string{"(servicio no disponible) " + err.Error()}
	}
	return l
}
