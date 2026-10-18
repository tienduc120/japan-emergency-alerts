//go:build windows

package popup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"unsafe"
)

const (
	// Flags for MessageBoxW: an OK button, a warning icon and sound, in front of everything, and brought to the front
	// even though the program that asked is not the window the person is using.
	messageBoxFlags = 0x00000000 | 0x00000030 | 0x00040000 | 0x00010000
	createNoWindow  = 0x08000000
	maxInputBytes   = 1 << 20
)

// osShow starts this same program as a helper that shows the window, so the window belongs to a process the system
// can end when the program stops, and a window left open never keeps the program from closing.
func osShow(ctx context.Context, title, text string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, exe, WindowCommand)
	cmd.Stdin = strings.NewReader(title + "\n" + text)
	cmd.Stderr = &stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil //nolint:nilerr // stopped on purpose; the window was closed with the helper
		}
		return fmt.Errorf("pop-up helper: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// RunWindowCommand is the helper: it reads a title (the first line) and a text, shows one warning window, and returns
// when it is closed.
func RunWindowCommand(in io.Reader) error {
	data, err := io.ReadAll(io.LimitReader(in, maxInputBytes))
	if err != nil {
		return err
	}
	title, text, _ := strings.Cut(string(data), "\n")
	return messageBox(title, text)
}

func messageBox(title, text string) error {
	titlePtr, err := syscall.UTF16PtrFromString(withoutNul(title))
	if err != nil {
		return err
	}
	textPtr, err := syscall.UTF16PtrFromString(withoutNul(text))
	if err != nil {
		return err
	}
	proc := syscall.NewLazyDLL("user32.dll").NewProc("MessageBoxW")
	result, _, callErr := proc.Call(
		0, uintptr(unsafe.Pointer(textPtr)), uintptr(unsafe.Pointer(titlePtr)), uintptr(messageBoxFlags))
	if result == 0 {
		return errors.Join(errors.New("MessageBoxW failed"), callErr)
	}
	return nil
}

func withoutNul(s string) string { return strings.ReplaceAll(s, "\x00", "") }
