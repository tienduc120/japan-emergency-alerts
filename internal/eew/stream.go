package eew

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
	"github.com/tienduc120/japan-emergency-alerts/internal/async"
	"github.com/tienduc120/japan-emergency-alerts/internal/clock"
)

const (
	defaultURL        = "wss://api.p2pquake.net/v2/ws"
	defaultMinBackoff = time.Second
	defaultMaxBackoff = time.Minute
	backoffFactor     = 2
	// defaultPingEvery is short on purpose. Measured against the live service, a connection pinged every 10 seconds
	// lasted over five minutes, while one pinged every 30 seconds was cut at 60 or 90 seconds.
	defaultPingEvery = 10 * time.Second
	defaultPongWait  = 35 * time.Second
	handshakeTimeout = 15 * time.Second
	writeWait        = 5 * time.Second
	maxFrameBytes    = 1 << 20

	// DefaultHistoryURL is the service's list of its latest early warnings. It allows 60 requests a minute and is
	// about a second slower than the live stream, so it only fills gaps.
	DefaultHistoryURL = "https://api.p2pquake.net/v2/history?codes=556&limit=10"
	// gapWindow is how far back a warning is worth recovering after a reconnect: older than this it is history, not
	// a warning.
	gapWindow = 2 * time.Minute
	// minFillGap stops a flapping connection from asking for history more than the service allows.
	minFillGap      = 10 * time.Second
	historyTimeout  = 10 * time.Second
	maxHistoryBytes = 4 << 20
	// connectingGrace is how long the very first connection may take before the status calls it a failure.
	connectingGrace = 30 * time.Second
	// stableAfter is how long a connection must last before a later failure starts the backoff over, so a service that
	// accepts connections and drops them at once is not hammered.
	stableAfter = time.Minute
	// eventMemory is how long a warning's prefectures are kept so that a cancellation can be sent to the same people.
	eventMemory = 2 * time.Hour
	userAgent   = "japan-emergency-alerts"
)

// Clock is the time source for the stream's memory, so a test can move it.
type Clock interface {
	Now() time.Time
}

// Config is everything a Stream needs; only Handle is required.
type Config struct {
	URL string
	// HistoryURL is read once after each (re)connect for warnings issued in the last two minutes, which covers the
	// seconds a dropped connection would have missed. Empty turns that off.
	HistoryURL string
	Handle     alert.Handler
	Logger     *slog.Logger
	Clock      Clock
	// MinBackoff and MaxBackoff bound the wait before reconnecting; it doubles after each failure.
	MinBackoff, MaxBackoff time.Duration
	// PingEvery is how often the stream pings the service, and PongWait how long it waits for any sign of life before
	// it decides the connection is dead and replaces it.
	PingEvery, PongWait time.Duration
}

type event struct {
	areas []alert.Area
	seen  time.Time
}

// Stream keeps one connection to the service open and hands each warning to Handle.
type Stream struct {
	url                    string
	historyURL             string
	handle                 alert.Handler
	logger                 *slog.Logger
	clock                  Clock
	minBackoff, maxBackoff time.Duration
	pingEvery, pongWait    time.Duration
	history                *http.Client

	// mu guards events and lastFill, which the live reader and the history read both use.
	mu       sync.Mutex
	events   map[string]event
	lastFill time.Time

	// statusMu guards what Status reports, which another goroutine reads while the connection changes.
	statusMu      sync.Mutex
	connected     bool
	everConnected bool
	changed       time.Time
}

func NewStream(cfg Config) *Stream {
	s := &Stream{
		url:        cfg.URL,
		historyURL: cfg.HistoryURL,
		handle:     cfg.Handle,
		logger:     cfg.Logger,
		clock:      cfg.Clock,
		minBackoff: cfg.MinBackoff,
		maxBackoff: cfg.MaxBackoff,
		pingEvery:  cfg.PingEvery,
		pongWait:   cfg.PongWait,
		history: &http.Client{
			Timeout: historyTimeout,
			Transport: &http.Transport{
				Proxy:             http.ProxyFromEnvironment,
				DisableKeepAlives: true,
			},
		},
		events: map[string]event{},
	}
	if s.url == "" {
		s.url = defaultURL
	}
	if s.logger == nil {
		s.logger = slog.New(slog.DiscardHandler)
	}
	if s.clock == nil {
		s.clock = clock.System{}
	}
	if s.minBackoff <= 0 {
		s.minBackoff = defaultMinBackoff
	}
	if s.maxBackoff <= 0 {
		s.maxBackoff = defaultMaxBackoff
	}
	if s.pingEvery <= 0 {
		s.pingEvery = defaultPingEvery
	}
	if s.pongWait <= 0 {
		s.pongWait = defaultPongWait
	}
	return s
}

