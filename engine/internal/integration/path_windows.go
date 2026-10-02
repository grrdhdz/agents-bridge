//go:build windows

package integration

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

type registryPath struct {
	key       string
	broadcast bool
}

func platformUserPath() UserPath { return registryPath{key: `Environment`, broadcast: true} }
func (p registryPath) Read() (string, error) {
	k, e := registry.OpenKey(registry.CURRENT_USER, p.key, registry.QUERY_VALUE)
	if errors.Is(e, registry.ErrNotExist) {
		return "", nil
	}
	if e != nil {
		return "", e
	}
	defer k.Close()
	v, _, e := k.GetStringValue("Path")
	if errors.Is(e, registry.ErrNotExist) {
		return "", nil
	}
	return v, e
}
func (p registryPath) Write(value string) error {
	k, _, e := registry.CreateKey(registry.CURRENT_USER, p.key, registry.SET_VALUE)
	if e != nil {
		return e
	}
	defer k.Close()
	return k.SetExpandStringValue("Path", value)
}
func (p registryPath) Notify() error {
	if !p.broadcast {
		return nil
	}
	name, e := windows.UTF16PtrFromString("Environment")
	if e != nil {
		return e
	}
	var result uintptr
	// Microsoft requires HWND_BROADCAST, WM_SETTINGCHANGE and the Environment string.
	proc := windows.NewLazySystemDLL("user32.dll").NewProc("SendMessageTimeoutW")
	ok, _, e := proc.Call(0xffff, 0x001a, 0, uintptr(unsafe.Pointer(name)), 0x0002, 100, uintptr(unsafe.Pointer(&result)))
	if ok == 0 {
		return errors.New("WM_SETTINGCHANGE no se pudo difundir")
	}
	return nil
}
