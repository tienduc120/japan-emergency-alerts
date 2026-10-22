//go:build !windows

package notice

import (
	"context"
	"errors"
	"os"
)

// Show is only used on Windows.
func Show(context.Context, Message) error { return errors.ErrUnsupported }

// RunHelper is only used on Windows.
func RunHelper(context.Context, *os.File, func(context.Context, string) error) error {
	return errors.ErrUnsupported
}
