package popup_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
	"github.com/tienduc120/japan-emergency-alerts/internal/popup"
)

const waitLimit = 10 * time.Second

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

type shown struct{ title, text string }

// screen stands in for the desktop: it records each window and keeps it open until the test closes it.
type screen struct {
	mu      sync.Mutex
	windows []shown
	opened  chan shown
	close   chan struct{}
	open    atomic.Int32
	peak    atomic.Int32
	fail    error
}

func newScreen() *screen {
	return &screen{opened: make(chan shown, 32), close: make(chan struct{}, 32)}
}

func (s *screen) show(ctx context.Context, title, text string) error {
	if s.fail != nil {
		return s.fail
	}
	w := shown{title, text}
	s.mu.Lock()
	s.windows = append(s.windows, w)
	s.mu.Unlock()
	n := s.open.Add(1)
	for {
		peak := s.peak.Load()
		if n <= peak || s.peak.CompareAndSwap(peak, n) {
			break
		}
	}
	s.opened <- w
	defer s.open.Add(-1)
	select {
	case <-s.close:
		return nil
	case <-ctx.Done():
		return nil
	}
}

func (s *screen) next(t *testing.T) shown {
	t.Helper()
	select {
	case w := <-s.opened:
		return w
	case <-time.After(waitLimit):
		t.Fatal("no window was opened")
		return shown{}
	}
}

// run starts the pop-up workers and stops them when the test ends.
func run(t *testing.T, opts popup.Options) *popup.Window {
	t.Helper()
	w := popup.New(opts)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return w
}

func tsunami() alert.Alert {
	return alert.Alert{
		ID: "t1", Kind: alert.KindTsunami, Issued: time.Date(2026, time.October, 18, 3, 0, 0, 0, time.UTC),
		Text: "津波警報が発表されました。\nただちに高台へ避難してください。",
	}
}

func TestAnAlertBecomesAWindowNamingItsKindWithJMAsTextInside(t *testing.T) {
	t.Parallel()
	scr := newScreen()
	w := run(t, popup.Options{Show: scr.show})

	if err := w.Send(t.Context(), tsunami()); err != nil {
		t.Fatal(err)
	}
	got := scr.next(t)

	if !strings.Contains(got.title, "Tsunami warning") {
		t.Errorf("title = %q, want it to name the kind of alert", got.title)
	}
	for _, want := range []string{tsunami().Text, "2026-10-18 12:00 JST", "Japan Meteorological Agency", "Unofficial"} {
		if !strings.Contains(got.text, want) {
			t.Errorf("text lacks %q:\n%s", want, got.text)
		}
	}
	if strings.Contains(got.text, "late") {
		t.Errorf("an alert on time must not call itself late:\n%s", got.text)
	}
}

func TestALateAlertSaysSoInTheWindow(t *testing.T) {
	t.Parallel()
	scr := newScreen()
	w := run(t, popup.Options{Show: scr.show})
	a := tsunami()
	a.Late = true

	if err := w.Send(t.Context(), a); err != nil {
		t.Fatal(err)
	}

	if got := scr.next(t); !strings.Contains(strings.ToLower(got.text), "sent late") {
		t.Errorf("text = %q, want it to say it arrived late", got.text)
	} else if strings.Contains(got.text, "offline") {
		t.Errorf("text = %q, want no reason for the delay: it cannot know why", got.text)
	}
}

func TestAVeryLongTextIsCutAndSaysWhereToReadTheRest(t *testing.T) {
	t.Parallel()
	scr := newScreen()
	w := run(t, popup.Options{Show: scr.show})
	a := tsunami()
	a.Text = strings.Repeat("津", 5000)

	if err := w.Send(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	got := scr.next(t)

	if n := len([]rune(got.text)); n > 1500 {
		t.Errorf("text has %d characters, want a window that fits on a screen", n)
	}
	if !strings.Contains(got.text, "…") || !strings.Contains(got.text, "settings page") {
		t.Errorf("a cut text must say it was cut and where to read it all:\n%s", got.text)
	}
}

func TestAtMostThreeWindowsAreOpenAtOnce(t *testing.T) {
	t.Parallel()
	scr := newScreen()
	w := run(t, popup.Options{Show: scr.show, Workers: 3, Queue: 8})

	for i := range 5 {
		a := tsunami()
		a.ID = string(rune('a' + i))
		if err := w.Send(t.Context(), a); err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		scr.next(t)
	}

	if scr.open.Load() != 3 {
		t.Fatalf("%d windows open, want 3: a flood of alerts must not bury the screen", scr.open.Load())
	}
	scr.close <- struct{}{}
	scr.next(t) // the fourth takes the free place
	if scr.peak.Load() != 3 {
		t.Errorf("peak = %d, want never more than 3", scr.peak.Load())
	}
}

func TestAFullQueueIsReportedAtOnceInsteadOfBlocking(t *testing.T) {
	t.Parallel()
	scr := newScreen()
	w := run(t, popup.Options{Show: scr.show, Workers: 1, Queue: 1})
	if err := w.Send(t.Context(), tsunami()); err != nil {
		t.Fatal(err)
	}
	scr.next(t) // the only worker is now busy with a window nobody closes
	if err := w.Send(t.Context(), tsunami()); err != nil {
		t.Fatal(err) // fills the queue
	}

	err := w.Send(t.Context(), tsunami())

	if !errors.Is(err, popup.ErrBusy) {
		t.Errorf("err = %v, want ErrBusy: an alert source must never wait on a person closing a window", err)
	}
}

func TestStoppingDoesNotWaitForWindowsToBeClosed(t *testing.T) {
	t.Parallel()
	scr := newScreen()
	w := popup.New(popup.Options{Show: scr.show})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	if err := w.Send(ctx, tsunami()); err != nil {
		t.Fatal(err)
	}
	scr.next(t)

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v, want nil", err)
		}
	case <-time.After(waitLimit):
		t.Fatal("Run waited for an open window: closing the program would hang")
	}
}

func TestATestWindowSaysItIsNotAnAlert(t *testing.T) {
	t.Parallel()
	scr := newScreen()
	w := run(t, popup.Options{Show: scr.show})

	if err := w.Test(t.Context()); err != nil {
		t.Fatal(err)
	}

	if got := scr.next(t); !strings.Contains(got.text, "not an alert") || !strings.Contains(got.title, "Test") {
		t.Errorf("window = %+v, want it to say it is only a test", got)
	}
}

func TestAWindowThatCannotBeShownIsLoggedAndTheNextOneStillWorks(t *testing.T) {
	t.Parallel()
	scr := newScreen()
	scr.fail = errors.New("no display")
	var logs bytes.Buffer
	var mu sync.Mutex
	w := run(t, popup.Options{Show: scr.show, Logger: slog.New(slog.NewTextHandler(&lockedWriter{&mu, &logs}, nil))})

	if err := w.Send(t.Context(), tsunami()); err != nil {
		t.Fatalf("Send = %v: showing happens later, so the sender cannot know yet", err)
	}
	deadline := time.After(waitLimit)
	for {
		mu.Lock()
		got := logs.String()
		mu.Unlock()
		if strings.Contains(got, "no display") {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("the failure was never logged:\n%s", got)
		case <-time.After(2 * time.Millisecond):
		}
	}
}

type lockedWriter struct {
	mu  *sync.Mutex
	buf *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}
