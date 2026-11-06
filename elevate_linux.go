package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// elevate runs bin with args as root via polkit (pkexec).
func elevate(bin string, args ...string) error {
	out, err := exec.Command("pkexec", append([]string{bin}, args...)...).CombinedOutput()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && (ee.ExitCode() == 126 || ee.ExitCode() == 127) {
			return fmt.Errorf("instalación cancelada o pkexec no disponible; ejecuta: sudo %s %s", bin, strings.Join(args, " "))
		}
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
