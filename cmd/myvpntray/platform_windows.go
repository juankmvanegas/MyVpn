package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

func exeDir() string {
	exe, _ := os.Executable()
	return filepath.Dir(exe)
}

func openGUI() {
	cmd := exec.Command(filepath.Join(exeDir(), "MyVpn.exe"))
	cmd.Dir = exeDir()
	cmd.Start()
}

func quitGUI() {
	cmd := exec.Command("taskkill", "/IM", "MyVpn.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Run()
}

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue("MyVpn")
	return err == nil
}

func setAutostart(on bool) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		return k.DeleteValue("MyVpn")
	}
	exe, _ := os.Executable()
	return k.SetStringValue("MyVpn", `"`+exe+`"`)
}
