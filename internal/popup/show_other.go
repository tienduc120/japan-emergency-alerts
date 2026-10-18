//go:build !windows && !darwin

package popup

import (
	"context"
	"errors"
	"io"
	"os/exec"
)

// osShow uses a desktop notification when the system has the tool for it, which stays on screen until dismissed, and a
// dialog otherwise. The text goes in as plain arguments, never through a shell.
func osShow(ctx context.Context, title, text string) error {
	if path, err := exec.LookPath("notify-send"); err == nil {
		return run(ctx, exec.CommandContext(ctx, path, "--urgency=critical", "--expire-time=0", title, text))
	}
	if path, err := exec.LookPath("zenity"); err == nil {
		return run(ctx, exec.CommandContext(ctx, path, "--warning", "--no-markup", "--title="+title, "--text="+text))
	}
	return errors.New("no pop-up tool found: install notify-send (libnotify) or zenity")
}

func run(ctx context.Context, cmd *exec.Cmd) error {
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil //nolint:nilerr // stopped on purpose; the window goes with its process
		}
		return err
	}
	return nil
}

// RunWindowCommand is only used on Windows.
func RunWindowCommand(io.Reader) error {
	return errors.New("the pop-up helper is only used on Windows")
}
