// Package filter decides which alerts are worth sending: the right place, strong enough, and not sent before. It does
// no I/O, so every rule can be tested with plain values.
package filter

import (
	"maps"
	"sync"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
	"github.com/tienduc120/japan-emergency-alerts/internal/intensity"
	"github.com/tienduc120/japan-emergency-alerts/internal/region"
)

// RememberFor is how long a sent alert is remembered. It outlasts the recent-events window of every source, and the
// memory stays small because older entries are dropped.
const RememberFor = 24 * time.Hour

// Decision is the verdict on one alert, and its name is what the log shows when an alert was not sent.
type Decision int

const (
	Send Decision = iota + 1
	DropRegion
	DropIntensity
	DropDuplicate
	DropNoID
)

func (d Decision) String() string {
	switch d {
	case Send:
		return "send"
	case DropRegion:
		return "outside the watched location"
	case DropIntensity:
		return "below the intensity threshold"
	case DropDuplicate:
		return "already sent"
	case DropNoID:
		return "no id"
	default:
		return "unknown"
	}
}

// Clock is the time source, so a test can move it.
type Clock interface {
	Now() time.Time
}

// Filter is safe for several sources to share: the early-warning stream and the feed poller call it at the same time.
type Filter struct {
	location region.Location
	min      intensity.Scale
	clock    Clock

	mu   sync.Mutex
	seen map[string]time.Time
}

func New(location region.Location, minIntensity intensity.Scale, clock Clock) *Filter {
	return &Filter{location: location, min: minIntensity, clock: clock, seen: map[string]time.Time{}}
}

// Decide judges an alert and, only when it says Send, remembers the ID. An alert that was dropped for its place is not
// remembered, so a later update with the same ID that grows to cover this place still gets through.
func (f *Filter) Decide(a alert.Alert) Decision {
	if a.ID == "" {
		return DropNoID
	}
	areas := f.watched(a.Areas)
	if len(areas) == 0 && !f.location.IsEverywhere() {
		return DropRegion
	}
	// Only earthquake reports carry a measured intensity; for the rest JMA has already decided the alert is worth sending.
	if a.Kind == alert.KindEarthquakeReport && !f.strongEnough(areas) {
		return DropIntensity
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.clock.Now()
	maps.DeleteFunc(f.seen, func(_ string, at time.Time) bool { return now.Sub(at) >= RememberFor })
	if _, ok := f.seen[a.ID]; ok {
		return DropDuplicate
	}
	f.seen[a.ID] = now
	return Send
}

// Remember records an alert that an earlier run of the program sent, so a restart does not send it again. An alert
// older than the memory is ignored, as it would have been forgotten by now.
func (f *Filter) Remember(id string, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.clock.Now().Sub(at) >= RememberFor {
		return
	}
	f.seen[id] = at
}

// Forget undoes the memory of one alert. The caller uses it when a send failed, so the alert is not lost as a
// "duplicate" of something that never arrived.
func (f *Filter) Forget(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.seen, id)
}

// Remembered is how many sent alerts are held for duplicate checks.
func (f *Filter) Remembered() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.seen)
}

func (f *Filter) watched(areas []alert.Area) []alert.Area {
	var out []alert.Area
	for _, area := range areas {
		if f.location.Covers(area.Prefecture) {
			out = append(out, area)
		}
	}
	return out
}

func (f *Filter) strongEnough(areas []alert.Area) bool {
	for _, area := range areas {
		if area.Intensity >= f.min {
			return true
		}
	}
	return false
}
