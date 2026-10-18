package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
	"github.com/tienduc120/japan-emergency-alerts/internal/config"
	"github.com/tienduc120/japan-emergency-alerts/internal/intensity"
	"github.com/tienduc120/japan-emergency-alerts/internal/jma"
	"github.com/tienduc120/japan-emergency-alerts/internal/state"
)

const waitLimit = 10 * time.Second

// fakeSource stands in for the JMA poller, so no test touches the network.
type fakeSource struct {
	handle  jma.Handler
	since   time.Time // what the program told the source about the last run
	horizon time.Time // what the source says it has read up to
	status  string
	run     func(ctx context.Context, handle jma.Handler) error
	replay  func(ctx context.Context, handle jma.Handler) error
}

func (s *fakeSource) Run(ctx context.Context) error {
	if s.run == nil {
		<-ctx.Done()
		return nil
	}
	return s.run(ctx, s.handle)
}

func (s *fakeSource) Replay(ctx context.Context) error {
	if s.replay == nil {
		return nil
	}
	return s.replay(ctx, s.handle)
}

func (s *fakeSource) Horizon() time.Time { return s.horizon }

func (s *fakeSource) Status() string { return s.status }

func fakeSources(s *fakeSource) sourceFactory {
	return func(sc sourceConfig) []runner {
		s.handle = sc.Handle
		s.since = sc.Since
		return []runner{s}
	}
}

// recorder is a fake webhook that counts, and keeps, the messages it receives.
type recorder struct {
	mu     sync.Mutex
	posts  int
	bodies []string
	seen   chan struct{}
	srv    *httptest.Server
}

func newRecorder(t *testing.T, status func(n int) int) *recorder {
	t.Helper()
	r := &recorder{seen: make(chan struct{}, 100)}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		r.mu.Lock()
		r.posts++
		n := r.posts
		r.bodies = append(r.bodies, string(body))
		r.mu.Unlock()
		r.seen <- struct{}{}
		code := http.StatusNoContent
		if status != nil {
			code = status(n)
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.posts
}

func (r *recorder) webhook() string { return r.srv.URL + "/hook/" + token }

// waitForBody blocks until a received message contains want, and returns it.
func (r *recorder) waitForBody(t *testing.T, want string) string {
	t.Helper()
	deadline := time.After(waitLimit)
	for {
		r.mu.Lock()
		for _, body := range r.bodies {
			if strings.Contains(body, want) {
				r.mu.Unlock()
				return body
			}
		}
		r.mu.Unlock()
		select {
		case <-r.seen:
		case <-deadline:
			t.Fatalf("no message containing %q arrived", want)
		}
	}
}

// syncBuffer is a log destination several goroutines can write to.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func configFor(t *testing.T, webhook string) config.Config {
	t.Helper()
	cfg, err := config.FromEnv(envWith(webhook))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func quiet() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestRunFiltersAndPostsWhatItsSourcesProduce(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t, nil)
	finished := make(chan struct{})
	src := &fakeSource{run: func(ctx context.Context, handle jma.Handler) error {
		_ = handle(ctx, tsunamiFor(13, "tokyo"))
		_ = handle(ctx, tsunamiFor(1, "hokkaido"))
		_ = handle(ctx, tsunamiFor(13, "tokyo"))
		close(finished)
		<-ctx.Done()
		return nil
	}}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx, quiet(), configFor(t, rec.webhook()), fakeSources(src)) }()

	select {
	case <-finished:
	case <-time.After(waitLimit):
		t.Fatal("the source never finished its alerts")
	}
	cancel()

	if err := <-done; err != nil {
		t.Errorf("run = %v, want a clean shutdown", err)
	}
	if rec.count() != 1 {
		t.Errorf("%d posts, want exactly 1: the Hokkaido alert is elsewhere and the repeat is a duplicate", rec.count())
	}
}

