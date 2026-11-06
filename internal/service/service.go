// Package service installs, removes and runs the privileged MyVpn service.
package service

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Name of the service on every platform.
const Name = "MyVpn"

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	os.Chmod(tmp, mode)
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("instalando %s: %w", dst, err)
	}
	return nil
}

func selfPath() (string, error) {
	p, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(p)
}
