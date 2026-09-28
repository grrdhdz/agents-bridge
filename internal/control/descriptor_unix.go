//go:build !windows

package control

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// makePrivateDir creates dir with mode 0700. Unlike on Windows, an existing
// directory is never loosened or tightened here: verifyPrivateDir rejects it.
func makePrivateDir(dir string) error { return os.MkdirAll(dir, 0o700) }

// CreatePrivateFile creates path exclusively (it fails if path exists) with
// mode 0600.
func CreatePrivateFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

// VerifyOwnerOnly reports whether path is a regular file owned by the current
// user with mode 0600 (§5.1). Tests use it for descriptors and ready files.
func VerifyOwnerOnly(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("is not a regular file")
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		return fmt.Errorf("has mode %#o, want 0600", perm)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Getuid() {
		return errors.New("is not owned by the current user")
	}
	return nil
}

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
