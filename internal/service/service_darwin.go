package service

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

const (
	label     = "com.myvpn.daemon"
	binPath   = "/Library/PrivilegedHelperTools/com.myvpn.daemon"
	plistPath = "/Library/LaunchDaemons/com.myvpn.daemon.plist"
	logPath   = "/Library/Logs/MyVpn.log"
)

// Install copies the running binary and registers a launchd daemon.
func Install(ownerUID int) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("se requieren privilegios de administrador")
	}
	self, err := selfPath()
	if err != nil {
		return err
	}
	exec.Command("launchctl", "bootout", "system/"+label).Run()
	if err := copyFile(self, binPath, 0o755); err != nil {
		return err
	}
	os.Chown(binPath, 0, 0)
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>%s</string>
	<key>ProgramArguments</key>
	<array><string>%s</string><string>run</string><string>--owner-uid</string><string>%d</string></array>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key><true/>
	<key>StandardOutPath</key><string>%s</string>
	<key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, label, binPath, ownerUID, logPath, logPath)
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return err
	}
	os.Chown(plistPath, 0, 0)
	if out, err := exec.Command("launchctl", "bootstrap", "system", plistPath).CombinedOutput(); err != nil {
		// older macOS
		if out2, err2 := exec.Command("launchctl", "load", "-w", plistPath).CombinedOutput(); err2 != nil {
			return fmt.Errorf("launchctl: %v %s %s", err, out, out2)
		}
	}
	return nil
}

// Uninstall removes the daemon.
func Uninstall() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("se requieren privilegios de administrador")
	}
	exec.Command("launchctl", "bootout", "system/"+label).Run()
	os.Remove(plistPath)
	os.Remove(binPath)
	return nil
}

// RunAsService is a no-op on macOS (launchd runs the process directly).
func RunAsService(run func(stop <-chan struct{})) bool { return false }

// DefaultOwnerUID returns the user that invoked sudo (or the current user).
func DefaultOwnerUID() int {
	if s := os.Getenv("SUDO_UID"); s != "" {
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
	}
	return os.Getuid()
}
