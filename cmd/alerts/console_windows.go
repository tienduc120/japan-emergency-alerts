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
