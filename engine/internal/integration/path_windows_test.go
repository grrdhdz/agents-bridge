//go:build windows

package integration

import (
	"context"
	"fmt"
	"os"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// Uses a disposable registry key, never HKCU Environment or the real user PATH.
func TestWindowsRegistryWithTemporaryLocalAppData(t *testing.T) {
	o := isolatedEnsure(t)
	o.Platform = "windows"
	key := fmt.Sprintf(`Software\agents-bridge-tests\%d-%s`, os.Getpid(), filepathSafe(t.Name()))
	t.Cleanup(func() { registry.DeleteKey(registry.CURRENT_USER, key) })
	o.UserPath = registryPath{key: key, broadcast: false}
	r, e := Ensure(context.Background(), o)
	if e != nil || r.CLIError != "" || !r.CLICurrent {
		t.Fatal(r, e)
	}
	value, e := o.UserPath.Read()
	if e != nil || value == "" {
		t.Fatal(value, e)
	}
	r, e = Ensure(context.Background(), o)
	if e != nil || r.Changed {
		t.Fatal(r, e)
	}
}
func filepathSafe(s string) string { return fmt.Sprintf("%x", []byte(s)) }
