package jma_test

import (
	"strings"
	"testing"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/jma"
)

const (
	quakeA = "20261018100000_0_VXSE53_270000.xml"
	quakeB = "20261018113000_0_VXSE53_130000.xml"
	quakeC = "20261018030000_0_VXSE53_130000.xml"
)

func quakeAt(file string, updated time.Time) feedItem {
	return feedItem{file: file, title: "震源・震度に関する情報", content: "【震源・震度情報】地震がありました。", updated: updated}
}

func withClock(clk *fakeClock, since time.Time) func(*jma.Config) {
	return func(c *jma.Config) {
		c.Clock = clk
		c.CatchUpSince = since
	}
}

func TestARestartRelaysWhatWasPublishedWhileTheProgramWasOff(t *testing.T) {
	t.Parallel()
	f := newFakeJMA(t)
	f.put(quakeA, testdata(t, "VXSE53_kumamoto.xml"))
	f.put(quakeB, testdata(t, "VXSE53_strong.xml"))
	f.add("eqvol.xml", quakeAt(quakeA, origin().Add(-90*time.Minute)))
	f.add("eqvol.xml", quakeAt(quakeB, origin().Add(-30*time.Minute)))
	handle, got := collect()
	startWith(t, f, handle, withClock(&fakeClock{now: origin()}, origin().Add(-time.Hour)))

	a := next(t, got)
	f.waitForPolls(t, f.hit("feed:eqvol.xml")+5)

	if a.ID != "20261018113000_0_VXSE53_130000" || !a.Late {
		t.Errorf("ID %q, Late %v, want the alert from while the program was off, marked late", a.ID, a.Late)
	}
	if len(got) != 0 || f.hit("data:"+quakeA) != 0 {
		t.Errorf("%d more alerts and %d downloads of the older telegram, want none: it was published before the stop",
			len(got), f.hit("data:"+quakeA))
	}
}

func TestCatchUpReadsTheLongFeedForWhatTheShortOneNoLongerHolds(t *testing.T) {
	t.Parallel()
	f := newFakeJMA(t)
	f.put(quakeA, testdata(t, "VXSE53_kumamoto.xml"))
	f.put(quakeC, testdata(t, "VXSE53_strong.xml"))
	f.add("eqvol_l.xml", quakeAt(quakeC, origin().Add(-4*time.Hour)))
	f.add("eqvol_l.xml", quakeAt(quakeA, origin().Add(-2*time.Hour)))
	handle, got := collect()
	startWith(t, f, handle, withClock(&fakeClock{now: origin()}, origin().Add(-3*time.Hour)))

	a := next(t, got)
	f.waitForPolls(t, f.hit("feed:eqvol.xml")+5)

	if a.ID != "20261018100000_0_VXSE53_270000" || !a.Late || len(got) != 0 {
		t.Errorf("ID %q, Late %v, %d more, want only the telegram published after the program stopped",
			a.ID, a.Late, len(got))
	}
}

func TestCatchUpNeverGoesBackFurtherThanTwelveHours(t *testing.T) {
	t.Parallel()
	f := newFakeJMA(t)
	f.put(quakeA, testdata(t, "VXSE53_kumamoto.xml"))
	f.put(quakeC, testdata(t, "VXSE53_strong.xml"))
	f.add("eqvol_l.xml", quakeAt(quakeC, origin().Add(-20*time.Hour)))
	f.add("eqvol_l.xml", quakeAt(quakeA, origin().Add(-5*time.Hour)))
	handle, got := collect()
	startWith(t, f, handle, withClock(&fakeClock{now: origin()}, origin().Add(-30*time.Hour)))

	a := next(t, got)
	f.waitForPolls(t, f.hit("feed:eqvol.xml")+5)

	if a.ID != "20261018100000_0_VXSE53_270000" || len(got) != 0 {
		t.Errorf("ID %q and %d more, want only the alert from the last twelve hours: older news is not worth a message",
			a.ID, len(got))
	}
}

func TestAFreshInstallRelaysNothingFromThePast(t *testing.T) {
	t.Parallel()
	f := newFakeJMA(t)
	f.put(quakeA, testdata(t, "VXSE53_kumamoto.xml"))
	f.add("eqvol.xml", quakeAt(quakeA, origin().Add(-time.Minute)))
	f.add("eqvol_l.xml", quakeAt(quakeA, origin().Add(-time.Minute)))
	handle, got := collect()
	startWith(t, f, handle, withClock(&fakeClock{now: origin()}, time.Time{}))

	f.waitForPolls(t, 6)

	if len(got) != 0 || f.hit("feed:eqvol_l.xml") != 0 {
		t.Errorf("%d alerts and %d reads of the long feed, want none: with no history there is nothing to catch up on",
			len(got), f.hit("feed:eqvol_l.xml"))
	}
}

