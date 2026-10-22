package main

import (
	"context"
	"net"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/startup"
)

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

	if opened != h.address() {
		t.Errorf("opened %q, want the settings page %q", opened, h.address())
	}
	stopped(t, cancel, done)
}

// A start by Windows at sign-in shows nothing, set up or not: the program just watches.
func TestAStartByWindowsShowsNothing(t *testing.T) {
	t.Parallel()
	for name, settings := range map[string]map[string]string{"set up": configured(t), "not set up": nil} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t, settings, fakeSources(newWatcher(nil).src))
			h.onWindows(&fakeStartup{})
			ctx, cancel := context.WithCancel(t.Context())
			done := h.startWith(ctx, []string{startup.Marker})
			h.address()

			code := stopped(t, cancel, done)

			if code != 0 || len(h.opened) != 0 {
				t.Errorf("exit %d, %d pages opened, want nothing shown at sign-in", code, len(h.opened))
			}
		})
	}
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

	if code != 0 || len(second.opened) != 0 {
		t.Errorf("exit %d, %d pages, want a quiet exit: an old start-up shortcut must not open a page",
			code, len(second.opened))
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
	code := stopped(t, cancel, done)

	if code != 0 || len(h.opened) != 0 {
		t.Errorf(
			"exit %d, %d pages opened, want today's quiet start on macOS and Linux, marker accepted",
			code,
			len(h.opened),
		)
	}
}
