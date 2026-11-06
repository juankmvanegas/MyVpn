package service

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func installDir() string {
	pf := os.Getenv("ProgramFiles")
	if pf == "" {
		pf = `C:\Program Files`
	}
	return filepath.Join(pf, "MyVpn")
}

func isAdmin() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// Install copies myvpnd.exe (and wintun.dll) to Program Files and registers
// an auto-start Windows service.
func Install(ownerUID int) error {
	if !isAdmin() {
		return fmt.Errorf("se requieren privilegios de administrador")
	}
	self, err := selfPath()
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	if s, err := m.OpenService(Name); err == nil {
		stopService(s)
		s.Delete()
		s.Close()
		time.Sleep(time.Second)
	}
	dir := installDir()
	exe := filepath.Join(dir, "myvpnd.exe")
	if err := copyFile(self, exe, 0o755); err != nil {
		return err
	}
	dll := filepath.Join(filepath.Dir(self), "wintun.dll")
	if _, err := os.Stat(dll); err == nil {
		if err := copyFile(dll, filepath.Join(dir, "wintun.dll"), 0o644); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("falta wintun.dll junto a %s", self)
	}
	s, err := m.CreateService(Name, exe, mgr.Config{
		DisplayName:  "MyVpn",
		Description:  "Servicio del cliente SSL VPN MyVpn",
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
	}, "run")
	if err != nil {
		return err
	}
	defer s.Close()
	s.SetRecoveryActions([]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: 2 * time.Second}}, 60)
	return s.Start()
}

func stopService(s *mgr.Service) {
	st, err := s.Control(svc.Stop)
	if err != nil {
		return
	}
	for i := 0; i < 50 && st.State != svc.Stopped; i++ {
		time.Sleep(200 * time.Millisecond)
		st, err = s.Query()
		if err != nil {
			return
		}
	}
}

// Uninstall stops and removes the service.
func Uninstall() error {
	if !isAdmin() {
		return fmt.Errorf("se requieren privilegios de administrador")
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	if s, err := m.OpenService(Name); err == nil {
		stopService(s)
		s.Delete()
		s.Close()
	}
	time.Sleep(time.Second)
	os.RemoveAll(installDir())
	return nil
}

type handler struct{ run func(stop <-chan struct{}) }

func (h handler) Execute(_ []string, req <-chan svc.ChangeRequest, st chan<- svc.Status) (bool, uint32) {
	st <- svc.Status{State: svc.StartPending}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { h.run(stop); close(done) }()
	st <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				st <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				st <- svc.Status{State: svc.StopPending}
				close(stop)
				select {
				case <-done:
				case <-time.After(20 * time.Second):
				}
				return false, 0
			}
		case <-done:
			return false, 0
		}
	}
}

// RunAsService runs run under the Windows service manager when started by
// it and reports true; otherwise it returns false immediately.
func RunAsService(run func(stop <-chan struct{})) bool {
	ok, err := svc.IsWindowsService()
	if err != nil || !ok {
		return false
	}
	svc.Run(Name, handler{run: run})
	return true
}

// DefaultOwnerUID is unused on Windows (the pipe ACL grants interactive users).
func DefaultOwnerUID() int { return 0 }
