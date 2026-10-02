//go:build windows

package control

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows has no mode bits, so §5.1's "owner only" is a protected DACL with a
// single ACE granting the current user full control, and the current user as
// owner. Protected means nothing is inherited from %LOCALAPPDATA%\Temp, whose
// ACL commonly also grants SYSTEM, Administrators and sandbox accounts (for
// example Codex's CodexSandboxUsers) access to everything created below it.

// currentUserSID is the SID of the user this process runs as.
func currentUserSID() (*windows.SID, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("cannot identify the current user: %w", err)
	}
	return user.User.Sid.Copy()
}

// ownerOnlySecurityDescriptor grants full control to the current user alone.
// inherit makes the ACE apply to files and directories created inside, so
// descriptors written into a private directory are private too. The owner is
// set explicitly: in an elevated shell the default owner of new objects is
// Administrators, which verifyOwnerOnlyACL would then (correctly) reject.
func ownerOnlySecurityDescriptor(inherit bool) (*windows.SECURITY_DESCRIPTOR, error) {
	sid, err := currentUserSID()
	if err != nil {
		return nil, err
	}
	flags := ""
	if inherit {
		flags = "OICI"
	}
	return windows.SecurityDescriptorFromString(fmt.Sprintf("O:%sD:P(A;%s;FA;;;%s)", sid, flags, sid))
}

func ownerOnlySecurityAttributes(inherit bool) (*windows.SecurityAttributes, error) {
	sd, err := ownerOnlySecurityDescriptor(inherit)
	if err != nil {
		return nil, err
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, nil
}

// makePrivateDir creates dir with the owner-only DACL. An existing directory
// owned by the current user is tightened in place, which is how directories
// left by v0.2.0 (created with the inherited, shared ACL) are migrated. One
// owned by anyone else is left alone for verifyPrivateDir to reject.
func makePrivateDir(dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return err
	}
	sa, err := ownerOnlySecurityAttributes(true)
	if err != nil {
		return err
	}
	path, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	err = windows.CreateDirectory(path, sa)
	if err == nil {
		return nil
	}
	if !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	if verifyOwnerOnlyACL(dir) == nil {
		return nil
	}
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return nil
	}
	owner, _, err := sd.Owner()
	user, userErr := currentUserSID()
	if err != nil || userErr != nil || !owner.Equals(user) {
		return nil
	}
	dacl, _, err := sa.SecurityDescriptor.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

// verifyPrivateDir requires a real directory (not a junction or symlink) that
// the current user owns and nobody else can access.
func verifyPrivateDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeIrregular != 0 {
		return errors.New("is a link or reparse point")
	}
	if !info.IsDir() {
		return errors.New("is not a directory")
	}
	if err := verifyOwnerOnlyACL(dir); err != nil {
		return fmt.Errorf("%w; bórralo para que agents-bridge lo recree con permisos privados", err)
	}
	return nil
}

// verifyOwnerOnlyACL checks path's owner and that every ACE granting access
// names the current user. Deny ACEs are harmless and allowed.
func verifyOwnerOnlyACL(path string) error {
	user, err := currentUserSID()
	if err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("cannot read ACL: %w", err)
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil {
		return errors.New("cannot verify owner")
	}
	if !owner.Equals(user) {
		return fmt.Errorf("is owned by %s, not by the current user", owner)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("cannot read ACL: %w", err)
	}
	if dacl == nil {
		return errors.New("has no DACL, so everyone has access")
	}
	for i := uint32(0); i < uint32(dacl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return fmt.Errorf("cannot read ACL: %w", err)
		}
		if ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !sid.Equals(user) {
			return fmt.Errorf("grants access to %s besides the current user", sid)
		}
	}
	return nil
}

// CreatePrivateFile creates path exclusively (it fails if path exists) with
// the owner-only DACL already in place, so there is no moment in which the
// file exists with the looser ACL inherited from its directory.
func CreatePrivateFile(path string) (*os.File, error) {
	sa, err := ownerOnlySecurityAttributes(false)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	// Same sharing as os.OpenFile: the ready file stays open while the bridge
	// runs, and whoever launched it must be able to read it meanwhile; the
	// DACL, not the share mode, is what keeps other users out.
	share := uint32(windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE)
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE, share, sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			err = os.ErrExist
		}
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

// VerifyOwnerOnly reports whether path is a regular file only the current
// user can access (§5.1). Tests use it for descriptors and ready files.
func VerifyOwnerOnly(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("is not a regular file")
	}
	return verifyOwnerOnlyACL(path)
}
