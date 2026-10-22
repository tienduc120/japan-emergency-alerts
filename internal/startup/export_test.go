//go:build windows

package startup

import (
	"fmt"
	"syscall"
	"unsafe"
)

// SetApprovedForTest writes the value Task Manager keeps when someone turns the entry on or off.
func SetApprovedForTest(r *Registry, data []byte) error {
	return setValue(r.ApprovedKey, r.Name, syscall.REG_BINARY, data)
}

// DeleteTreeForTest removes a scratch key and everything under it.
func DeleteTreeForTest(key string) error {
	keyPtr, err := syscall.UTF16PtrFromString(key)
	if err != nil {
		return err
	}
	code, _, _ := advapi("RegDeleteTreeW").Call(uintptr(syscall.HKEY_CURRENT_USER), uintptr(unsafe.Pointer(keyPtr)))
	if code != 0 && syscall.Errno(code) != syscall.ERROR_FILE_NOT_FOUND {
		return fmt.Errorf("delete %s: %w", key, syscall.Errno(code))
	}
	return nil
}