// Run keeps the connection up until ctx ends, which is a clean shutdown. Whatever goes wrong, it reconnects: the
// service re-sends nothing after an outage, so being connected is the only protection there is.
func (s *Stream) Run(ctx context.Context) error {
	backoff := s.minBackoff
	s.setConnected(false)
	for ctx.Err() == nil {
		started := s.clock.Now()
		err := s.session(ctx)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // the session ended because the program is stopping, which is a clean shutdown
		}
		if s.clock.Now().Sub(started) >= stableAfter {
			backoff = s.minBackoff
		}
		s.logger.WarnContext(ctx, "early-warning stream lost, reconnecting", "error", err, "wait", backoff.String())
		if err = sleep(ctx, backoff); err != nil {
			return nil //nolint:nilerr // the wait was cut short by the program stopping, which is a clean shutdown
		}
		backoff = min(backoff*backoffFactor, s.maxBackoff)
	}
	return nil
}

// dialerFor makes a dialer whose connection is closed when the session ends or is cancelled. The WebSocket library does
// not watch its context while it waits for the server's answer, so without this a stop request, or new settings,
// would wait out the whole handshake timeout when it arrived during the connect.
func dialerFor(session context.Context) websocket.Dialer {
	return websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: handshakeTimeout,
		NetDialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			// Tied to the session, not to ctx: the library cancels its own context the moment the dial succeeds.
			context.AfterFunc(session, func() { _ = conn.Close() })
			return conn, nil
		},
	}
}

// session is one connection, from dial to the first failure.
func (s *Stream) session(ctx context.Context) error {
	sessionCtx, endSession := context.WithCancel(ctx)
	defer endSession()
	dialer := dialerFor(sessionCtx)
	conn, resp, err := dialer.DialContext(sessionCtx, s.url, http.Header{"User-Agent": {userAgent}})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	conn.SetReadLimit(maxFrameBytes)
	// Any frame, or the answer to a ping, proves the connection is alive; silence for pongWait does not.
	extend := func() { _ = conn.SetReadDeadline(time.Now().Add(s.pongWait)) }
	extend()
	conn.SetPongHandler(func(string) error {
		extend()
		return nil
	})
	s.logger.InfoContext(ctx, "early-warning stream connected")
	s.setConnected(true)
	defer s.setConnected(false)

	err = async.Run(ctx, s.read(conn, extend), s.ping(conn), s.catchUp, closeWhenDone(conn))
	if ctx.Err() != nil {
		return nil
	}
	return err
}

func (s *Stream) setConnected(connected bool) {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	s.connected = connected
	s.everConnected = s.everConnected || connected
	s.changed = s.clock.Now()
}

// Status is one line about the connection, for the heartbeat and the settings page. Setting up the first connection
// takes a few seconds, so for the first half minute it says "connecting" instead of raising an alarm.
func (s *Stream) Status() string {
	s.statusMu.Lock()
	defer s.statusMu.Unlock()
	switch {
	case s.changed.IsZero():
		return "Early-warning stream: not connected yet"
	case s.connected:
		return "Early-warning stream: connected since " + inJST(s.changed)
	case !s.everConnected && s.clock.Now().Sub(s.changed) < connectingGrace:
		return "Early-warning stream: connecting…"
	default:
		return "Early-warning stream: NOT connected since " + inJST(s.changed)
	}
}

func inJST(t time.Time) string {
	return t.In(time.FixedZone("JST", jstOffset)).Format("2006-01-02 15:04") + " JST"
}

func (s *Stream) read(conn *websocket.Conn, extend func()) func(context.Context) error {
	return func(ctx context.Context) error {
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return err
			}
			extend()
			s.frame(ctx, data)
		}
	}
}

func (s *Stream) ping(conn *websocket.Conn) func(context.Context) error {
	return func(ctx context.Context) error {
		ticker := time.NewTicker(s.pingEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait)); err != nil {
					return err
				}
			}
		}
	}
}

// closeWhenDone closes the connection politely when the session ends, which also unblocks the reader.
func closeWhenDone(conn *websocket.Conn) func(context.Context) error {
	return func(ctx context.Context) error {
		<-ctx.Done()
		bye := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		_ = conn.WriteControl(websocket.CloseMessage, bye, time.Now().Add(writeWait))
		return conn.Close()
	}
}

