// Package browser opens URLs in the user's default browser.
package browser

import (
	"os"
	"os/exec"
	"runtime"
)

// Open launches url in the default browser of the logged-in user.
func Open(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		if u := os.Getenv("SUDO_USER"); u != "" && os.Geteuid() == 0 {
			// do not open the browser as root
			cmd = exec.Command("sudo", "-u", u, "xdg-open", url)
		} else {
			cmd = exec.Command("xdg-open", url)
		}
	}
	return cmd.Start()
}
