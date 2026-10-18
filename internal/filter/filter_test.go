package filter_test

import (
	"context"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
	"github.com/tienduc120/japan-emergency-alerts/internal/async"
	"github.com/tienduc120/japan-emergency-alerts/internal/filter"
	"github.com/tienduc120/japan-emergency-alerts/internal/intensity"
	"github.com/tienduc120/japan-emergency-alerts/internal/region"
)

const (
	hokkaido = 1
	tokyo    = 13
	kanagawa = 14
	osaka    = 27
	okinawa  = 47
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func newClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, time.October, 18, 12, 0, 0, 0, time.UTC)}
}

func inTokyo(t *testing.T, clock *fakeClock) *filter.Filter {
	t.Helper()
	p, ok := region.Lookup("Tokyo")
	if !ok {
		t.Fatal("Tokyo must exist")
	}
	return filter.New(region.InPrefecture(p), intensity.FiveLower, clock)
}

func everywhere(clock *fakeClock, floor intensity.Scale) *filter.Filter {
	return filter.New(region.Everywhere(), floor, clock)
}

// warning is any non-earthquake-report alert covering the given prefectures.
func warning(id string, kind alert.Kind, prefectures ...int) alert.Alert {
	a := alert.Alert{ID: id, Kind: kind}
	for _, p := range prefectures {
		a.Areas = append(a.Areas, alert.Area{Prefecture: p})
	}
	return a
}

func report(id string, shaking map[int]intensity.Scale) alert.Alert {
	a := alert.Alert{ID: id, Kind: alert.KindEarthquakeReport}
	for p, level := range shaking {
		a.Areas = append(a.Areas, alert.Area{Prefecture: p, Intensity: level})
	}
	return a
}

func TestEveryKindOfWarningForTheWatchedPrefectureIsSent(t *testing.T) {
	t.Parallel()

	kinds := map[string]alert.Kind{
		"early warning":   alert.KindEarlyWarning,
		"tsunami":         alert.KindTsunami,
		"special warning": alert.KindSpecialWarning,
		"volcano":         alert.KindVolcano,
	}
	for name, kind := range kinds {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := inTokyo(t, newClock())

			got := f.Decide(warning("a", kind, tokyo))

			if got != filter.Send {
				t.Errorf("Decide = %v, want Send: the intensity threshold is for earthquake reports only", got)
			}
		})
	}
}

func TestAnAlertForOtherPrefecturesOnlyIsDropped(t *testing.T) {
	t.Parallel()
	f := inTokyo(t, newClock())

	got := f.Decide(warning("a", alert.KindTsunami, hokkaido, okinawa))

	if got != filter.DropRegion {
		t.Errorf("Decide = %v, want DropRegion", got)
	}
}

func TestOneMatchingAreaAmongSeveralIsEnough(t *testing.T) {
	t.Parallel()
	f := inTokyo(t, newClock())

	got := f.Decide(warning("a", alert.KindSpecialWarning, hokkaido, tokyo, osaka))

	if got != filter.Send {
		t.Errorf("Decide = %v, want Send", got)
	}
}

func TestAnAlertWithNoAreasIsDroppedForASinglePrefecture(t *testing.T) {
	t.Parallel()
	f := inTokyo(t, newClock())

	got := f.Decide(warning("a", alert.KindVolcano))

	if got != filter.DropRegion {
		t.Errorf("Decide = %v, want DropRegion: with no area we cannot say it matters here", got)
	}
}

func TestAReportBelowTheThresholdHereIsDroppedEvenIfStrongElsewhere(t *testing.T) {
	t.Parallel()
	f := inTokyo(t, newClock())

	got := f.Decide(report("a", map[int]intensity.Scale{tokyo: intensity.Four, hokkaido: intensity.Seven}))

	if got != filter.DropIntensity {
		t.Errorf("Decide = %v, want DropIntensity: only the shaking in the watched prefecture counts", got)
	}
}

