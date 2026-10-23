package jma

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
	"github.com/tienduc120/japan-emergency-alerts/internal/clock"
)

const (
	defaultFeedBase = "https://www.data.jma.go.jp/developer/xml/feed/"
	defaultDataBase = "https://www.data.jma.go.jp/developer/xml/data/"
	// defaultInterval matches how often JMA updates the feeds and how long its CDN caches them.
	defaultInterval  = time.Minute
	requestTimeout   = 15 * time.Second
	maxFeedBytes     = 8 << 20
	maxTelegramBytes = 4 << 20
	// rememberFor is longer than the ten minutes a high-frequency feed keeps an entry, so a telegram is never seen twice.
	rememberFor = time.Hour
	userAgent   = "japan-emergency-alerts"

	// staleAfter is how long the feeds may go unread before the program assumes it missed something and catches up.
	staleAfter = 5 * time.Minute
	// lateAfter is how old a telegram must be when it is handed on to be marked late.
	lateAfter = 10 * time.Minute
	// maxCatchUp limits how far back catching up goes: news from yesterday is not worth a message.
	maxCatchUp = 12 * time.Hour
	// maxCatchUpTries bounds the downloads of the long feeds, which are large, when a telegram keeps failing.
	maxCatchUpTries = 5
	jstOffset       = 9 * 60 * 60
)

// Handler takes one alert. Returning an error asks the poller to offer the same alert again on its next poll.
type Handler = alert.Handler

// Clock is the time source for the poller's memory, so a test can move it.
type Clock interface {
	Now() time.Time
}

// Config is everything a Poller needs; only Handle is required.
type Config struct {
	Client *http.Client
	// FeedBase and DataBase default to JMA's own addresses. A telegram is only downloaded from under DataBase, so a
	// feed cannot send the program to another host.
	FeedBase string
	DataBase string
	// Feeds are polled every Interval. They default to the high-frequency earthquake/volcano and "extra" feeds.
	Feeds []string
	// ReplayFeeds are the long feeds Replay reads; they hold the last few days.
	ReplayFeeds []string
	Interval    time.Duration
	Handle      Handler
	Logger      *slog.Logger
	Clock       Clock
	// CatchUpSince is when an earlier run last read the feeds in full. Anything published after it is relayed, marked
	// late; zero means a first run, which relays nothing from the past.
	CatchUpSince time.Time
}

// Poller watches JMA's feeds and hands new, relevant alerts to Handle.
type Poller struct {
	client      *http.Client
	feedBase    string
	dataBase    string
	feeds       []string
	replayFeeds []string
	interval    time.Duration
	handle      Handler
	logger      *slog.Logger
	clock       Clock

	etag    map[string]string
	primed  map[string]bool
	seen    map[string]time.Time
	episode catchUpEpisode

	mu      sync.Mutex
	horizon time.Time
	started time.Time
}

// catchUpEpisode is the state of catching up on one gap, so the large long feeds are read once per gap, and only a few
// times if a telegram keeps failing.
type catchUpEpisode struct {
	done  bool
	tries int
	feeds map[string]bool
}

func NewPoller(cfg Config) *Poller {
	p := &Poller{
		client:      cfg.Client,
		feedBase:    cfg.FeedBase,
		dataBase:    cfg.DataBase,
		feeds:       cfg.Feeds,
		replayFeeds: cfg.ReplayFeeds,
		interval:    cfg.Interval,
		handle:      cfg.Handle,
		logger:      cfg.Logger,
		clock:       cfg.Clock,
		etag:        map[string]string{},
		primed:      map[string]bool{},
		seen:        map[string]time.Time{},
		episode:     catchUpEpisode{feeds: map[string]bool{}},
		horizon:     cfg.CatchUpSince,
	}
	if p.client == nil {
		p.client = &http.Client{
			Timeout: requestTimeout,
			Transport: &http.Transport{
				Proxy:               http.ProxyFromEnvironment,
				DisableKeepAlives:   true,
				TLSHandshakeTimeout: requestTimeout,
			},
		}
	}
	if p.feedBase == "" {
		p.feedBase = defaultFeedBase
	}
	if p.dataBase == "" {
		p.dataBase = defaultDataBase
	}
	if len(p.feeds) == 0 {
		p.feeds = []string{"eqvol.xml", "extra.xml"}
	}
	if len(p.replayFeeds) == 0 {
		p.replayFeeds = []string{"eqvol_l.xml", "extra_l.xml"}
	}
	if p.interval <= 0 {
		p.interval = defaultInterval
	}
	if p.logger == nil {
		p.logger = slog.New(slog.DiscardHandler)
	}
	if p.clock == nil {
		p.clock = clock.System{}
	}
	return p
}

