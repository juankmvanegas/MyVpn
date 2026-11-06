package main

import (
	"fmt"
	"os/exec"
	"strings"
)

// elevate runs bin with args as root via the standard macOS admin prompt.
func elevate(bin string, args ...string) error {
	cmd := "quoted form of " + applescriptString(bin)
	for _, a := range args {
		cmd += ` & " " & quoted form of ` + applescriptString(a)
	}
	script := fmt.Sprintf(`do shell script (%s) with administrator privileges with prompt "MyVpn necesita permisos de administrador para instalar su servicio de red."`, cmd)
	out, err := exec.Command("/usr/bin/osascript", "-e", script).CombinedOutput()
	if err != nil {
		s := strings.TrimSpace(string(out))
		if strings.Contains(s, "-128") {
			return fmt.Errorf("instalación cancelada")
		}
		return fmt.Errorf("%v: %s", err, s)
	}
	return nil
}

func applescriptString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
