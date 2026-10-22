//go:build windows

package notice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

const (
	wmDestroy   = 0x0002
	wmClose     = 0x0010
	wmTimer     = 0x0113
	trayMessage = 0x8000 + 1 // WM_APP + 1

	nimAdd        = 0
	nimDelete     = 2
	nimSetVersion = 4
	nifMessage    = 0x01
	nifIcon       = 0x02
	nifTip        = 0x04
	nifInfo       = 0x10
	niifInfo      = 0x01
	// Version 4 puts the event in the low word of lParam.
	notifyIconVersion4  = 4
	ninSelect           = 0x0400 // the icon itself was clicked
	ninKeySelect        = 0x0401
	ninBalloonHide      = 0x0403
	ninBalloonUserClick = 0x0405
	lowWord             = 0xffff

	idiApplication   = 32512
	getMessageFailed = ^uintptr(0) // GetMessageW's -1
	iconID           = 1
	timerID          = 1
	// watchEvery is how often, in milliseconds, the window checks whether the program that started it has stopped.
	watchEvery = 250
	eventRoom  = 4
	tip        = "Japan Emergency Alerts"
	className  = "JapanEmergencyAlertsNotice"
)

// notifyIconData is NOTIFYICONDATAW; Go lays it out as C does on 64-bit Windows (976 bytes).
type notifyIconData struct {
	cbSize           uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         [16]byte
	hBalloonIcon     uintptr
}

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type winMsg struct {
	hwnd     uintptr
	message  uint32
	wParam   uintptr
	lParam   uintptr
	time     uint32
	ptX      int32
	ptY      int32
	lPrivate uint32
}

type procs struct {
	getModuleHandle, peekNamedPipe                                       *syscall.LazyProc
	registerClass, createWindow, defWindowProc, destroyWindow            *syscall.LazyProc
	getMessage, translateMessage, dispatchMessage, postMessage, postQuit *syscall.LazyProc
	setTimer, killTimer, loadIcon                                        *syscall.LazyProc
	notifyIcon, extractIcon                                              *syscall.LazyProc
}

func loadProcs() procs {
	kernel32, user32, shell32 := syscall.NewLazyDLL("kernel32.dll"), syscall.NewLazyDLL("user32.dll"),
		syscall.NewLazyDLL("shell32.dll")
	return procs{
		getModuleHandle: kernel32.NewProc("GetModuleHandleW"), peekNamedPipe: kernel32.NewProc("PeekNamedPipe"),
		registerClass: user32.NewProc("RegisterClassExW"), createWindow: user32.NewProc("CreateWindowExW"),
		defWindowProc: user32.NewProc("DefWindowProcW"), destroyWindow: user32.NewProc("DestroyWindow"),
		getMessage: user32.NewProc("GetMessageW"), translateMessage: user32.NewProc("TranslateMessage"),
		dispatchMessage: user32.NewProc("DispatchMessageW"), postMessage: user32.NewProc("PostMessageW"),
		postQuit: user32.NewProc("PostQuitMessage"), setTimer: user32.NewProc("SetTimer"),
		killTimer: user32.NewProc("KillTimer"), loadIcon: user32.NewProc("LoadIconW"),
		notifyIcon: shell32.NewProc("Shell_NotifyIconW"), extractIcon: shell32.NewProc("ExtractIconExW"),
	}
}

// winTray shows the notification from a hidden window. Windows delivers a window's messages to the thread that made
// it, so run keeps one thread for the window's whole life.
type winTray struct {
	p       procs
	stdin   syscall.Handle
	events  chan Event
	ready   chan struct{}
	readyMu sync.Once
	hwnd    uintptr // set before ready is closed; zero when the window could not be made
	icon    uintptr
}

func newTray(stdin syscall.Handle) *winTray {
	return &winTray{p: loadProcs(), stdin: stdin, events: make(chan Event, eventRoom), ready: make(chan struct{})}
}

func (t *winTray) markReady() { t.readyMu.Do(func() { close(t.ready) }) }

// run makes the window and pumps its messages until it is destroyed, which happens when Close is called or ctx ends.
func (t *winTray) run(ctx context.Context) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer t.markReady()
	if err := t.create(ctx); err != nil {
		return err
	}
	t.markReady()
	var m winMsg
	for {
		r, _, callErr := t.p.getMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		switch r {
		case 0:
			return nil
		case getMessageFailed:
			return fmt.Errorf("notification window: %w", callErr)
		}
		_, _, _ = t.p.translateMessage.Call(uintptr(unsafe.Pointer(&m)))
		_, _, _ = t.p.dispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func (t *winTray) create(ctx context.Context) error {
	instance, _, _ := t.p.getModuleHandle.Call(0)
	class, err := syscall.UTF16PtrFromString(className)
	if err != nil {
		return err
	}
	wc := wndClassEx{
		lpfnWndProc: syscall.NewCallback(func(hwnd, message, wParam, lParam uintptr) uintptr {
			return t.handle(ctx, hwnd, message, wParam, lParam)
		}),
		hInstance:     instance,
		lpszClassName: class,
	}
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	if r, _, callErr := t.p.registerClass.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return fmt.Errorf("register the notification window: %w", callErr)
	}
	hwnd, _, callErr := t.p.createWindow.Call(
		0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(class)), 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if hwnd == 0 {
		return fmt.Errorf("make the notification window: %w", callErr)
	}
	t.hwnd = hwnd
	t.icon = t.programIcon()
	if r, _, timerErr := t.p.setTimer.Call(hwnd, timerID, watchEvery, 0); r == 0 {
		return fmt.Errorf("start the notification window's timer: %w", timerErr)
	}
	return nil
}

