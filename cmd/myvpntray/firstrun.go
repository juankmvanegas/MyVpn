package main

import (
	"os"
	"path/filepath"

	"fyne.io/systray"
)

// firstRunAutostart enables "start with the system" the first time the tray
// runs (FortiClient's tray also starts at login); the user can untick it.
func firstRunAutostart(item *systray.MenuItem) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return
	}
	marker := filepath.Join(dir, "MyVpn", "tray-initialized")
	if _, err := os.Stat(marker); err == nil {
		return
	}
	os.MkdirAll(filepath.Dir(marker), 0o700)
	os.WriteFile(marker, []byte("1"), 0o600)
	if !autostartEnabled() && setAutostart(true) == nil {
		item.Check()
	}
}
