//go:build windows

package startup

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

const (
	regOptionNonVolatile = 0
	utf16Bytes           = 2
)

// Registry is the entry in the person's own part of the registry, so no admin rights are needed. The keys are under
// HKEY_CURRENT_USER.
type Registry struct {
	RunKey, ApprovedKey, Name string
}

// Live is the entry Windows reads at sign-in, and the place Task Manager records that it was turned off.
func Live() *Registry {
	return &Registry{
		RunKey:      `Software\Microsoft\Windows\CurrentVersion\Run`,
		ApprovedKey: `Software\Microsoft\Windows\CurrentVersion\Explorer\StartupApproved\Run`,
		Name:        "Japan Emergency Alerts",
	}
}

// State says whether Windows will start the program at sign-in, and which program the entry names.
func (r *Registry) State() (bool, string, error) {
	data, found, err := readValue(r.RunKey, r.Name)
	if err != nil || !found {
		return false, "", err
	}
	exe, _ := ExeOf(utf16String(data))
	approved, found, err := readValue(r.ApprovedKey, r.Name)
	if err != nil {
		return false, exe, err
	}
	// Task Manager marks an entry it turned off with an odd first byte.
	turnedOff := found && len(approved) > 0 && approved[0]%2 == 1
	return !turnedOff, exe, nil
}

// Enable makes Windows start exe at sign-in, also when Task Manager had turned the entry off.
func (r *Registry) Enable(exe string) error {
	value, err := syscall.UTF16FromString(Command(exe))
	if err != nil {
		return fmt.Errorf("write the start-up entry: %w", err)
	}
	data := unsafe.Slice((*byte)(unsafe.Pointer(&value[0])), len(value)*utf16Bytes)
	if err = setValue(r.RunKey, r.Name, syscall.REG_SZ, data); err != nil {
		return err
	}
	if err = deleteValue(r.ApprovedKey, r.Name); err != nil {
		return fmt.Errorf("clear Task Manager's mark: %w", err)
	}
	return nil
}

// Disable removes the entry. One that is not there is not an error.
func (r *Registry) Disable() error {
	if err := deleteValue(r.RunKey, r.Name); err != nil {
		return err
	}
	if err := deleteValue(r.ApprovedKey, r.Name); err != nil {
		return fmt.Errorf("clear Task Manager's mark: %w", err)
	}
	return nil
}

func advapi(name string) *syscall.LazyProc { return syscall.NewLazyDLL("advapi32.dll").NewProc(name) }

// readValue returns a value's bytes. A missing key or value is not found, not an error.
func readValue(key, name string) ([]byte, bool, error) {
	k, err := open(key, syscall.KEY_QUERY_VALUE)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("open %s: %w", key, err)
	}
	defer func() { _ = syscall.RegCloseKey(k) }()
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, false, err
	}
	var size uint32
	err = syscall.RegQueryValueEx(k, namePtr, nil, nil, nil, &size)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return nil, false, nil
	}
	if err != nil || size == 0 {
		return nil, err == nil, wrapRead(key, name, err)
	}
	data := make([]byte, size)
	if err = syscall.RegQueryValueEx(k, namePtr, nil, nil, &data[0], &size); err != nil {
		return nil, false, wrapRead(key, name, err)
	}
	return data[:size], true, nil
}

func wrapRead(key, name string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("read %s\\%s: %w", key, name, err)
}

func setValue(key, name string, kind uint32, data []byte) error {
	keyPtr, err := syscall.UTF16PtrFromString(key)
	if err != nil {
		return err
	}
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	var k syscall.Handle
	if code, _, _ := advapi("RegCreateKeyExW").Call(
		uintptr(syscall.HKEY_CURRENT_USER), uintptr(unsafe.Pointer(keyPtr)), 0, 0, regOptionNonVolatile,
		uintptr(syscall.KEY_SET_VALUE), 0, uintptr(unsafe.Pointer(&k)), 0,
	); code != 0 {
		return fmt.Errorf("create %s: %w", key, syscall.Errno(code))
	}
	defer func() { _ = syscall.RegCloseKey(k) }()
	if code, _, _ := advapi("RegSetValueExW").Call(
		uintptr(k), uintptr(unsafe.Pointer(namePtr)), 0, uintptr(kind),
		uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)),
	); code != 0 {
		return fmt.Errorf("write %s\\%s: %w", key, name, syscall.Errno(code))
	}
	return nil
}

// deleteValue removes a value. A missing key or value is already what was wanted.
func deleteValue(key, name string) error {
	k, err := open(key, syscall.KEY_SET_VALUE)
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", key, err)
	}
	defer func() { _ = syscall.RegCloseKey(k) }()
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return err
	}
	code, _, _ := advapi("RegDeleteValueW").Call(uintptr(k), uintptr(unsafe.Pointer(namePtr)))
	if code != 0 && !errors.Is(syscall.Errno(code), syscall.ERROR_FILE_NOT_FOUND) {
		return fmt.Errorf("delete %s\\%s: %w", key, name, syscall.Errno(code))
	}
	return nil
}

func open(key string, access uint32) (syscall.Handle, error) {
	keyPtr, err := syscall.UTF16PtrFromString(key)
	if err != nil {
		return 0, err
	}
	var k syscall.Handle
	err = syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, keyPtr, 0, access, &k)
	return k, err
}

func utf16String(data []byte) string {
	if len(data) < utf16Bytes {
		return ""
	}
	return syscall.UTF16ToString(unsafe.Slice((*uint16)(unsafe.Pointer(&data[0])), len(data)/utf16Bytes))
}
