//go:build windows

package control

import (
	"errors"
	"os"
)

// verifyPrivateDir on Windows only checks that the path is a real directory.
// The owner-only ACL required by the 2026-09-13 spec (§5.1) is still pending.
func verifyPrivateDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("is not a directory")
	}
	return nil
}
