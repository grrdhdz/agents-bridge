//go:build !windows

package control

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// verifyPrivateDir requires a real directory owned by the current user with
// mode 0700, so descriptor capabilities are never exposed to other users.
func verifyPrivateDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("is not a directory")
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		return fmt.Errorf("has mode %#o, want 0700", perm)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("cannot verify owner")
	}
	if int(stat.Uid) != os.Getuid() {
		return errors.New("is not owned by the current user")
	}
	return nil
}
