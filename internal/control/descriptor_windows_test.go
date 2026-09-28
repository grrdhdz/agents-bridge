//go:build windows

package control

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// TestNewDescriptorDirectoryIsOwnerOnly covers §5.1 on Windows: a directory
// codex-bridge creates carries a protected DACL for the current user alone.
func TestNewDescriptorDirectoryIsOwnerOnly(t *testing.T) {
	root := filepath.Join(t.TempDir(), "instances")
	if _, err := ListDescriptors(root); err != nil {
		t.Fatal(err)
	}
	if err := verifyPrivateDir(root); err != nil {
		t.Fatalf("new descriptor directory should be private: %v", err)
	}
}

// TestLooseDescriptorDirectoryIsRejectedThenHardened: a directory the user
// owns but others can read (as v0.2.0 left them, with the ACL inherited from
// %TEMP%) fails verification, and descriptorDir tightens it in place.
func TestLooseDescriptorDirectoryIsRejectedThenHardened(t *testing.T) {
	root := t.TempDir()
	loose, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;OW)(A;OICI;FR;;;WD)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := loose.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
	if err := verifyPrivateDir(root); err == nil {
		t.Fatal("a directory readable by Everyone must fail verification")
	}
	if _, err := ListDescriptors(root); err != nil {
		t.Fatalf("a loose directory owned by the current user should be hardened, got %v", err)
	}
	if err := verifyPrivateDir(root); err != nil {
		t.Fatalf("directory should be private after hardening: %v", err)
	}
}

// TestCreatePrivateFileIsOwnerOnlyAndExclusive covers the ready-file path:
// created owner-only from the start, and never over an existing file.
func TestCreatePrivateFileIsOwnerOnlyAndExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ready.json")
	file, err := CreatePrivateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("{}\n"); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if err := VerifyOwnerOnly(path); err != nil {
		t.Fatalf("file should be owner-only: %v", err)
	}
	if _, err := CreatePrivateFile(path); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second create should fail with ErrExist, got %v", err)
	}
}
