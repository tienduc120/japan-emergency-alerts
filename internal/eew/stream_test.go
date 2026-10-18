package eew_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
	"github.com/tienduc120/japan-emergency-alerts/internal/eew"
)

const waitLimit = 10 * time.Second

// wsServer is a fake early-warning service. Each connection runs onConn in its own goroutine, and the connection
// closes when onConn returns.
type wsServer struct {
	url   string
	mu    sync.Mutex
	conns int
}

func newWS(t *testing.T, onConn func(n int, c *websocket.Conn)) *wsServer {
	t.Helper()
	s := &wsServer{}
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		s.mu.Lock()
		s.conns++
		n := s.conns
		s.mu.Unlock()
		onConn(n, c)
	}))
	t.Cleanup(srv.Close)
	s.url = "ws" + strings.TrimPrefix(srv.URL, "http")
	return s
}

func (s *wsServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns
}

func send(t *testing.T, c *websocket.Conn, frame string) {
	t.Helper()
	if err := c.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
		t.Errorf("server could not send: %v", err)
	}
}

// drain reads until the client closes, which also lets the server answer the client's pings.
func drain(c *websocket.Conn) {
	for {
		if _, _, err := c.ReadMessage(); err != nil {
			return
		}
	}
}

func collect() (alert.Handler, chan alert.Alert) {
	got := make(chan alert.Alert, 100)
	return func(_ context.Context, a alert.Alert) error {
		got <- a
		return nil
	}, got
}

func next(t *testing.T, got chan alert.Alert) alert.Alert {
	t.Helper()
	select {
	case a := <-got:
		return a
	case <-time.After(waitLimit):
		t.Fatal("no alert arrived")
		return alert.Alert{}
	}
}

func startStream(t *testing.T, cfg eew.Config) {
	t.Helper()
	cfg.MinBackoff, cfg.MaxBackoff = time.Millisecond, 5*time.Millisecond
	if cfg.PingEvery == 0 {
		cfg.PingEvery, cfg.PongWait = 20*time.Millisecond, 2*time.Second
	}
	stream := eew.NewStream(cfg)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- stream.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run returned %v: a context that ends is a clean shutdown", err)
		}
	})
}

func warning(t *testing.T, eventID, serial string) string {
	t.Helper()
	frame := string(testdata(t, "warning_chiba.json"))
	frame = strings.ReplaceAll(frame, "20261018140005", eventID)
	return strings.Replace(frame, `"serial": "1"`, `"serial": "`+serial+`"`, 1)
}

func cancel(t *testing.T, eventID string) string {
	t.Helper()
	return strings.ReplaceAll(string(testdata(t, "cancel.json")), "20261018140005", eventID)
}

func TestAWarningFrameIsRelayedAsAnAlert(t *testing.T) {
	t.Parallel()
	frame := warning(t, "E1", "1")
	srv := newWS(t, func(_ int, c *websocket.Conn) {
		send(t, c, frame)
		drain(c)
	})
	handle, got := collect()
	startStream(t, eew.Config{URL: srv.url, Handle: handle})

	a := next(t, got)

	if a.ID != "E1" || a.Kind != alert.KindEarlyWarning {
		t.Errorf("ID %q, Kind %v, want the event id and an early warning", a.ID, a.Kind)
	}
	if len(a.Areas) != 3 || !strings.Contains(a.Text, "震央地名：千葉県東方沖") {
		t.Errorf("Areas %v, Text %q, want three prefectures and JMA's epicentre", a.Areas, a.Text)
	}
}

func TestJunkTestFramesAndOtherCodesAreSkippedAndTheStreamCarriesOn(t *testing.T) {
	t.Parallel()
	asTest := strings.Replace(warning(t, "T1", "1"), `"cancelled": false,`, `"cancelled": false, "test": true,`, 1)
	srv := newWS(t, func(_ int, c *websocket.Conn) {
		send(t, c, "garbage")
		send(t, c, `{"code":551,"id":"quake"}`)
		send(t, c, asTest)
		send(t, c, warning(t, "REAL", "1"))
		drain(c)
	})
	handle, got := collect()
	startStream(t, eew.Config{URL: srv.url, Handle: handle})

	a := next(t, got)

	if a.ID != "REAL" || len(got) != 0 {
		t.Errorf(
			"first alert %q with %d more, want only REAL: a test message must never look like an earthquake",
			a.ID,
			len(got),
		)
	}
}

