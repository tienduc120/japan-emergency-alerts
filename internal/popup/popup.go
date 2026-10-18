// Package popup shows alerts in windows on this computer, as a second way to be told beside the webhook.
//
// A window stays open until the person closes it, so showing one blocks. That must never hold up an alert source or the
// program's shutdown, so windows are shown by a few workers, each on a window of its own (a separate process on
// Windows), at most three at a time, with the rest waiting in a short queue.
package popup

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
	"github.com/tienduc120/japan-emergency-alerts/internal/async"
)

const (
	defaultWorkers = 3
	defaultQueue   = 8
	// maxTextRunes keeps a window small enough for a screen; the rest is on the settings page.
	maxTextRunes = 1200
	jstOffset    = 9 * 60 * 60

	// WindowCommand is the command the program is started with, by itself, to show one window on Windows and wait for
	// it to be closed. A window in a process of its own can be closed by the system when the program stops.
	WindowCommand = "popup-window"

	testTitle = "Japan Emergency Alerts · Test"
	testText  = "This is a test pop-up from japan-emergency-alerts.\nIf you can see this, pop-ups work.\n\nThis is not an alert."
)

// ErrBusy means every worker is showing a window and the queue is full.
var ErrBusy = errors.New("too many pop-up windows are waiting to be closed")

// ShowFunc shows one window and returns when it is closed, or when ctx ends.
type ShowFunc func(ctx context.Context, title, text string) error

// Options are all optional.
type Options struct {
	// Show defaults to the window of the operating system.
	Show    ShowFunc
	Workers int
	Queue   int
	Logger  *slog.Logger
}

type job struct{ title, text string }

// Window shows alerts as pop-up windows. Run must be running for anything to appear.
type Window struct {
	show    ShowFunc
	workers int
	jobs    chan job
	logger  *slog.Logger
}

func New(opts Options) *Window {
	w := &Window{show: opts.Show, workers: opts.Workers, logger: opts.Logger}
	if w.show == nil {
		w.show = osShow
	}
	if w.workers <= 0 {
		w.workers = defaultWorkers
	}
	queue := opts.Queue
	if queue <= 0 {
		queue = defaultQueue
	}
	if w.logger == nil {
		w.logger = slog.New(slog.DiscardHandler)
	}
	w.jobs = make(chan job, queue)
	return w
}

// Run shows queued windows until ctx ends. It does not wait for open windows to be closed.
func (w *Window) Run(ctx context.Context) error {
	tasks := make([]func(context.Context) error, w.workers)
	for i := range tasks {
		tasks[i] = w.work
	}
	return async.Run(ctx, tasks...)
}

func (w *Window) work(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case j := <-w.jobs:
			if err := w.show(ctx, j.title, j.text); err != nil && ctx.Err() == nil {
				w.logger.WarnContext(ctx, "pop-up window could not be shown", "error", err)
			}
		}
	}
}

// Send queues a window for an alert and returns at once; ErrBusy means there was no room.
func (w *Window) Send(_ context.Context, a alert.Alert) error {
	title, text := compose(a)
	return w.enqueue(job{title: title, text: text})
}

// Test queues a window that says it is only a test.
func (w *Window) Test(context.Context) error {
	return w.enqueue(job{title: testTitle, text: testText})
}

func (w *Window) enqueue(j job) error {
	select {
	case w.jobs <- j:
		return nil
	default:
		return ErrBusy
	}
}

// ShowNow shows one window and waits until it is closed. It is for a command that has nothing else to do.
func ShowNow(ctx context.Context, title, text string) error {
	return osShow(ctx, title, text)
}

// TestWindow is the title and text of the window that proves pop-ups work.
func TestWindow() (string, string) { return testTitle, testText }

func compose(a alert.Alert) (string, string) {
	title := "Japan Emergency Alerts · " + a.Kind.Label()

	var header []string
	if !a.Issued.IsZero() {
		header = append(
			header,
			"Issued "+a.Issued.In(time.FixedZone("JST", jstOffset)).Format("2006-01-02 15:04")+" JST",
		)
	}
	if a.Late {
		header = append(header, "Sent late")
	}

	body := strings.TrimSpace(a.Text)
	if runes := []rune(body); len(runes) > maxTextRunes {
		body = string(runes[:maxTextRunes]) + "…\n(The rest is on the settings page.)"
	}

	var text strings.Builder
	if len(header) > 0 {
		text.WriteString(strings.Join(header, "\n") + "\n\n")
	}
	text.WriteString(body + "\n\nSource: Japan Meteorological Agency. Unofficial relay, not affiliated with JMA.")
	return title, text.String()
}