func TestAReportAtOrAboveTheThresholdHereIsSent(t *testing.T) {
	t.Parallel()

	for _, level := range []intensity.Scale{intensity.FiveLower, intensity.FiveUpper, intensity.Seven} {
		t.Run(level.String(), func(t *testing.T) {
			t.Parallel()
			f := inTokyo(t, newClock())

			got := f.Decide(report("a", map[int]intensity.Scale{tokyo: level}))

			if got != filter.Send {
				t.Errorf("shaking %v with threshold 5-: Decide = %v, want Send", level, got)
			}
		})
	}
}

func TestAReportForAnotherPrefectureIsAMismatchOfRegionNotIntensity(t *testing.T) {
	t.Parallel()
	f := inTokyo(t, newClock())

	got := f.Decide(report("a", map[int]intensity.Scale{hokkaido: intensity.Seven}))

	if got != filter.DropRegion {
		t.Errorf("Decide = %v, want DropRegion", got)
	}
}

func TestAllJapanSendsAWarningForAnyPrefectureEvenWithoutAreas(t *testing.T) {
	t.Parallel()
	f := everywhere(newClock(), intensity.FiveLower)

	for i, a := range []alert.Alert{
		warning("a", alert.KindTsunami, okinawa),
		warning("b", alert.KindVolcano),
	} {
		if got := f.Decide(a); got != filter.Send {
			t.Errorf("alert %d: Decide = %v, want Send", i, got)
		}
	}
}

func TestAllJapanJudgesAReportByTheStrongestShakingAnywhere(t *testing.T) {
	t.Parallel()
	f := everywhere(newClock(), intensity.FiveLower)

	weak := f.Decide(report("weak", map[int]intensity.Scale{okinawa: intensity.Three, hokkaido: intensity.Four}))
	strong := f.Decide(
		report("strong", map[int]intensity.Scale{okinawa: intensity.Three, hokkaido: intensity.FiveUpper}),
	)
	empty := f.Decide(report("empty", nil))

	if weak != filter.DropIntensity || strong != filter.Send || empty != filter.DropIntensity {
		t.Errorf("weak, strong, empty = %v, %v, %v, want DropIntensity, Send, DropIntensity", weak, strong, empty)
	}
}

func TestTheSameAlertIsSentOnce(t *testing.T) {
	t.Parallel()
	f := inTokyo(t, newClock())
	a := warning("same", alert.KindTsunami, tokyo)

	first := f.Decide(a)
	second := f.Decide(a)

	if first != filter.Send || second != filter.DropDuplicate {
		t.Errorf("first, second = %v, %v, want Send, DropDuplicate", first, second)
	}
}

func TestADuplicateIsForgottenOnceTheMemoryExpires(t *testing.T) {
	t.Parallel()
	clock := newClock()
	f := inTokyo(t, clock)
	a := warning("same", alert.KindTsunami, tokyo)
	f.Decide(a)

	clock.Advance(filter.RememberFor - time.Minute)
	stillRemembered := f.Decide(a)
	clock.Advance(2 * time.Minute)
	forgotten := f.Decide(a)

	if stillRemembered != filter.DropDuplicate || forgotten != filter.Send {
		t.Errorf("before, after expiry = %v, %v, want DropDuplicate, Send", stillRemembered, forgotten)
	}
}

func TestADroppedAlertIsNotRememberedSoAnUpdateCanStillArrive(t *testing.T) {
	t.Parallel()
	f := inTokyo(t, newClock())

	first := f.Decide(warning("event", alert.KindEarlyWarning, hokkaido))
	update := f.Decide(warning("event", alert.KindEarlyWarning, hokkaido, tokyo))

	if first != filter.DropRegion || update != filter.Send {
		t.Errorf("first, update = %v, %v, want DropRegion, Send: the warning grew to cover us", first, update)
	}
}

func TestForgetLetsAnAlertThatFailedToSendBeTriedAgain(t *testing.T) {
	t.Parallel()
	f := inTokyo(t, newClock())
	a := warning("same", alert.KindTsunami, tokyo)
	first := f.Decide(a)

	f.Forget("same")
	f.Forget("never-seen")
	second := f.Decide(a)

	if first != filter.Send || second != filter.Send {
		t.Errorf(
			"first, second = %v, %v, want Send both times: a lost alert must not be remembered as sent",
			first,
			second,
		)
	}
	if f.Remembered() != 1 {
		t.Errorf("Remembered = %d, want 1", f.Remembered())
	}
}