// Run polls until ctx ends, which is a clean shutdown. A feed that cannot be reached is logged and tried again on the
// next poll; it never stops the loop.
//
// On a first run the first good poll of each feed is only a baseline: what the feed already holds was published before
// this program was watching, so it is remembered, not relayed. When an earlier run left a horizon, or when the feeds
// have gone unread for longer than staleAfter (a sleeping laptop, a network outage), anything published since is relayed
// instead, marked late.
func (p *Poller) Run(ctx context.Context) error {
	p.mu.Lock()
	p.started = p.clock.Now()
	p.mu.Unlock()
	for ctx.Err() == nil {
		p.cycle(ctx)
		if err := sleep(ctx, p.interval); err != nil {
			return nil //nolint:nilerr // the wait was cut short by the program stopping, which is a clean shutdown
		}
	}
	return nil
}

// cycle is one round: catch up if the feeds have gone unread for too long, read every feed, and if all went well move
// the horizon to now.
func (p *Poller) cycle(ctx context.Context) {
	now := p.clock.Now()
	horizon := p.Horizon()
	complete := true

	if !horizon.IsZero() && now.Sub(horizon) > staleAfter && !p.episode.done {
		p.episode.tries++
		finished := p.catchUp(ctx, later(horizon, now.Add(-maxCatchUp)), now)
		if finished || p.episode.tries >= maxCatchUpTries {
			p.episode.done = true
		} else {
			complete = false
		}
	}
	for _, name := range p.feeds {
		if err := p.pollFeed(ctx, name, horizon); err != nil {
			complete = false
			if ctx.Err() == nil {
				p.logger.WarnContext(ctx, "feed poll failed", "feed", name, "error", err)
			}
		}
	}
	if complete && ctx.Err() == nil {
		p.setHorizon(now)
	}
}

// catchUp reads the long feeds for telegrams published after since and hands them on, marked late. It returns true when
// there is nothing left to retry. A feed that was finished is not read again for the same gap.
func (p *Poller) catchUp(ctx context.Context, since, now time.Time) bool {
	finished := true
	for _, name := range p.replayFeeds {
		if p.episode.feeds[name] {
			continue
		}
		entries, _, err := p.readFeed(ctx, name, "")
		if err != nil {
			p.logger.WarnContext(ctx, "catching up failed to read a feed, will retry", "feed", name, "error", err)
			finished = false
			continue
		}
		if p.relayMissed(ctx, entries, since, now) {
			p.episode.feeds[name] = true
		} else {
			finished = false
		}
	}
	return finished
}

// relayMissed hands on the wanted entries published after since that have not been seen, oldest first, and says whether
// every one of them was delivered.
func (p *Poller) relayMissed(ctx context.Context, entries []Entry, since, now time.Time) bool {
	sortOldestFirst(entries)
	delivered := true
	for _, e := range entries {
		if !Wanted(e) || !e.Updated.After(since) {
			continue
		}
		if _, done := p.seen[e.ID]; done {
			continue
		}
		if p.deliver(ctx, e) {
			p.seen[e.ID] = now
		} else {
			delivered = false
		}
	}
	return delivered
}

// Horizon is when every feed was last read in full: the point up to which nothing can have been missed. It is zero until
// the first clean round.
func (p *Poller) Horizon() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.horizon
}

// setHorizon also starts a fresh catch-up episode, because a new gap can only open after this moment.
func (p *Poller) setHorizon(t time.Time) {
	p.mu.Lock()
	p.horizon = t
	p.mu.Unlock()
	p.episode = catchUpEpisode{feeds: map[string]bool{}}
}

// Status is one line about the feeds, for the heartbeat and the settings page.
func (p *Poller) Status() string {
	now := p.clock.Now()
	health, horizon, started := p.health(now)
	switch {
	case health == alert.Starting:
		return "JMA feeds: not read yet"
	case horizon.IsZero():
		return "JMA feeds: NOT read yet, trying since " + inJST(started)
	case health == alert.Down:
		return "JMA feeds: NOT read since " + inJST(horizon)
	case now.Sub(horizon) < time.Minute:
		return "JMA feeds: OK, last read in full less than a minute ago"
	default:
		return fmt.Sprintf("JMA feeds: OK, last read in full %d minutes ago", int(now.Sub(horizon).Minutes()))
	}
}

// Health is the light beside Status on the settings page.
func (p *Poller) Health() alert.Health {
	health, _, _ := p.health(p.clock.Now())
	return health
}

// health is read once for Status and Health, so the line and its light agree. Feeds never read count from the start, so
// a network that never lets them through (a hotel's login page) turns the light red instead of leaving it waiting.
func (p *Poller) health(now time.Time) (alert.Health, time.Time, time.Time) {
	p.mu.Lock()
	horizon, started := p.horizon, p.started
	p.mu.Unlock()
	switch {
	case horizon.IsZero() && (started.IsZero() || now.Sub(started) <= staleAfter):
		return alert.Starting, horizon, started
	case horizon.IsZero(), now.Sub(horizon) > staleAfter:
		return alert.Down, horizon, started
	default:
		return alert.Up, horizon, started
	}
}

