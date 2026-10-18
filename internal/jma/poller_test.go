package jma_test

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
	"github.com/tienduc120/japan-emergency-alerts/internal/jma"
)

const waitLimit = 10 * time.Second

type feedItem struct {
	file    string
	id      string // overrides the usual address, to model an entry that points elsewhere
	title   string
	content string
	updated time.Time // zero means a time derived from the entry's place in the feed
}

// fakeClock lets a test decide what time the poller believes it is.
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

func origin() time.Time { return time.Date(2026, time.October, 18, 12, 0, 0, 0, time.UTC) }

// fakeJMA serves feeds and telegrams the way JMA does, with ETags, and counts what was asked for.
type fakeJMA struct {
	mu        sync.Mutex
	feeds     map[string][]feedItem
	files     map[string][]byte
	failFirst map[string]int // telegram file -> how many leading requests answer 500
	feedFails int            // how many leading feed requests answer 500
	hits      map[string]int
	polls     chan struct{}
	srv       *httptest.Server
}

func newFakeJMA(t *testing.T) *fakeJMA {
	t.Helper()
	f := &fakeJMA{
		feeds:     map[string][]feedItem{},
		files:     map[string][]byte{},
		failFirst: map[string]int{},
		hits:      map[string]int{},
		polls:     make(chan struct{}, 4096),
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeJMA) feedBase() string { return f.srv.URL + "/feed/" }
func (f *fakeJMA) dataBase() string { return f.srv.URL + "/data/" }

func (f *fakeJMA) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/feed/"):
		f.serveFeed(w, r, strings.TrimPrefix(r.URL.Path, "/feed/"))
	case strings.HasPrefix(r.URL.Path, "/data/"):
		f.serveFile(w, strings.TrimPrefix(r.URL.Path, "/data/"))
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeJMA) serveFeed(w http.ResponseWriter, r *http.Request, name string) {
	f.mu.Lock()
	f.hits["feed:"+name]++
	if f.feedFails > 0 {
		f.feedFails--
		f.mu.Unlock()
		f.polls <- struct{}{}
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	body := f.atom(f.feeds[name])
	etag := fmt.Sprintf(`"%x"`, hashOf(body))
	conditional := r.Header.Get("If-None-Match") == etag
	if conditional {
		f.hits["conditional:"+name]++
	}
	f.mu.Unlock()
	f.polls <- struct{}{}

	if conditional {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("ETag", etag)
	w.Header().Set("Content-Type", "text/xml")
	_, _ = w.Write([]byte(body))
}

func (f *fakeJMA) serveFile(w http.ResponseWriter, file string) {
	f.mu.Lock()
	f.hits["data:"+file]++
	fail := f.failFirst[file] > 0
	if fail {
		f.failFirst[file]--
	}
	body, ok := f.files[file]
	f.mu.Unlock()

	switch {
	case fail:
		w.WriteHeader(http.StatusInternalServerError)
	case !ok:
		w.WriteHeader(http.StatusNotFound)
	default:
		_, _ = w.Write(body)
	}
}

// atom must be called with the lock held.
func (f *fakeJMA) atom(items []feedItem) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?><feed xmlns="http://www.w3.org/2005/Atom">`)
	for i, it := range items {
		id := it.id
		if id == "" {
			id = f.dataBase() + it.file
		}
		updated := it.updated
		if updated.IsZero() {
			updated = time.Date(2026, time.October, 18, 7, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute)
		}
		b.WriteString("<entry><title>" + escape(it.title) + "</title><id>" + escape(id) + "</id><updated>")
		b.WriteString(
			updated.Format(
				time.RFC3339,
			) + `</updated><content type="text">` + escape(
				it.content,
			) + "</content></entry>",
		)
	}
	b.WriteString("</feed>")
	return b.String()
}

func escape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func hashOf(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

func (f *fakeJMA) put(file string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[file] = data
}

func (f *fakeJMA) add(feed string, item feedItem) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.feeds[feed] = append(f.feeds[feed], item)
}

func (f *fakeJMA) failFeeds(n int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.feedFails = n
}

func (f *fakeJMA) hit(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[key]
}

// waitForPolls blocks until the high-frequency earthquake feed has been requested n times in all.
func (f *fakeJMA) waitForPolls(t *testing.T, n int) {
	t.Helper()
	const feed = "feed:eqvol.xml"
	deadline := time.After(waitLimit)
	for f.hit(feed) < n {
		select {
		case <-f.polls:
		case <-deadline:
			t.Fatalf("%s was requested %d times, want %d", feed, f.hit(feed), n)
		}
	}
}

func quake(file string) feedItem {
	return feedItem{file: file, title: "震源・震度に関する情報", content: "【震源・震度情報】地震がありました。"}
}

const (
	oldQuake = "20261018074002_0_VXSE53_270000.xml"
	newQuake = "20261018050230_0_VXSE53_130000.xml"
)

// start runs a poller against the fake until the test ends.
func start(t *testing.T, f *fakeJMA, handle jma.Handler) {
	t.Helper()
	startWith(t, f, handle, nil)
}

// startWith is start with a chance to change the poller's configuration, and it returns the poller.
func startWith(t *testing.T, f *fakeJMA, handle jma.Handler, tweak func(*jma.Config)) *jma.Poller {
	t.Helper()
	cfg := jma.Config{
		FeedBase:    f.feedBase(),
		DataBase:    f.dataBase(),
		Feeds:       []string{"eqvol.xml"},
		ReplayFeeds: []string{"eqvol_l.xml"},
		Interval:    5 * time.Millisecond,
		Handle:      handle,
	}
	if tweak != nil {
		tweak(&cfg)
	}
	p := jma.NewPoller(cfg)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run returned %v: a context that ends is a clean shutdown", err)
		}
	})
	return p
}

func collect() (jma.Handler, chan alert.Alert) {
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

func TestThePollerRelaysANewTelegramButNotWhatWasAlreadyInTheFirstPoll(t *testing.T) {
	t.Parallel()
	f := newFakeJMA(t)
	f.put(oldQuake, testdata(t, "VXSE53_kumamoto.xml"))
	f.put(newQuake, testdata(t, "VXSE53_strong.xml"))
	f.add("eqvol.xml", quake(oldQuake))
	handle, got := collect()
	start(t, f, handle)
	f.waitForPolls(t, 2)

	f.add("eqvol.xml", quake(newQuake))
	a := next(t, got)
	f.waitForPolls(t, f.hit("feed:eqvol.xml")+5)

	if a.ID != "20261018050230_0_VXSE53_130000" {
		t.Errorf("ID = %q, want the new telegram", a.ID)
	}
	if len(got) != 0 {
		t.Errorf("%d more alerts arrived, want none: each telegram is relayed once", len(got))
	}
	if f.hit("data:"+oldQuake) != 0 || f.hit("data:"+newQuake) != 1 {
		t.Errorf("downloads: old %d, new %d, want 0 and 1: JMA asks clients never to fetch a file twice",
			f.hit("data:"+oldQuake), f.hit("data:"+newQuake))
	}
}

func TestTheFeedIsRequestedConditionallySoNothingIsDownloadedAgain(t *testing.T) {
	t.Parallel()
	f := newFakeJMA(t)
	f.add("eqvol.xml", quake(oldQuake))
	handle, _ := collect()
	start(t, f, handle)

	f.waitForPolls(t, 5)

	if f.hit("conditional:eqvol.xml") == 0 {
		t.Error("an unchanged feed must be answered 304: the request must carry If-None-Match")
	}
}

func TestATelegramThatFailsToDownloadIsTriedAgainOnTheNextPoll(t *testing.T) {
	t.Parallel()
	f := newFakeJMA(t)
	f.put(newQuake, testdata(t, "VXSE53_strong.xml"))
	f.failFirst[newQuake] = 1
	handle, got := collect()
	start(t, f, handle)
	f.waitForPolls(t, 2)

	f.add("eqvol.xml", quake(newQuake))
	a := next(t, got)

	if a.ID != "20261018050230_0_VXSE53_130000" || f.hit("data:"+newQuake) != 2 {
		t.Errorf(
			"ID %q after %d downloads, want the alert after exactly 2: one failure, one retry",
			a.ID,
			f.hit("data:"+newQuake),
		)
	}
}

func TestAnAlertTheHandlerCouldNotDeliverIsOfferedAgainOnTheNextPoll(t *testing.T) {
	t.Parallel()
	f := newFakeJMA(t)
	f.put(newQuake, testdata(t, "VXSE53_strong.xml"))
	var mu sync.Mutex
	calls := 0
	delivered := make(chan struct{}, 1)
	handle := func(context.Context, alert.Alert) error {
		mu.Lock()
		defer mu.Unlock()
		calls++
		if calls == 1 {
			return errors.New("webhook down")
		}
		delivered <- struct{}{}
		return nil
	}
	start(t, f, handle)
	f.waitForPolls(t, 2)

	f.add("eqvol.xml", quake(newQuake))
	select {
	case <-delivered:
	case <-time.After(waitLimit):
		t.Fatal("the alert was never offered a second time")
	}
	f.waitForPolls(t, f.hit("feed:eqvol.xml")+5)

	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Errorf("handler called %d times, want 2: one failure, one success, then done", calls)
	}
}

func TestAFeedThatFailsAtFirstStillGetsItsBaselineBeforeAnythingIsRelayed(t *testing.T) {
	t.Parallel()
	f := newFakeJMA(t)
	f.put(oldQuake, testdata(t, "VXSE53_kumamoto.xml"))
	f.add("eqvol.xml", quake(oldQuake))
	f.feedFails = 2
	handle, got := collect()
	start(t, f, handle)

	f.waitForPolls(t, 6)

	if len(got) != 0 || f.hit("data:"+oldQuake) != 0 {
		t.Errorf(
			"%d alerts, %d downloads, want none: the first good poll is the baseline",
			len(got),
			f.hit("data:"+oldQuake),
		)
	}
}

func TestAnEntryPointingAwayFromJMAIsNeverDownloaded(t *testing.T) {
	t.Parallel()
	f := newFakeJMA(t)
	elsewhere := newFakeJMA(t)
	elsewhere.put(newQuake, testdata(t, "VXSE53_strong.xml"))
	handle, got := collect()
	start(t, f, handle)
	f.waitForPolls(t, 2)

	f.add("eqvol.xml", feedItem{id: elsewhere.dataBase() + newQuake, title: "震源・震度に関する情報"})
	f.waitForPolls(t, f.hit("feed:eqvol.xml")+5)

	if elsewhere.hit("data:"+newQuake) != 0 || len(got) != 0 {
		t.Errorf("%d downloads from elsewhere, %d alerts, want none: a feed must not be able to send us to other hosts",
			elsewhere.hit("data:"+newQuake), len(got))
	}
}

func TestATelegramNobodyWantsIsNotDownloaded(t *testing.T) {
	t.Parallel()
	f := newFakeJMA(t)
	handle, got := collect()
	start(t, f, handle)
	f.waitForPolls(t, 2)
	ash := "20261018110033_0_VFVO53_010000.xml"

	f.add("eqvol.xml", feedItem{file: ash, title: "降灰予報（定時）", content: "降灰予報"})
	f.waitForPolls(t, f.hit("feed:eqvol.xml")+5)

	if f.hit("data:"+ash) != 0 || len(got) != 0 {
		t.Errorf("%d downloads, %d alerts, want none for an ash-fall forecast", f.hit("data:"+ash), len(got))
	}
}

func TestReplayRelaysEveryWantedTelegramInTheLongFeedOldestFirst(t *testing.T) {
	t.Parallel()
	f := newFakeJMA(t)
	f.put(oldQuake, testdata(t, "VXSE53_kumamoto.xml"))
	f.put(newQuake, testdata(t, "VXSE53_strong.xml"))
	f.add("eqvol_l.xml", quake(oldQuake))
	f.add("eqvol_l.xml", feedItem{file: "20261018110033_0_VFVO53_010000.xml", title: "降灰予報（定時）"})
	f.add("eqvol_l.xml", quake(newQuake))
	handle, got := collect()
	p := jma.NewPoller(jma.Config{
		FeedBase: f.feedBase(), DataBase: f.dataBase(), ReplayFeeds: []string{"eqvol_l.xml"}, Handle: handle,
	})

	if err := p.Replay(t.Context()); err != nil {
		t.Fatal(err)
	}

	first, second := next(t, got), next(t, got)
	if first.ID != "20261018074002_0_VXSE53_270000" || second.ID != "20261018050230_0_VXSE53_130000" || len(got) != 0 {
		t.Errorf(
			"got %q then %q (%d more), want the two quakes in feed order and nothing else",
			first.ID,
			second.ID,
			len(got),
		)
	}
}

func TestRunStopsWhenItsContextEnds(t *testing.T) {
	t.Parallel()
	f := newFakeJMA(t)
	handle, _ := collect()
	p := jma.NewPoller(jma.Config{
		FeedBase: f.feedBase(),
		DataBase: f.dataBase(),
		Feeds:    []string{"eqvol.xml"},
		Interval: time.Hour,
		Handle:   handle,
	})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	f.waitForPolls(t, 1)

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v, want nil", err)
		}
	case <-time.After(waitLimit):
		t.Fatal("Run did not stop: waiting an hour for the next poll must not outlast a shutdown")
	}
}
