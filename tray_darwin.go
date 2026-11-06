package main

import (
	"os"
	"os/exec"
	"path/filepath"
)

// startTray launches the menu-bar helper bundled at
// MyVpn.app/Contents/Library/MyVpnTray.app (no-op if it is already running).
func startTray() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	exe, _ = filepath.EvalSymlinks(exe)
	app := filepath.Join(filepath.Dir(filepath.Dir(exe)), "Library", "MyVpnTray.app")
	if _, err := os.Stat(app); err == nil {
		exec.Command("/usr/bin/open", "-g", app).Start()
	}
}