func inJST(t time.Time) string {
	return t.In(time.FixedZone("JST", jstOffset)).Format("2006-01-02 15:04") + " JST"
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// Replay reads the long feeds once, oldest first, and hands every relevant telegram to Handle, whatever was seen
// before. It exists so a person can check what the program would have done over the last few days.
func (p *Poller) Replay(ctx context.Context) error {
	for _, name := range p.replayFeeds {
		entries, _, err := p.readFeed(ctx, name, "")
		if err != nil {
			return err
		}
		sortOldestFirst(entries)
		for _, e := range entries {
			if Wanted(e) {
				p.deliver(ctx, e)
			}
		}
	}
	return nil
}

func (p *Poller) pollFeed(ctx context.Context, name string, horizon time.Time) error {
	entries, etag, err := p.readFeed(ctx, name, p.etag[name])
	if err != nil {
		return err
	}
	if entries == nil && etag == "" {
		return nil // 304: nothing changed
	}
	if p.process(ctx, name, entries, horizon) {
		// Something must be offered again, so the next poll must read the feed in full, not be told "unchanged".
		delete(p.etag, name)
	} else {
		p.etag[name] = etag
	}
	return nil
}

// process handles the entries it has not seen and reports whether any must be tried again. On a feed's first read,
// entries from before the horizon are only remembered; with no horizon, which is a first run, all of them are.
func (p *Poller) process(ctx context.Context, feed string, entries []Entry, horizon time.Time) bool {
	now := p.clock.Now()
	sortOldestFirst(entries)
	baseline := !p.primed[feed]
	p.primed[feed] = true

	retry := false
	for _, e := range entries {
		if _, done := p.seen[e.ID]; done {
			continue
		}
		if !Wanted(e) || (baseline && (horizon.IsZero() || !e.Updated.After(horizon))) {
			p.seen[e.ID] = now
			continue
		}
		if p.deliver(ctx, e) {
			p.seen[e.ID] = now
		} else {
			retry = true
		}
	}
	for id, at := range p.seen {
		if now.Sub(at) >= rememberFor {
			delete(p.seen, id)
		}
	}
	return retry
}

// deliver downloads one telegram, decodes it and hands it on. It returns false when the entry should be tried again.
func (p *Poller) deliver(ctx context.Context, e Entry) bool {
	if !strings.HasPrefix(e.ID, p.dataBase) {
		p.logger.WarnContext(ctx, "telegram outside JMA's data address ignored", "name", e.Name())
		return true
	}
	data, err := p.get(ctx, e.ID, "", maxTelegramBytes)
	if err != nil {
		p.logger.WarnContext(ctx, "telegram download failed, will retry", "name", e.Name(), "error", err)
		return false
	}
	a, relay, err := Decode(e, data.body)
	if err != nil {
		level := slog.LevelError
		if relay {
			level = slog.LevelWarn
		}
		p.logger.Log(ctx, level, "telegram not read cleanly", "name", e.Name(), "relayed", relay, "error", err)
	}
	if !relay {
		return true
	}
	a.Late = !e.Updated.IsZero() && p.clock.Now().Sub(e.Updated) > lateAfter
	if err = p.handle(ctx, a); err != nil {
		p.logger.ErrorContext(ctx, "alert not delivered, will retry", "id", a.ID, "error", err)
		return false
	}
	return true
}

// readFeed downloads and parses a feed. A feed that has not changed since etag gives no entries and no new etag.
func (p *Poller) readFeed(ctx context.Context, name, etag string) ([]Entry, string, error) {
	got, err := p.get(ctx, p.feedBase+name, etag, maxFeedBytes)
	if err != nil {
		return nil, "", fmt.Errorf("download feed %s: %w", name, err)
	}
	if got.notModified {
		return nil, "", nil
	}
	entries, err := ParseFeed(bytes.NewReader(got.body))
	if err != nil {
		return nil, "", fmt.Errorf("feed %s: %w", name, err)
	}
	if entries == nil {
		entries = []Entry{}
	}
	return entries, got.etag, nil
}

type download struct {
	body        []byte
	etag        string
	notModified bool
}

func (p *Poller) get(ctx context.Context, address, etag string, limit int64) (download, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return download{}, err
	}
	req.Header.Set("User-Agent", userAgent)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return download{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusNotModified:
		return download{notModified: true}, nil
	case http.StatusOK:
	default:
		return download{}, fmt.Errorf("%s answered %d %s", address, resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return download{}, err
	}
	if int64(len(body)) > limit {
		return download{}, fmt.Errorf("%s is larger than %d bytes", address, limit)
	}
	return download{body: body, etag: resp.Header.Get("ETag")}, nil
}

func sortOldestFirst(entries []Entry) {
	slices.SortStableFunc(entries, func(a, b Entry) int { return a.Updated.Compare(b.Updated) })
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
