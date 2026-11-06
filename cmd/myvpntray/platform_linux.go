package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func exeDir() string {
	exe, _ := os.Executable()
	return filepath.Dir(exe)
}

func openGUI() { exec.Command(filepath.Join(exeDir(), "MyVpn")).Start() }

func quitGUI() { exec.Command("pkill", "-x", "MyVpn").Run() }

func desktopPath() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "autostart", "myvpn-tray.desktop")
}

func autostartEnabled() bool {
	_, err := os.Stat(desktopPath())
	return err == nil
}

func setAutostart(on bool) error {
	if !on {
		return os.Remove(desktopPath())
	}
	exe, _ := os.Executable()
	os.MkdirAll(filepath.Dir(desktopPath()), 0o755)
	return os.WriteFile(desktopPath(), []byte(fmt.Sprintf(
		"[Desktop Entry]\nType=Application\nName=MyVpn (bandeja)\nExec=%s\nIcon=%s\nX-GNOME-Autostart-enabled=true\n",
		exe, filepath.Join(exeDir(), "myvpn.png"))), 0o644)
}
