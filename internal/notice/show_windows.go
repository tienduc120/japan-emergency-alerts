//go:build windows

package notice

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/async"
)

const (
	createNoWindow = 0x08000000
	// helperGrace is how long a helper told to stop may take to remove its icon before it is killed.
	helperGrace = 5 * time.Second
)

// Show starts this same program as the helper that shows m, and waits for it. When ctx ends it closes the helper's
// input, which tells it to remove its icon and leave: a killed helper would leave the icon behind.
func Show(ctx context.Context, m Message) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find this program: %w", err)
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, exe, Command)
	cmd.Stderr = &stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	in, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("notification helper: %w", err)
	}
	cmd.Cancel = in.Close
	cmd.WaitDelay = helperGrace
	if err = cmd.Start(); err != nil {
		return fmt.Errorf("start the notification helper: %w", err)
	}
	writeErr := Encode(in, m)
	err = cmd.Wait()
	switch {
	case ctx.Err() != nil:
		return nil //nolint:nilerr // stopped on purpose; the helper was told to remove its icon
	case writeErr != nil:
		return writeErr
	case err != nil:
		return fmt.Errorf("notification helper: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// RunHelper is the helper: it reads one message from stdin, shows it, opens the settings page with open if it is
// clicked, and leaves when the notification goes or the program that started it closes stdin.
func RunHelper(ctx context.Context, stdin *os.File, open func(context.Context, string) error) error {
	m, err := Decode(bufio.NewReader(stdin))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	t := newTray(syscall.Handle(stdin.Fd()))
	return async.Run(ctx, t.run, func(ctx context.Context) error {
		defer cancel()
		return Serve(ctx, Fit(m), t, open)
	})
}