func TestAGapWhileRunningIsCaughtUpOnce(t *testing.T) {
	t.Parallel()
	f := newFakeJMA(t)
	f.put(quakeB, testdata(t, "VXSE53_strong.xml"))
	clk := &fakeClock{now: origin()}
	handle, got := collect()
	startWith(t, f, handle, withClock(clk, time.Time{}))
	f.waitForPolls(t, 2)

	f.add("eqvol_l.xml", quakeAt(quakeB, origin().Add(30*time.Minute)))
	clk.Advance(2 * time.Hour) // a laptop that slept
	a := next(t, got)
	f.waitForPolls(t, f.hit("feed:eqvol.xml")+8)

	if a.ID != "20261018113000_0_VXSE53_130000" || !a.Late {
		t.Errorf("ID %q, Late %v, want the alert from during the sleep, marked late", a.ID, a.Late)
	}
	if reads := f.hit("feed:eqvol_l.xml"); reads != 1 {
		t.Errorf(
			"the long feed was read %d times, want once per gap: it is big, and JMA blocks heavy downloaders",
			reads,
		)
	}
}

func TestCatchUpRetriesAFailedDownloadButGivesUp(t *testing.T) {
	t.Parallel()
	f := newFakeJMA(t)
	f.add("eqvol_l.xml", quakeAt(quakeB, origin().Add(-30*time.Minute))) // its telegram is never served
	handle, got := collect()
	startWith(t, f, handle, withClock(&fakeClock{now: origin()}, origin().Add(-time.Hour)))

	f.waitForPolls(t, 40)

	reads := f.hit("feed:eqvol_l.xml")
	if reads < 2 || reads > 5 || len(got) != 0 {
		t.Errorf(
			"long feed read %d times with %d alerts, want a few retries (2 to 5) and then no more",
			reads,
			len(got),
		)
	}
}

func TestAnAlertOnTimeIsNotMarkedLate(t *testing.T) {
	t.Parallel()
	f := newFakeJMA(t)
	f.put(quakeB, testdata(t, "VXSE53_strong.xml"))
	clk := &fakeClock{now: origin()}
	handle, got := collect()
	startWith(t, f, handle, withClock(clk, time.Time{}))
	f.waitForPolls(t, 2)

	f.add("eqvol.xml", quakeAt(quakeB, origin().Add(-time.Minute)))
	a := next(t, got)

	if a.Late {
		t.Error("an alert published a minute ago must not be labelled late")
	}
}

func TestHorizonIsTheLastTimeEveryFeedWasReadInFull(t *testing.T) {
	t.Parallel()
	f := newFakeJMA(t)
	clk := &fakeClock{now: origin()}
	handle, _ := collect()
	p := startWith(t, f, handle, withClock(clk, time.Time{}))
	f.waitForPolls(t, 3)

	if got := p.Horizon(); !got.Equal(origin()) {
		t.Fatalf("Horizon = %v, want %v after a clean read", got, origin())
	}
	if status := p.Status(); !strings.Contains(status, "OK") {
		t.Errorf("Status = %q, want it to say the feeds are fine", status)
	}

	f.failFeeds(1 << 20)
	clk.Advance(10 * time.Minute)
	f.waitForPolls(t, f.hit("feed:eqvol.xml")+5)

	if got := p.Horizon(); !got.Equal(origin()) {
		t.Errorf("Horizon = %v, want it to stay at %v while the feed fails: nothing newer was read", got, origin())
	}
	status := p.Status()
	if !strings.Contains(status, "NOT read since") || !strings.Contains(status, "21:00") {
		t.Errorf("Status = %q, want it to say the feeds have not been read since 21:00 JST", status)
	}
}

func TestStatusBeforeAnythingIsReadSaysSo(t *testing.T) {
	t.Parallel()
	f := newFakeJMA(t)

	p := jma.NewPoller(jma.Config{FeedBase: f.feedBase(), DataBase: f.dataBase(), Clock: &fakeClock{now: origin()}})

	if status := p.Status(); !strings.Contains(status, "not read yet") {
		t.Errorf("Status = %q, want it to say nothing has been read yet", status)
	}
}