func TestAnAlertTheWebhookRefusedIsOfferedAgainAndThenSent(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t, func(n int) int {
		if n == 1 {
			return http.StatusNotFound
		}
		return http.StatusNoContent
	})
	results := make(chan [2]error, 1)
	src := &fakeSource{run: func(ctx context.Context, handle jma.Handler) error {
		first := handle(ctx, tsunamiFor(13, "tokyo"))
		second := handle(ctx, tsunamiFor(13, "tokyo"))
		results <- [2]error{first, second}
		<-ctx.Done()
		return nil
	}}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx, quiet(), configFor(t, rec.webhook()), fakeSources(src)) }()

	var got [2]error
	select {
	case got = <-results:
	case <-time.After(waitLimit):
		t.Fatal("the source never finished its alerts")
	}
	cancel()
	<-done

	if got[0] == nil || got[1] != nil {
		t.Errorf("first, second = %v, %v, want a failure and then success", got[0], got[1])
	}
	if rec.count() != 2 {
		t.Errorf("%d posts, want 2: the refused one and the retry", rec.count())
	}
}

func TestCheckListsWhatWouldBeSentAndSendsNothing(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t, nil)
	weakQuake := alert.Alert{
		ID: "weak", Kind: alert.KindEarthquakeReport, Issued: time.Date(2026, time.October, 18, 3, 0, 0, 0, time.UTC),
		Areas: []alert.Area{{Prefecture: 13, Intensity: intensity.Two}},
	}
	src := &fakeSource{replay: func(ctx context.Context, handle jma.Handler) error {
		_ = handle(ctx, tsunamiFor(13, "tokyo-tsunami"))
		_ = handle(ctx, tsunamiFor(1, "hokkaido-tsunami"))
		_ = handle(ctx, weakQuake)
		return nil
	}}
	var stdout, stderr bytes.Buffer

	code := execute(
		t.Context(),
		[]string{"check"},
		testEnvironment(envWith(rec.webhook()), &stdout, &stderr, fakeSources(src)),
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr:\n%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"2026-10-18 12:00 JST", "tokyo-tsunami", "WOULD SEND",
		"hokkaido-tsunami", "outside the watched location", "weak", "below the intensity threshold",
		"1 of 3",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "WOULD SEND") != 1 {
		t.Errorf("want exactly one WOULD SEND:\n%s", out)
	}
	if rec.count() != 0 {
		t.Errorf("%d posts, want none: check is a dry run", rec.count())
	}
	if strings.Contains(out+stderr.String(), token) {
		t.Error("the webhook token must never be written out")
	}
}

func TestCheckReportsAFeedItCouldNotRead(t *testing.T) {
	t.Parallel()
	src := &fakeSource{replay: func(context.Context, jma.Handler) error { return context.DeadlineExceeded }}
	var stdout, stderr bytes.Buffer

	code := execute(t.Context(), []string{"check"}, testEnvironment(validEnv, &stdout, &stderr, fakeSources(src)))

	if code != 1 || !strings.Contains(stderr.String(), "check failed") {
		t.Errorf("exit %d, stderr %q, want exit 1 and a failure message", code, stderr.String())
	}
}

func TestTheLiveSourcesAreTheFeedPollerAndTheEarlyWarningStream(t *testing.T) {
	t.Parallel()

	sources := liveSources(
		sourceConfig{Handle: func(context.Context, alert.Alert) error { return nil }, Logger: quiet()},
	)

	if len(sources) != 2 {
		t.Fatalf("%d live sources, want 2: JMA's feeds and the early-warning stream", len(sources))
	}
	replayers := 0
	for _, s := range sources {
		if _, ok := s.(replayer); ok {
			replayers++
		}
	}
	if replayers != 1 {
		t.Errorf("%d sources can replay the past, want 1: only the feeds keep a history for check to read", replayers)
	}
}

// runUntilAlertsAreSent runs the program with one source that sends the given alerts, waits until it has finished, then
// shuts the program down.
func runUntilAlertsAreSent(t *testing.T, cfg config.Config, src *fakeSource, logger *slog.Logger, ids ...string) {
	t.Helper()
	finished := make(chan struct{})
	src.run = func(ctx context.Context, handle jma.Handler) error {
		for _, id := range ids {
			_ = handle(ctx, tsunamiFor(13, id))
		}
		close(finished)
		<-ctx.Done()
		return nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx, logger, cfg, fakeSources(src)) }()
	select {
	case <-finished:
	case <-time.After(waitLimit):
		t.Fatal("the source never finished its alerts")
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("run = %v, want a clean shutdown", err)
	}
}