func TestTheStreamReconnectsAfterTheServerHangsUp(t *testing.T) {
	t.Parallel()
	srv := newWS(t, func(n int, c *websocket.Conn) {
		if n == 1 {
			send(t, c, warning(t, "FIRST", "1"))
			return // hang up
		}
		send(t, c, warning(t, "SECOND", "1"))
		drain(c)
	})
	handle, got := collect()
	startStream(t, eew.Config{URL: srv.url, Handle: handle})

	first, second := next(t, got), next(t, got)

	if first.ID != "FIRST" || second.ID != "SECOND" || srv.count() < 2 {
		t.Errorf(
			"got %q then %q over %d connections, want both events across a reconnect",
			first.ID,
			second.ID,
			srv.count(),
		)
	}
}

func TestASilentServerIsReplacedByAFreshConnection(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	second := make(chan struct{})
	var once sync.Once
	srv := newWS(t, func(n int, c *websocket.Conn) {
		if n == 1 {
			<-release // never reads, so it never answers the client's pings
			return
		}
		once.Do(func() { close(second) })
		drain(c)
	})
	handle, _ := collect()
	startStream(
		t,
		eew.Config{URL: srv.url, Handle: handle, PingEvery: 10 * time.Millisecond, PongWait: 100 * time.Millisecond},
	)

	select {
	case <-second:
	case <-time.After(waitLimit):
		t.Fatalf(
			"still on the first connection after %d connection(s): a dead link must be noticed and replaced",
			srv.count(),
		)
	}
}

func TestACancellationIsSentOnlyForAWarningThatWasRelayed(t *testing.T) {
	t.Parallel()
	srv := newWS(t, func(_ int, c *websocket.Conn) {
		send(t, c, cancel(t, "NEVER-SEEN"))
		send(t, c, warning(t, "E1", "1"))
		send(t, c, cancel(t, "E1"))
		drain(c)
	})
	handle, got := collect()
	startStream(t, eew.Config{URL: srv.url, Handle: handle})

	first, second := next(t, got), next(t, got)

	if first.ID != "E1" || second.ID != "E1-cancel" {
		t.Fatalf(
			"got %q then %q, want the warning and then its cancellation, and nothing for the unknown event",
			first.ID,
			second.ID,
		)
	}
	if second.Kind != alert.KindEarlyWarning || len(second.Areas) != len(first.Areas) {
		t.Errorf("cancellation Kind %v, %d areas, want the same areas as the warning, so only those warned hear of it",
			second.Kind, len(second.Areas))
	}
	if !strings.Contains(second.Text, "取り消され") {
		t.Errorf("Text = %q, want it to say the warning was cancelled", second.Text)
	}
}

func TestLaterUpdatesOfTheSameEventShareOneID(t *testing.T) {
	t.Parallel()
	srv := newWS(t, func(_ int, c *websocket.Conn) {
		send(t, c, warning(t, "E1", "1"))
		send(t, c, warning(t, "E1", "2"))
		drain(c)
	})
	handle, got := collect()
	startStream(t, eew.Config{URL: srv.url, Handle: handle})

	first, second := next(t, got), next(t, got)

	if first.ID != second.ID {
		t.Errorf(
			"IDs %q and %q differ: the filter can only send one message per event if updates share an ID",
			first.ID,
			second.ID,
		)
	}
}