func TestRememberMakesAnAlertAnEarlierRunSentCountAsSent(t *testing.T) {
	t.Parallel()
	clock := newClock()
	f := inTokyo(t, clock)

	f.Remember("sent-before", clock.Now().Add(-time.Hour))
	f.Remember("too-old", clock.Now().Add(-filter.RememberFor-time.Minute))

	if got := f.Decide(warning("sent-before", alert.KindTsunami, tokyo)); got != filter.DropDuplicate {
		t.Errorf("Decide = %v, want DropDuplicate: a restart must not send it again", got)
	}
	if got := f.Decide(warning("too-old", alert.KindTsunami, tokyo)); got != filter.Send {
		t.Errorf("Decide = %v, want Send: past the memory, an old ID is just a new alert", got)
	}
}

func TestAnAlertWithoutAnIDIsDropped(t *testing.T) {
	t.Parallel()
	f := inTokyo(t, newClock())

	got := f.Decide(warning("", alert.KindTsunami, tokyo))

	if got != filter.DropNoID {
		t.Errorf("Decide = %v, want DropNoID: it could never be told apart from a repeat", got)
	}
}

func TestTheMemoryDoesNotGrowWithoutLimit(t *testing.T) {
	t.Parallel()
	clock := newClock()
	f := inTokyo(t, clock)
	for i := range 100 {
		f.Decide(warning(strconv.Itoa(i), alert.KindTsunami, tokyo))
	}
	if f.Remembered() != 100 {
		t.Fatalf("Remembered = %d, want 100", f.Remembered())
	}

	clock.Advance(filter.RememberFor + time.Minute)
	f.Decide(warning("fresh", alert.KindTsunami, tokyo))

	if f.Remembered() != 1 {
		t.Errorf("Remembered = %d, want 1: expired alerts must be dropped from memory", f.Remembered())
	}
}

func TestEveryDecisionHasADistinctNameForTheLog(t *testing.T) {
	t.Parallel()

	seen := map[string]filter.Decision{}
	for _, d := range []filter.Decision{
		filter.Send, filter.DropRegion, filter.DropIntensity, filter.DropDuplicate, filter.DropNoID,
	} {
		name := d.String()
		if name == "" || name == "unknown" {
			t.Errorf("decision %d has no name", d)
		}
		if other, ok := seen[name]; ok {
			t.Errorf("decisions %d and %d are both called %q", d, other, name)
		}
		seen[name] = d
	}
}

func TestSourcesRunningAtOnceShareOneFilterSafely(t *testing.T) {
	t.Parallel()
	f := inTokyo(t, newClock())
	const sources, perSource = 8, 25

	var sent atomic.Int32
	tasks := make([]func(context.Context) error, sources)
	for source := range tasks {
		tasks[source] = func(context.Context) error {
			// Every source sees the shared alert, as when the stream and the feed both carry it.
			alerts := []alert.Alert{warning("shared", alert.KindTsunami, tokyo)}
			for i := range perSource {
				alerts = append(alerts, warning(strconv.Itoa(source)+"-"+strconv.Itoa(i), alert.KindTsunami, tokyo))
			}
			for _, a := range alerts {
				if f.Decide(a) == filter.Send {
					sent.Add(1)
				}
			}
			return nil
		}
	}

	if err := async.Run(t.Context(), tasks...); err != nil {
		t.Fatal(err)
	}

	if want := int32(1 + sources*perSource); sent.Load() != want {
		t.Errorf("sent %d alerts, want %d: each unique alert once, and the shared one exactly once", sent.Load(), want)
	}
	if f.Remembered() != 1+sources*perSource {
		t.Errorf("Remembered = %d, want %d", f.Remembered(), 1+sources*perSource)
	}
}
