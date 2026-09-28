//go:build !windows

package control

import (
	"os"
	"testing"
)

func TestDescriptorDirectoryWithLoosePermissionsIsRejected(t *testing.T) {
	root := t.TempDir()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ListDescriptors(root); err == nil {
		t.Fatal("descriptor directory readable by others must be rejected")
	}
}
