// Command myvpntray is MyVpn's notification-area icon (macOS menu bar,
// Windows system tray, Linux AppIndicator). It keeps running after the main
// window is closed, like FortiClient's FortiTray.
package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"net"
	"os"
	"runtime"
	"sync"
	"time"

	"fyne.io/systray"

	"myvpn/internal/browser"
	"myvpn/internal/ipc"
	"myvpn/internal/login"
	"myvpn/internal/vpn"
)

//go:embed icons
var icons embed.FS

func icon(name string) []byte {
	b, _ := icons.ReadFile("icons/" + name)
	return b
}

// lockAddr doubles as a single-instance lock and a "show the menu" no-op.
const lockAddr = "127.0.0.1:47811"

func main() {
	l, err := net.Listen("tcp", lockAddr)
	if err != nil {
		return // another tray instance is already running
	}
	defer l.Close()
	t := &tray{client: ipc.NewClient()}
	systray.Run(t.onReady, func() {})
}

type tray struct {
	client *ipc.Client

	mu        sync.Mutex
	authing   bool
	lastErr   string
	lastState string
	cancel    context.CancelFunc

	mStatus, mConnect, mDisconnect, mOpen, mAutostart, mQuit *systray.MenuItem
}

func (t *tray) setIcon(connected bool) {
	st := "off"
	if connected {
		st = "on"
	}
	switch runtime.GOOS {
	case "darwin":
		systray.SetTemplateIcon(icon("template_"+st+".png"), icon("color_"+st+".png"))
	case "windows":
		systray.SetIcon(icon("color_" + st + ".ico"))
	default:
		systray.SetIcon(icon("color_" + st + ".png"))
	}
}

func (t *tray) onReady() {
	t.setIcon(false)
	systray.SetTooltip("MyVpn")
	t.mStatus = systray.AddMenuItem("MyVpn: desconectado", "")
	t.mStatus.Disable()
	systray.AddSeparator()
	t.mConnect = systray.AddMenuItem("Conectar", "Iniciar sesión y conectar la VPN")
	t.mDisconnect = systray.AddMenuItem("Desconectar", "Desconectar la VPN")
	t.mDisconnect.Hide()
	systray.AddSeparator()
	t.mOpen = systray.AddMenuItem("Abrir MyVpn", "Mostrar la ventana de MyVpn")
	t.mAutostart = systray.AddMenuItemCheckbox("Iniciar con el sistema", "Mostrar este icono al iniciar sesión", autostartEnabled())
	systray.AddSeparator()
	t.mQuit = systray.AddMenuItem("Salir de MyVpn", "Desconectar y cerrar MyVpn")

	firstRunAutostart(t.mAutostart)
	systray.SetOnTapped(openGUI) // Windows/Linux: left click opens the window

	go t.loop()
	go t.refreshLoop()
}

func (t *tray) loop() {
	for {
		select {
		case <-t.mConnect.ClickedCh:
			go t.connect()
		case <-t.mDisconnect.ClickedCh:
			t.mu.Lock()
			if t.cancel != nil {
				t.cancel()
			}
			t.mu.Unlock()
			go func() { t.client.Disconnect(); t.refresh() }()
		case <-t.mOpen.ClickedCh:
			openGUI()
		case <-t.mAutostart.ClickedCh:
			if t.mAutostart.Checked() {
				if err := setAutostart(false); err == nil {
					t.mAutostart.Uncheck()
				}
			} else if err := setAutostart(true); err == nil {
				t.mAutostart.Check()
			}
		case <-t.mQuit.ClickedCh:
			t.client.Disconnect()
			quitGUI()
			systray.Quit()
			os.Exit(0)
		}
	}
}

func (t *tray) connect() {
	ctx, cancel := context.WithCancel(context.Background())
	t.mu.Lock()
	t.authing, t.lastErr, t.cancel = true, "", cancel
	t.mu.Unlock()
	t.refresh()
	_, err := login.Connect(ctx, t.client, "", browser.Open)
	t.mu.Lock()
	t.authing, t.cancel = false, nil
	if err != nil && ctx.Err() == nil {
		t.lastErr = err.Error()
		log.Print(err)
	}
	t.mu.Unlock()
	cancel()
	t.refresh()
}

func (t *tray) refreshLoop() {
	for {
		t.refresh()
		time.Sleep(2 * time.Second)
	}
}

func (t *tray) refresh() {
	s, err := t.client.Status()
	t.mu.Lock()
	authing, lastErr := t.authing, t.lastErr
	t.mu.Unlock()

	text, connected, busy := "MyVpn: desconectado", false, authing
	switch {
	case err != nil:
		text = "MyVpn: servicio no disponible"
	case authing:
		text = "MyVpn: inicia sesión en el navegador…"
	case s.State == vpn.Connected:
		text, connected = fmt.Sprintf("MyVpn: conectado (%s)", s.LocalIP), true
	case s.State == vpn.Connecting:
		text, busy = "MyVpn: conectando…", true
	case s.State == vpn.Reconnecting:
		text, busy = "MyVpn: reconectando…", true
	case s.Error != "":
		lastErr = s.Error
	}
	if !connected && !busy && lastErr != "" {
		text = "MyVpn: " + truncate(lastErr, 60)
	}
	t.mStatus.SetTitle(text)
	systray.SetTooltip(text)
	if connected || busy {
		t.mConnect.Hide()
		t.mDisconnect.Show()
		if authing {
			t.mDisconnect.SetTitle("Cancelar")
		} else {
			t.mDisconnect.SetTitle("Desconectar")
		}
	} else {
		t.mDisconnect.Hide()
		t.mConnect.Show()
		if err != nil {
			t.mConnect.Disable()
		} else {
			t.mConnect.Enable()
		}
	}
	state := fmt.Sprint(connected)
	if state != t.lastState {
		t.lastState = state
		t.setIcon(connected)
	}
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
