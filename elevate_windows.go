package main

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

// elevate runs bin with args through UAC and waits for it to finish.
func elevate(bin string, args ...string) error {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = "'" + strings.ReplaceAll(a, "'", "''") + "'"
	}
	argList := "@()"
	if len(quoted) > 0 {
		argList = strings.Join(quoted, ",")
	}
	ps := fmt.Sprintf("$p = Start-Process -FilePath '%s' -ArgumentList %s -Verb RunAs -WindowStyle Hidden -Wait -PassThru; exit $p.ExitCode",
		strings.ReplaceAll(bin, "'", "''"), argList)
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		s := strings.TrimSpace(string(out))
		if strings.Contains(s, "canceled") || strings.Contains(s, "cancelada") {
			return fmt.Errorf("instalación cancelada")
		}
		return fmt.Errorf("%v: %s", err, s)
	}
	return nil
}