func TestARestartDoesNotSendAgainWhatTheLastRunSent(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t, nil)
	cfg := configFor(t, rec.webhook())
	cfg.StateFile = filepath.Join(t.TempDir(), "state.json")
	before, err := state.Open(cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	if err = before.MarkSent("sent-by-the-last-run", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}

	runUntilAlertsAreSent(t, cfg, &fakeSource{}, quiet(), "sent-by-the-last-run", "new")

	if rec.count() != 1 {
		t.Errorf("%d posts, want 1: only the new alert", rec.count())
	}
	after, err := state.Open(cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after.Sent()["new"]; !ok {
		t.Errorf("Sent = %v, want the new alert recorded so the next restart will not repeat it", after.Sent())
	}
}

func TestTheHorizonIsGivenToTheSourcesAndSavedAtShutdown(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t, nil)
	cfg := configFor(t, rec.webhook())
	cfg.StateFile = filepath.Join(t.TempDir(), "state.json")
	was := time.Date(2026, time.October, 18, 3, 0, 0, 0, time.UTC)
	now := time.Date(2026, time.October, 18, 9, 0, 0, 0, time.UTC)
	before, err := state.Open(cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	if err = before.SetHorizon(was); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{horizon: now}

	runUntilAlertsAreSent(t, cfg, src, quiet())

	if !src.since.Equal(was) {
		t.Errorf("the source was told %v, want %v: that is where catching up starts", src.since, was)
	}
	after, err := state.Open(cfg.StateFile)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Horizon().Equal(now) {
		t.Errorf("Horizon = %v, want %v saved at shutdown", after.Horizon(), now)
	}
}

func TestADamagedStateFileDoesNotStopTheProgram(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t, nil)
	cfg := configFor(t, rec.webhook())
	cfg.StateFile = filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(cfg.StateFile, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	var logs syncBuffer

	runUntilAlertsAreSent(t, cfg, &fakeSource{}, slog.New(slog.NewJSONHandler(&logs, nil)), "a")

	if rec.count() != 1 {
		t.Errorf("%d posts, want 1: losing the history must not cost an alert", rec.count())
	}
	if !strings.Contains(logs.String(), "history") {
		t.Errorf("the log must say the history was lost:\n%s", logs.String())
	}
}

func TestTheHeartbeatReportsEverySourcesHealthAndTheAlertsSent(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t, nil)
	cfg := configFor(t, rec.webhook())
	cfg.HeartbeatEvery = 20 * time.Millisecond
	src := &fakeSource{status: "Fake feed: all fine"}
	finished := make(chan struct{})
	src.run = func(ctx context.Context, handle jma.Handler) error {
		_ = handle(ctx, tsunamiFor(13, "tokyo"))
		close(finished)
		<-ctx.Done()
		return nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx, quiet(), cfg, fakeSources(src)) }()
	<-finished
	t.Cleanup(func() {
		cancel()
		<-done
	})

	body := rec.waitForBody(t, "Alerts sent since then: 1")

	for _, want := range []string{`"status":true`, "Watching: Tokyo", "5弱", "Fake feed: all fine", "not an alert"} {
		if !strings.Contains(body, want) {
			t.Errorf("heartbeat lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, token) {
		t.Error("the webhook token must never be written out")
	}
}

func TestNoHeartbeatIsSentWhenItIsSwitchedOff(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t, nil)
	cfg := configFor(t, rec.webhook())
	cfg.HeartbeatEvery = 0

	runUntilAlertsAreSent(t, cfg, &fakeSource{status: "Fake feed: all fine"}, quiet(), "tokyo")

	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, body := range rec.bodies {
		if strings.Contains(body, `"status":true`) {
			t.Errorf("a status message arrived although the heartbeat is off:\n%s", body)
		}
	}
}

func TestOnlyTestAndCheckAreCommands(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"check", "extra"}, {"run"}} {
		var stdout, stderr bytes.Buffer

		code := execute(t.Context(), args, testEnvironment(validEnv, &stdout, &stderr, fakeSources(&fakeSource{})))

		if code != exitUsage || !strings.Contains(stderr.String(), "usage") {
			t.Errorf(
				"args %v: exit %d, stderr %q, want exit %d and a usage line",
				args,
				code,
				stderr.String(),
				exitUsage,
			)
		}
	}
}