func TestAHandlerFailureDoesNotStopTheStream(t *testing.T) {
	t.Parallel()
	srv := newWS(t, func(_ int, c *websocket.Conn) {
		send(t, c, warning(t, "E1", "1"))
		send(t, c, warning(t, "E2", "1"))
		drain(c)
	})
	var mu sync.Mutex
	calls := 0
	done := make(chan alert.Alert, 1)
	handle := func(_ context.Context, a alert.Alert) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return context.DeadlineExceeded
		}
		done <- a
		return nil
	}
	startStream(t, eew.Config{URL: srv.url, Handle: handle})

	select {
	case a := <-done:
		if a.ID != "E2" {
			t.Errorf("ID = %q, want E2", a.ID)
		}
	case <-time.After(waitLimit):
		t.Fatal("the second alert never arrived after the first failed")
	}
}

// history is a fake of the service's history endpoint, which returns the latest messages newest first.
type history struct {
	mu     sync.Mutex
	hits   int
	body   string
	status int
	srv    *httptest.Server
}

func newHistory(t *testing.T, status int, messages ...string) *history {
	t.Helper()
	h := &history{status: status, body: "[" + strings.Join(messages, ",") + "]"}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		h.mu.Lock()
		h.hits++
		h.mu.Unlock()
		if h.status != http.StatusOK {
			w.WriteHeader(h.status)
			return
		}
		_, _ = w.Write([]byte(h.body))
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *history) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hits
}

// warningIssuedAt is a warning whose issue time, in Japan time, is the one given.
func warningIssuedAt(t *testing.T, eventID, issued string) string {
	t.Helper()
	return strings.Replace(warning(t, eventID, "1"), `"time": "2026/10/18 14:00:24"}`, `"time": "`+issued+`"}`, 1)
}

// afterTheWarning is half a minute after the fixture warning was issued (14:00:24 Japan time).
func afterTheWarning() *fakeClock {
	return &fakeClock{now: time.Date(2026, time.October, 18, 5, 0, 54, 0, time.UTC)}
}

func TestAfterConnectingTheStreamRecoversWarningsIssuedInTheLastTwoMinutes(t *testing.T) {
	t.Parallel()
	hist := newHistory(t, http.StatusOK,
		warningIssuedAt(t, "NEWEST", "2026/10/18 14:00:30"),
		warningIssuedAt(t, "RECENT", "2026/10/18 14:00:24"),
		warningIssuedAt(t, "OLD", "2026/10/18 12:00:00"),
	)
	srv := newWS(t, func(_ int, c *websocket.Conn) { drain(c) })
	handle, got := collect()
	startStream(t, eew.Config{URL: srv.url, HistoryURL: hist.srv.URL, Handle: handle, Clock: afterTheWarning()})

	first, second := next(t, got), next(t, got)

	if first.ID != "RECENT" || second.ID != "NEWEST" || len(got) != 0 {
		t.Errorf("got %q then %q (%d more), want RECENT then NEWEST and not the warning from two hours ago",
			first.ID, second.ID, len(got))
	}
}

func TestAHistoryThatCannotBeReadDoesNotStopTheLiveStream(t *testing.T) {
	t.Parallel()
	hist := newHistory(t, http.StatusInternalServerError)
	srv := newWS(t, func(_ int, c *websocket.Conn) {
		send(t, c, warning(t, "LIVE", "1"))
		drain(c)
	})
	handle, got := collect()
	startStream(t, eew.Config{URL: srv.url, HistoryURL: hist.srv.URL, Handle: handle, Clock: afterTheWarning()})

	a := next(t, got)

	if a.ID != "LIVE" {
		t.Errorf("ID = %q, want the live warning", a.ID)
	}
}

