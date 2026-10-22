package notice_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"go.uber.org/goleak"

	"github.com/tienduc120/japan-emergency-alerts/internal/notice"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

const address = "http://127.0.0.1:47600/"

// fakeTray fails to show a given number of times, as Windows does while the taskbar is not there yet.
type fakeTray struct {
	mu       sync.Mutex
	failures int
	shows    int
	closes   int
	shownAt  time.Time
	events   chan notice.Event
}

func newFakeTray(failures int, events ...notice.Event) *fakeTray {
	f := &fakeTray{failures: failures, events: make(chan notice.Event, len(events))}
	for _, e := range events {
		f.events <- e
	}
	return f
}

func (f *fakeTray) Show(notice.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shows++
	if f.shows <= f.failures {
		return errors.New("the taskbar is not there yet")
	}
	f.shownAt = time.Now()
	return nil
}

func (f *fakeTray) Events() <-chan notice.Event { return f.events }

func (f *fakeTray) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	return nil
}

func (f *fakeTray) closed() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closes
}

type opener struct {
	mu     sync.Mutex
	opened []string
}

func (o *opener) open(_ context.Context, address string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.opened = append(o.opened, address)
	return nil
}

func (o *opener) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.opened)
}

func message() notice.Message {
	return notice.Message{Title: "Japan Emergency Alerts is running / 起動中", Text: "Watching Tokyo", Address: address}
}

func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

func TestFitShortensWithoutBreakingACharacter(t *testing.T) {
	t.Parallel()
	long := notice.Message{
		Title:   strings.Repeat("見", 70),
		Text:    strings.Repeat("a", 253) + "😀" + strings.Repeat("b", 46),
		Address: address,
	}

	short, fitted := notice.Fit(message()), notice.Fit(long)

	if short != message() {
		t.Errorf("Fit changed a message that fits: %+v", short)
	}
	if utf16Len(fitted.Title) > 63 || utf16Len(fitted.Text) > 255 {
		t.Errorf(
			"fitted to %d and %d UTF-16 units, want at most 63 and 255: Windows cuts the rest",
			utf16Len(fitted.Title),
			utf16Len(fitted.Text),
		)
	}
	if !strings.HasSuffix(fitted.Text, "…") || !strings.HasSuffix(fitted.Title, "…") ||
		!utf8.ValidString(fitted.Text) ||
		strings.ContainsRune(fitted.Text, '😀') {
		t.Errorf("text %q, want it cut before the emoji, which does not fit whole, and marked with …", fitted.Text)
	}
	if fitted.Address != address {
		t.Errorf("Address = %q, want it untouched", fitted.Address)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()
	m := notice.Message{
		Title:   "Japan Emergency Alerts is running / 起動中",
		Text:    "Watching Tokyo\n監視中：Tokyo",
		Address: address,
	}
	var line bytes.Buffer

	err := notice.Encode(&line, m)
	got, decodeErr := notice.Decode(bufio.NewReader(&line))

	if err != nil || decodeErr != nil || got != m {
		t.Errorf("round trip = %+v (%v, %v), want %+v", got, err, decodeErr, m)
	}
}

func TestServeOpensThePageOnAClick(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tray, o := newFakeTray(0, notice.Clicked), &opener{}

		err := notice.Serve(t.Context(), message(), tray, o.open)

		if err != nil || len(o.opened) != 1 || o.opened[0] != address || tray.closed() != 1 {
			t.Errorf(
				"err %v, opened %v, closed %d times, want the page opened once and the icon removed once",
				err,
				o.opened,
				tray.closed(),
			)
		}
	})
}

func TestServeOpensNothingWhenTheBalloonCloses(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tray, o := newFakeTray(0, notice.Closed), &opener{}

		err := notice.Serve(t.Context(), message(), tray, o.open)

		if err != nil || o.count() != 0 || tray.closed() != 1 {
			t.Errorf(
				"err %v, %d opened, closed %d times, want nothing opened and the icon removed once",
				err,
				o.count(),
				tray.closed(),
			)
		}
	})
}

func TestServeRetriesUntilTheTaskbarIsThere(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tray, o := newFakeTray(3, notice.Closed), &opener{}
		start := time.Now()

		err := notice.Serve(t.Context(), message(), tray, o.open)

		if err != nil || tray.shows != 4 || tray.shownAt.Sub(start) != 6*time.Second || o.count() != 0 {
			t.Errorf(
				"err %v, %d tries, shown after %v, want it shown on the 4th try, 2 s apart",
				err,
				tray.shows,
				tray.shownAt.Sub(start),
			)
		}
	})
}

func TestServeGivesUpAfterThirtySecondsWithoutOpeningAnything(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tray, o := newFakeTray(1<<20), &opener{}
		start := time.Now()

		err := notice.Serve(t.Context(), message(), tray, o.open)

		if err == nil || time.Since(start) != 30*time.Second || o.count() != 0 || tray.closed() != 0 {
			t.Errorf(
				"err %v after %v, %d opened, closed %d times, want an error at 30 s and nothing opened instead",
				err,
				time.Since(start),
				o.count(),
				tray.closed(),
			)
		}
	})
}

func TestServeRemovesTheIconWhenTold(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tray, o := newFakeTray(0), &opener{}
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		err := notice.Serve(ctx, message(), tray, o.open)

		if err != nil || tray.closed() != 1 || o.count() != 0 {
			t.Errorf(
				"err %v, closed %d times, %d opened, want the icon removed once the program stops",
				err,
				tray.closed(),
				o.count(),
			)
		}
	})
}

func TestServeStopsWaitingAfterAMinute(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		tray, o := newFakeTray(0), &opener{}
		start := time.Now()

		err := notice.Serve(t.Context(), message(), tray, o.open)

		if err != nil || time.Since(start) != time.Minute || tray.closed() != 1 {
			t.Errorf(
				"err %v after %v, closed %d times, want the icon removed after a minute",
				err,
				time.Since(start),
				tray.closed(),
			)
		}
	})
}