// frame handles one message. Nothing in a message can stop the stream: a bad one is logged and the next is read.
func (s *Stream) frame(ctx context.Context, data []byte) {
	f, ok, err := Decode(data)
	if err != nil {
		s.logger.WarnContext(ctx, "early-warning message not read cleanly", "relayed", ok, "error", err)
	}
	if ok {
		s.dispatch(ctx, f)
	}
}

// dispatch turns a warning or a cancellation into an alert and hands it on.
func (s *Stream) dispatch(ctx context.Context, f Frame) {
	a, ok := s.alertFor(ctx, f)
	if !ok {
		return
	}
	if err := s.handle(ctx, a); err != nil {
		s.logger.ErrorContext(ctx, "early warning not delivered", "id", a.ID, "error", err)
	}
}

func (s *Stream) alertFor(ctx context.Context, f Frame) (alert.Alert, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	s.forgetOld(now)

	if f.Cancelled {
		known, seen := s.events[f.EventID]
		if !seen {
			s.logger.InfoContext(ctx, "cancellation of an unknown warning ignored", "event", f.EventID)
			return alert.Alert{}, false
		}
		return alert.Alert{
			ID:     f.EventID + "-cancel",
			Kind:   alert.KindEarlyWarning,
			Issued: f.Issued,
			Areas:  known.areas,
			Text:   "【緊急地震速報（警報）】先ほどの緊急地震速報（警報）は取り消されました。",
		}, true
	}
	// One ID for every update of an event, so the filter sends the first message that reaches a person's area and not
	// a new message each second as the forecast is refined.
	s.events[f.EventID] = event{areas: mergeAreas(s.events[f.EventID].areas, f.Areas), seen: now}
	return alert.Alert{
		ID:     f.EventID,
		Kind:   alert.KindEarlyWarning,
		Issued: f.Issued,
		Areas:  f.Areas,
		Text:   f.Text,
	}, true
}

// catchUp reads the service's latest warnings once and relays those issued in the last two minutes. The live stream
// re-sends nothing after a drop, so this is what covers the seconds between the drop and the reconnect. It never fails
// the session: if history cannot be read, the live stream carries on alone.
func (s *Stream) catchUp(ctx context.Context) error {
	if s.historyURL == "" {
		return nil
	}
	now := s.clock.Now()
	s.mu.Lock()
	tooSoon := !s.lastFill.IsZero() && now.Sub(s.lastFill) < minFillGap
	s.mu.Unlock()
	if tooSoon {
		return nil
	}

	messages, err := s.recent(ctx)
	if ctx.Err() != nil {
		//nolint:nilerr // the connection dropped again before the answer came; the next session tries afresh
		return nil
	}
	s.mu.Lock()
	s.lastFill = now
	s.mu.Unlock()
	if err != nil {
		s.logger.WarnContext(ctx, "recent early warnings could not be read", "error", err)
		return nil
	}
	// The service lists the newest first; relay the oldest first, so a cancellation follows its warning.
	for _, raw := range slices.Backward(messages) {
		f, ok, derr := Decode(raw)
		if derr != nil {
			s.logger.WarnContext(ctx, "early-warning message not read cleanly", "relayed", ok, "error", derr)
		}
		if ok && !f.Issued.IsZero() && now.Sub(f.Issued) <= gapWindow {
			s.dispatch(ctx, f)
		}
	}
	return nil
}

func (s *Stream) recent(ctx context.Context) ([]json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.historyURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.history.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("history answered %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHistoryBytes))
	if err != nil {
		return nil, err
	}
	var messages []json.RawMessage
	if err = json.Unmarshal(body, &messages); err != nil {
		return nil, fmt.Errorf("read history: %w", err)
	}
	return messages, nil
}

func (s *Stream) forgetOld(now time.Time) {
	for id, e := range s.events {
		if now.Sub(e.seen) >= eventMemory {
			delete(s.events, id)
		}
	}
}

// mergeAreas adds the areas of an update to those already known, keeping each prefecture once.
func mergeAreas(known, update []alert.Area) []alert.Area {
	merged := append([]alert.Area(nil), known...)
	for _, area := range update {
		found := false
		for _, have := range merged {
			if have.Prefecture == area.Prefecture {
				found = true
				break
			}
		}
		if !found {
			merged = append(merged, area)
		}
	}
	return merged
}

// sleep waits for d or until ctx ends, whichever comes first.
func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
