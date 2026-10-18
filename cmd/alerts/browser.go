package main

import (
	"context"
	"os/exec"
	"runtime"
	"time"
)

const browserTimeout = 10 * time.Second

// openBrowser shows an address in the person's own browser, using whatever their system uses for that.
func openBrowser(ctx context.Context, address string) error {
	ctx, cancel := context.WithTimeout(ctx, browserTimeout)
	defer cancel()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", address)
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", address)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", address)
	}
	return cmd.Run()
}
