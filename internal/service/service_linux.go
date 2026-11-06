package service

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

const (
	binPath  = "/usr/local/lib/myvpn/myvpnd"
	unitPath = "/etc/systemd/system/myvpnd.service"
)

// Install copies the running binary and registers a systemd unit.
func Install(ownerUID int) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("se requieren privilegios de administrador (usa sudo o pkexec)")
	}
	self, err := selfPath()
	if err != nil {
		return err
	}
	exec.Command("systemctl", "stop", "myvpnd").Run()
	if err := copyFile(self, binPath, 0o755); err != nil {
		return err
	}
	unit := fmt.Sprintf(`[Unit]
Description=MyVpn - cliente SSL VPN (servicio)
After=network-online.target
Wants=network-online.target

[Service]
ExecStart=%s run --owner-uid %d
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
`, binPath, ownerUID)
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	for _, args := range [][]string{{"daemon-reload"}, {"enable", "myvpnd"}, {"restart", "myvpnd"}} {
		if out, err := exec.Command("systemctl", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("systemctl %v: %v %s", args, err, out)
		}
	}
	return nil
}

// Uninstall removes the systemd unit.
func Uninstall() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("se requieren privilegios de administrador")
	}
	exec.Command("systemctl", "disable", "--now", "myvpnd").Run()
	os.Remove(unitPath)
	exec.Command("systemctl", "daemon-reload").Run()
	os.Remove(binPath)
	return nil
}

// RunAsService is a no-op on Linux (systemd runs the process directly).
func RunAsService(run func(stop <-chan struct{})) bool { return false }

// DefaultOwnerUID returns the user that invoked sudo/pkexec (or the current user).
func DefaultOwnerUID() int {
	for _, k := range []string{"SUDO_UID", "PKEXEC_UID"} {
		if s := os.Getenv(k); s != "" {
			if n, err := strconv.Atoi(s); err == nil {
				return n
			}
		}
	}
	return os.Getuid()
}
