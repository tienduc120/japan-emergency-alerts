//go:build !windows

package main

import "io"

// waitForEnterIfDoubleClicked does nothing here: only Windows closes a console window the moment its program ends.
func waitForEnterIfDoubleClicked(io.Writer) {}