func TestAReconnectRightAfterAHistoryReadDoesNotReadItAgain(t *testing.T) {
	t.Parallel()
	hist := newHistory(t, http.StatusOK)
	var once sync.Once
	reached := make(chan struct{})
	srv := newWS(t, func(n int, c *websocket.Conn) {
		if n == 1 {
			// Stay until history has been read, then hang up.
			deadline := time.After(waitLimit)
			for hist.count() < 1 {
				select {
				case <-deadline:
					return
				case <-time.After(time.Millisecond):
				}
			}
			<-time.After(60 * time.Millisecond) // let the stream finish reading the answer before the drop
			return
		}
		once.Do(func() { close(reached) })
		drain(c)
	})
	handle, _ := collect()
	startStream(t, eew.Config{URL: srv.url, HistoryURL: hist.srv.URL, Handle: handle, Clock: afterTheWarning()})

	select {
	case <-reached:
	case <-time.After(waitLimit):
		t.Fatalf("only %d connection(s): the stream never reconnected", srv.count())
	}
	// Give a second read, if one were going to happen, time to arrive.
	<-time.After(150 * time.Millisecond)

	if hist.count() != 1 {
		t.Errorf("history was read %d times, want once: the service allows 60 requests a minute", hist.count())
	}
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// waitForStatus blocks until the stream's status contains want.
func waitForStatus(t *testing.T, s *eew.Stream, want string) {
	t.Helper()
	deadline := time.After(waitLimit)
	for !strings.Contains(s.Status(), want) {
		select {
		case <-deadline:
			t.Fatalf("status is %q, want it to contain %q", s.Status(), want)
		case <-time.After(2 * time.Millisecond):
		}
	}
}

func TestStatusSaysWhetherTheStreamIsConnected(t *testing.T) {
	t.Parallel()
	drop := make(chan struct{})
	var mu sync.Mutex
	requests := 0
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		first := requests == 1
		mu.Unlock()
		if !first {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		<-drop
	}))
	t.Cleanup(srv.Close)
	clk := &fakeClock{now: time.Date(2026, time.October, 18, 12, 0, 0, 0, time.UTC)}
	handle, _ := collect()
	stream := eew.NewStream(eew.Config{
		URL: "ws" + strings.TrimPrefix(srv.URL, "http"), Handle: handle, Clock: clk,
		MinBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond,
	})
	if status := stream.Status(); !strings.Contains(status, "not connected yet") {
		t.Errorf("before Run: status = %q, want it to say it has not connected yet", status)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- stream.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	waitForStatus(t, stream, "connected since 2026-10-18 21:00 JST")
	clk.Advance(time.Hour)
	close(drop)

	waitForStatus(t, stream, "NOT connected since 2026-10-18 22:00 JST")
}

func TestWhileConnectingForTheFirstTimeTheStatusDoesNotAlarm(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-release // the service is slow to answer
		http.Error(w, "late", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	// Registered after the server, so it runs before the server is closed: Close waits for a handler that is waiting here.
	t.Cleanup(func() { close(release) })
	clk := &fakeClock{now: time.Date(2026, time.October, 18, 12, 0, 0, 0, time.UTC)}
	handle, _ := collect()
	stream := eew.NewStream(eew.Config{
		URL: "ws" + strings.TrimPrefix(srv.URL, "http"), Handle: handle, Clock: clk,
		MinBackoff: time.Millisecond, MaxBackoff: 5 * time.Millisecond,
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- stream.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	waitForStatus(t, stream, "connecting")
	clk.Advance(time.Minute)

	waitForStatus(t, stream, "NOT connected since")
}

func TestTheStreamStopsAndClosesItsConnectionWhenTheContextEnds(t *testing.T) {
	t.Parallel()
	closed := make(chan struct{})
	connected := make(chan struct{})
	srv := newWS(t, func(_ int, c *websocket.Conn) {
		close(connected)
		drain(c)
		close(closed)
	})
	handle, _ := collect()
	stream := eew.NewStream(
		eew.Config{URL: srv.url, Handle: handle, MinBackoff: time.Millisecond, MaxBackoff: time.Millisecond},
	)
	ctx, cancelRun := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- stream.Run(ctx) }()
	<-connected

	cancelRun()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v, want nil", err)
		}
	case <-time.After(waitLimit):
		t.Fatal("Run did not stop")
	}
	select {
	case <-closed:
	case <-time.After(waitLimit):
		t.Fatal("the connection was left open after shutdown")
	}
}
