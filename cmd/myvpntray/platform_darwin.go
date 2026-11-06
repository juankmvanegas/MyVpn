package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The tray lives at MyVpn.app/Contents/Library/MyVpnTray.app/Contents/MacOS/MyVpnTray.
func mainBundle() string {
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	if i := strings.Index(exe, ".app/Contents/Library/"); i >= 0 {
		return exe[:i+4]
	}
	return "/Applications/MyVpn.app"
}

func openGUI() { exec.Command("/usr/bin/open", mainBundle()).Run() }

func quitGUI() {
	exec.Command("/usr/bin/osascript", "-e", `tell application id "com.myvpn.app" to quit`).Run()
}

func agentPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", "com.myvpn.tray.plist")
}

func autostartEnabled() bool {
	_, err := os.Stat(agentPath())
	return err == nil
}

func setAutostart(on bool) error {
	if !on {
		return os.Remove(agentPath())
	}
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>com.myvpn.tray</string>
	<key>ProgramArguments</key><array><string>%s</string></array>
	<key>RunAtLoad</key><true/>
	<key>LimitLoadToSessionType</key><string>Aqua</string>
	<key>ProcessType</key><string>Interactive</string>
</dict>
</plist>
`, exe)
	os.MkdirAll(filepath.Dir(agentPath()), 0o755)
	return os.WriteFile(agentPath(), []byte(plist), 0o644)
}
