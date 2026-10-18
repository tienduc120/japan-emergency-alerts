// Package async is the one place goroutines are started. A goroutine started anywhere else has no owner to cancel it,
// bound it, or wait for it, which is how leaks and lost panics happen; this package gives every one of them all three.
package async

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
)

// Run starts every task in its own goroutine, hands each a context that is cancelled as soon as any of them fails or
// the parent is cancelled, and returns once all of them have finished.
//
// A task that returns nil does not stop the others: only a failure does. A task that returns context.Canceled was told
// to stop, which is a clean shutdown, so it is not reported. A panic is turned into an error carrying its stack
// instead of crashing the process or vanishing. Every failure is returned, joined, so a second problem is not hidden
// behind the first.
func Run(ctx context.Context, tasks ...func(context.Context) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for _, task := range tasks {
		if task == nil {
			continue
		}
		wg.Go(func() {
			if err := guarded(ctx, task); err != nil && !errors.Is(err, context.Canceled) {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
				cancel()
			}
		})
	}
	wg.Wait()
	return errors.Join(errs...)
}

// guarded runs a task and converts a panic into an error that keeps the stack.
func guarded(ctx context.Context, task func(context.Context) error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("task panicked: %v\n%s", r, debug.Stack())
		}
	}()
	return task(ctx)
}
