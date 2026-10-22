package notice

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	// At sign-in the program can start before the taskbar does, and showing fails until it is there.
	retryEvery  = 2 * time.Second
	giveUpAfter = 30 * time.Second
	// A notification fades after a few seconds; this only bounds a helper that was never told.
	waitAtMost = time.Minute
)

// Serve shows m on t and opens its address when it is clicked. It never opens anything when the notification could not
// be shown, and removes the icon whatever happens after it was.
func Serve(ctx context.Context, m Message, t Tray, open func(context.Context, string) error) error {
	if err := show(ctx, m, t); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return nil
	}
	limit := time.NewTimer(waitAtMost)
	defer limit.Stop()
	var opened error
	select {
	case event := <-t.Events():
		if event == Clicked {
			if err := open(ctx, m.Address); err != nil {
				opened = fmt.Errorf("open the settings page: %w", err)
			}
		}
	case <-limit.C:
	case <-ctx.Done():
	}
	if err := t.Close(); err != nil {
		return errors.Join(opened, fmt.Errorf("remove the notification icon: %w", err))
	}
	return opened
}

// show tries until the taskbar takes the notification, or gives up. A program told to stop has nothing to show.
func show(ctx context.Context, m Message, t Tray) error {
	deadline := time.NewTimer(giveUpAfter)
	defer deadline.Stop()
	retry := time.NewTicker(retryEvery)
	defer retry.Stop()
	for {
		err := t.Show(m)
		if err == nil {
			return nil
		}
		select {
		case <-deadline.C:
			return fmt.Errorf("show the start-up notification: %w", err)
		case <-ctx.Done():
			return nil
		case <-retry.C:
		}
	}
}
