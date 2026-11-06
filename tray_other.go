//go:build !darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// startTray launches MyVpnTray next to the executable; the tray itself
// enforces a single instance.
func startTray() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	name := "MyVpnTray"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	p := filepath.Join(filepath.Dir(exe), name)
	if _, err := os.Stat(p); err == nil {
		cmd := exec.Command(p)
		cmd.Dir = filepath.Dir(p)
		cmd.Start()
	}
}
