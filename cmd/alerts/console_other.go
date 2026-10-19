//go:build !windows

package main

import "io"

// attachTerminal is always true here: these programs run in a terminal, so there is somewhere to print.
func attachTerminal() (io.Writer, bool) { return nil, true }

// waitForEnterIfDoubleClicked does nothing here: only Windows closes a console window the moment its program ends.
func waitForEnterIfDoubleClicked(io.Writer) {}
