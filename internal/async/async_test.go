package async_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"go.uber.org/goleak"

	"github.com/tienduc120/japan-emergency-alerts/internal/async"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

var errBoom = errors.New("boom")

func TestRunWaitsForEveryTask(t *testing.T) {
	t.Parallel()

	var finished atomic.Int32
	task := func(context.Context) error {
		finished.Add(1)
		return nil
	}

	if err := async.Run(t.Context(), task, task, task); err != nil {
		t.Fatal(err)
	}
	if finished.Load() != 3 {
		t.Errorf("finished = %d, want 3: Run must not return while a task is still running", finished.Load())
	}
}

func TestAFailureStopsTheOthersAndOnlyTheFailureIsReported(t *testing.T) {
	t.Parallel()

	failing := func(context.Context) error { return errBoom }
	waiting := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}

	err := async.Run(t.Context(), waiting, failing, waiting)

	if !errors.Is(err, errBoom) {
		t.Fatalf("err = %v, want the failure", err)
	}
	if errors.Is(err, context.Canceled) {
		t.Errorf("err = %v: a task told to stop did not fail, so its cancellation is not reported", err)
	}
}

func TestEveryFailureIsReported(t *testing.T) {
	t.Parallel()

	other := errors.New("other")
	err := async.Run(t.Context(),
		func(context.Context) error { return errBoom },
		func(context.Context) error { return other },
	)

	if !errors.Is(err, errBoom) || !errors.Is(err, other) {
		t.Errorf("err = %v, want both failures: dropping one hides a second problem", err)
	}
}

func TestAPanicBecomesAnErrorWithItsStack(t *testing.T) {
	t.Parallel()

	err := async.Run(t.Context(), func(context.Context) error { panic("kaboom") })

	if err == nil {
		t.Fatal("a panic in a supervised task must not vanish")
	}
	msg := err.Error()
	for _, want := range []string{"panicked", "kaboom", "goroutine"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error lacks %q:\n%s", want, msg)
		}
	}
}

func TestCancellingTheParentStopsEveryTask(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	waiting := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}

	done := make(chan error, 1)
	started := make(chan struct{}, 2)
	task := func(ctx context.Context) error {
		started <- struct{}{}
		return waiting(ctx)
	}
	go func() { done <- async.Run(ctx, task, task) }()
	<-started
	<-started
	cancel()

	if err := <-done; err != nil {
		t.Errorf("err = %v: being told to stop is a clean shutdown, not a failure", err)
	}
}

func TestATaskThatFinishesCleanlyDoesNotStopTheOthers(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{}, 2)
	finishedEarly := func(ctx context.Context) error {
		started <- struct{}{}
		return ctx.Err()
	}
	longRunning := func(ctx context.Context) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}

	done := make(chan error, 1)
	go func() { done <- async.Run(ctx, finishedEarly, longRunning) }()
	<-started
	<-started

	select {
	case err := <-done:
		t.Fatalf("Run returned (%v) while a task was still running: only a failure stops the rest", err)
	default:
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestNoTasksAndNilTasksAreFine(t *testing.T) {
	t.Parallel()

	if err := async.Run(t.Context()); err != nil {
		t.Errorf("no tasks: %v", err)
	}
	if err := async.Run(t.Context(), nil, func(context.Context) error { return nil }); err != nil {
		t.Errorf("a nil task is skipped, not a crash: %v", err)
	}
}