// programIcon is the program's own small icon, or Windows' plain application icon when the file has none.
func (t *winTray) programIcon() uintptr {
	if small := t.fileIcon(); small != 0 {
		return small
	}
	icon, _, _ := t.p.loadIcon.Call(0, idiApplication)
	return icon
}

func (t *winTray) fileIcon() uintptr {
	exe, err := os.Executable()
	if err != nil {
		return 0
	}
	path, err := syscall.UTF16PtrFromString(exe)
	if err != nil {
		return 0
	}
	var small uintptr
	_, _, _ = t.p.extractIcon.Call(uintptr(unsafe.Pointer(path)), 0, 0, uintptr(unsafe.Pointer(&small)), 1)
	return small
}

func (t *winTray) handle(ctx context.Context, hwnd, message, wParam, lParam uintptr) uintptr {
	switch message {
	case trayMessage:
		// No case for NIN_BALLOONTIMEOUT: Windows 11 sends it at once when the banner goes straight to the
		// notification centre, where it can still be clicked while the icon is there.
		switch lParam & lowWord {
		case ninBalloonUserClick, ninSelect, ninKeySelect:
			t.send(Clicked)
		case ninBalloonHide:
			t.send(Closed)
		}
		return 0
	case wmTimer:
		if ctx.Err() != nil {
			_, _, _ = t.p.postMessage.Call(hwnd, wmClose, 0, 0)
		} else if t.inputClosed() {
			t.send(Closed)
		}
		return 0
	case wmClose:
		data := t.data()
		_, _, _ = t.p.notifyIcon.Call(nimDelete, uintptr(unsafe.Pointer(&data)))
		_, _, _ = t.p.killTimer.Call(hwnd, timerID)
		_, _, _ = t.p.destroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		_, _, _ = t.p.postQuit.Call(0)
		return 0
	}
	r, _, _ := t.p.defWindowProc.Call(hwnd, message, wParam, lParam)
	return r
}

// inputClosed says whether the program that started the helper has closed its end of stdin, because it is stopping.
func (t *winTray) inputClosed() bool {
	var available uint32
	r, _, err := t.p.peekNamedPipe.Call(uintptr(t.stdin), 0, 0, 0, uintptr(unsafe.Pointer(&available)), 0)
	return r == 0 && errors.Is(err, syscall.ERROR_BROKEN_PIPE)
}

func (t *winTray) send(e Event) {
	select {
	case t.events <- e:
	default:
	}
}

func (t *winTray) data() notifyIconData {
	d := notifyIconData{hWnd: t.hwnd, uID: iconID}
	d.cbSize = uint32(unsafe.Sizeof(d))
	return d
}

// Show adds the icon with its notification. It fails while the taskbar is not there yet, as at sign-in.
func (t *winTray) Show(m Message) error {
	<-t.ready
	if t.hwnd == 0 {
		return errors.New("there is no notification window")
	}
	d := t.data()
	d.uFlags = nifMessage | nifIcon | nifTip | nifInfo
	d.uCallbackMessage = trayMessage
	d.hIcon = t.icon
	d.dwInfoFlags = niifInfo
	copyUTF16(d.szTip[:], tip)
	copyUTF16(d.szInfoTitle[:], m.Title)
	copyUTF16(d.szInfo[:], m.Text)
	if r, _, callErr := t.p.notifyIcon.Call(nimAdd, uintptr(unsafe.Pointer(&d))); r == 0 {
		return fmt.Errorf("add the notification icon: %w", callErr)
	}
	d.uVersion = notifyIconVersion4
	_, _, _ = t.p.notifyIcon.Call(nimSetVersion, uintptr(unsafe.Pointer(&d)))
	return nil
}

func (t *winTray) Events() <-chan Event { return t.events }

// Close removes the icon and ends the window; the window's own thread does both.
func (t *winTray) Close() error {
	if t.hwnd == 0 {
		return nil
	}
	if r, _, callErr := t.p.postMessage.Call(t.hwnd, wmClose, 0, 0); r == 0 {
		return fmt.Errorf("close the notification window: %w", callErr)
	}
	return nil
}

// copyUTF16 fills dst with s, always leaving the last unit as the terminating zero.
func copyUTF16(dst []uint16, s string) {
	units, err := syscall.UTF16FromString(strings.ReplaceAll(s, "\x00", ""))
	if err != nil {
		return
	}
	copy(dst[:len(dst)-1], units)
}
