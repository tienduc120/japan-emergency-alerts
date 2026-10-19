//go:build windows

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"syscall"
	"unsafe"
)

// attachParentProcess asks AttachConsole for the console of the program that started this one.
const attachParentProcess = ^uintptr(0)

// attachTerminal says whether there is somewhere to print. The Windows program is built without a console of its own,
// so that double-clicking it opens no black window. When it is started from a terminal it joins that terminal, so
// `alerts test` and `alerts check` still print there; when it is not, output has nowhere to go and goes to a log file.
// The writer is the terminal it joined, or nil when standard output and error already work as they are.
func attachTerminal() (io.Writer, bool) {
	if handle, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE); err == nil && handle != 0 {
		return nil, true // a console of its own, or output redirected to a file or a pipe
	}
	kernel := syscall.NewLazyDLL("kernel32.dll")
	if joined, _, _ := kernel.NewProc("AttachConsole").Call(attachParentProcess); joined == 0 {
		return nil, false
	}
	out, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0)
	if err != nil {
		return nil, false
	}
	return out, true
}

// waitForEnterIfDoubleClicked keeps the window open after a failure when the program was started by double-clicking
// it, because Windows closes that window the moment the program ends and the message would never be read. When the
// program was started from a terminal, other processes share the console and nothing is held back.
func waitForEnterIfDoubleClicked(w io.Writer) {
	attached := consoleProcessCount()
	if attached != 1 {
		return
	}
	fmt.Fprintln(w, "\nPress Enter to close this window.")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

// consoleProcessCount is how many processes share this console: 1 when the program has a console of its own.
func consoleProcessCount() uintptr {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleProcessList")
	var ids [2]uint32
	count, _, _ := proc.Call(uintptr(unsafe.Pointer(&ids[0])), uintptr(len(ids)))
	return count
}
