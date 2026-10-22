package main

import (
	"context"
	"errors"
	"net"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/notice"
	"github.com/tienduc120/japan-emergency-alerts/internal/startup"
)

// notice waits for the start-up notification and returns it.
func (h *harness) notice() notice.Message {
	h.t.Helper()
	select {
	case m := <-h.notices:
		return m
	case <-time.After(waitLimit):
		h.t.Fatal("no start-up notification was shown")
		return notice.Message{}
	}
}

func configured(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{"WEBHOOK_URL": newRecorder(t, nil).webhook()}
}

func TestOnWindowsAStartByHandOpensThePageWhenSetUp(t *testing.T) {
	t.Parallel()
	w := newWatcher(nil)
	h := newHarness(t, configured(t), fakeSources(w.src))
	h.onWindows(&fakeStartup{})
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)

	var opened string
	select {
	case opened = <-h.opened:
	case <-time.After(waitLimit):
		t.Fatal("a person who started the program by hand must see its settings page")
	}

	if opened != h.address() || len(h.notices) != 0 {
		t.Errorf(
			"opened %q and %d notifications, want the page %q and no notification",
			opened,
			len(h.notices),
			h.address(),
		)
	}
	stopped(t, cancel, done)
}

func TestAStartByWindowsShowsANoticeAndNotThePage(t *testing.T) {
	t.Parallel()
	w := newWatcher(nil)
	h := newHarness(t, configured(t), fakeSources(w.src))
	h.onWindows(&fakeStartup{})
	ctx, cancel := context.WithCancel(t.Context())
	done := h.startWith(ctx, []string{startup.Marker})

	m := h.notice()

	if m.Title != "Japan Emergency Alerts is running / 起動中" || m.Address != h.address() {
		t.Errorf("notification %+v, want the running title and the settings address %q", m, h.address())
	}
	lines := strings.Split(m.Text, "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "Watching ") || !strings.HasPrefix(lines[1], "監視中：") ||
		lines[2] != "Click to open the settings. / クリックで設定を開きます。" {
		t.Errorf("text %q, want the two summary lines and how to open the settings", m.Text)
	}
	if len(h.opened) != 0 {
		t.Error("a start by Windows at sign-in must never open the settings page by itself")
	}
	stopped(t, cancel, done)
}

func TestAStartByWindowsBeforeSetUpShowsTheNotSetUpNotice(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil, fakeSources(newWatcher(nil).src))
	h.onWindows(&fakeStartup{})
	ctx, cancel := context.WithCancel(t.Context())
	done := h.startWith(ctx, []string{startup.Marker})

	m := h.notice()

	want := notice.Message{
		Title:   "Japan Emergency Alerts: not set up yet / 未設定",
		Text:    "Click to set it up. / クリックして設定してください。",
		Address: h.address(),
	}
	if m != want || len(h.opened) != 0 {
		t.Errorf("notification %+v and %d pages opened, want %+v and no page", m, len(h.opened), want)
	}
	stopped(t, cancel, done)
}

func TestASecondCopyStartedByWindowsLeavesQuietly(t *testing.T) {
	t.Parallel()
	first := newHarness(t, nil, fakeSources(newWatcher(nil).src))
	ctx, cancel := context.WithCancel(t.Context())
	done := first.start(ctx)
	usual := ""
	if u, err := url.Parse(first.address()); err == nil {
		usual = u.Host
	}

	second := newHarness(t, map[string]string{"CONFIG_FILE": first.configPath}, fakeSources(newWatcher(nil).src))
	second.onWindows(&fakeStartup{})
	second.env.listen = func(ctx context.Context) (net.Listener, error) { return listenPreferring(ctx, usual) }
	give, stop := context.WithTimeout(t.Context(), 3*time.Second)
	defer stop()
	code := execute(give, []string{startup.Marker}, second.env)

	if code != 0 || len(second.opened) != 0 || len(second.notices) != 0 {
		t.Errorf(
			"exit %d, %d pages, %d notifications, want a quiet exit: an old start-up shortcut must not open a page",
			code,
			len(second.opened),
			len(second.notices),
		)
	}
	stopped(t, cancel, done)
}

func TestAStartRepointsTheEntryToThisProgram(t *testing.T) {
	t.Parallel()
	moved, off := &fakeStartup{on: true, exe: `C:\old\alerts.exe`}, &fakeStartup{}
	for _, entry := range []*fakeStartup{moved, off} {
		h := newHarness(t, configured(t), fakeSources(newWatcher(nil).src))
		h.onWindows(entry)
		ctx, cancel := context.WithCancel(t.Context())
		done := h.startWith(ctx, []string{startup.Marker})
		h.address()
		stopped(t, cancel, done)
	}

	if enabled, _ := moved.calls(); !slices.Equal(enabled, []string{fakeExe}) {
		t.Errorf(
			"a moved program re-pointed the entry to %v, want %q: moving the file must not end starting with Windows",
			enabled,
			fakeExe,
		)
	}
	if enabled, _ := off.calls(); len(enabled) != 0 {
		t.Errorf("an entry that is off was turned on (%v): only the box may do that", enabled)
	}
}

func TestWithoutTheMarkerSystemTheMarkerChangesNothing(t *testing.T) {
	t.Parallel()
	w := newWatcher(nil)
	h := newHarness(t, configured(t), fakeSources(w.src))
	ctx, cancel := context.WithCancel(t.Context())
	done := h.startWith(ctx, []string{startup.Marker})

	w.waitForStart(t)

	if len(h.opened) != 0 || len(h.notices) != 0 {
		t.Errorf(
			"%d pages, %d notifications, want today's quiet start on macOS and Linux",
			len(h.opened),
			len(h.notices),
		)
	}
	if code := stopped(t, cancel, done); code != 0 {
		t.Errorf("exit %d, want the marker accepted everywhere", code)
	}
}

func TestTheNoticeHelperIsAHelper(t *testing.T) {
	t.Parallel()

	args := []string{notice.Command}

	if !isHelper(args) || holdsWindowOpen(args, 1) {
		t.Error("the notification helper must never open, and so empty, the log of the program that started it")
	}
}

func TestANoticeThatFailsIsLoggedAndWatchingGoesOn(t *testing.T) {
	t.Parallel()
	w := newWatcher(nil)
	h := newHarness(t, configured(t), fakeSources(w.src))
	h.onWindows(&fakeStartup{})
	h.env.notice = func(context.Context, notice.Message) error { return errors.New("no taskbar after 30 seconds") }
	ctx, cancel := context.WithCancel(t.Context())
	done := h.startWith(ctx, []string{startup.Marker})

	w.waitForStart(t)
	deadline := time.After(waitLimit)
	for !strings.Contains(h.stdout.String(), "no taskbar after 30 seconds") {
		select {
		case <-deadline:
			t.Fatalf("the failed notification was never logged:\n%s", h.stdout.String())
		case <-time.After(2 * time.Millisecond):
		}
	}
	code := stopped(t, cancel, done)

	if code != 0 || strings.Count(h.stdout.String(), "no taskbar after 30 seconds") != 1 {
		t.Errorf("exit %d, log:\n%s\nwant watching to go on and the failure logged once", code, h.stdout.String())
	}
}
