//go:build darwin

package popup

import (
	"context"
	"errors"
	"io"
	"os/exec"
)

// osShow shows a system alert dialog through AppleScript. The text goes in as an argument, never into the script, so
// nothing in an alert can be taken for a command.
func osShow(ctx context.Context, title, text string) error {
	cmd := exec.CommandContext(
		ctx,
		"osascript",
		"-e",
		"on run argv",
		"-e",
		"display alert (item 1 of argv) message (item 2 of argv) as critical",
		"-e",
		"end run",
		"--",
		title,
		text,
	)
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
