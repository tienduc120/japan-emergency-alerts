// Package alert is the one shape every source turns its messages into, so the filter and the sender never care where
// an alert came from.
package alert

import (
	"context"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/intensity"
)

// Kind is the type of alert, in the terms of what a resident's phone would show.
type Kind int

const (
	KindEarlyWarning     Kind = iota + 1 // earthquake early warning, before the shaking
	KindEarthquakeReport                 // epicentre and intensity, after the shaking
	KindTsunami                          // tsunami warning or major tsunami warning
	KindSpecialWarning                   // heavy rain, storm and other special warnings
	KindVolcano                          // eruption warning or flash report
)

// String is the stable name machines see in structured output.
func (k Kind) String() string {
	switch k {
	case KindEarlyWarning:
		return "early_warning"
	case KindEarthquakeReport:
		return "earthquake_report"
	case KindTsunami:
		return "tsunami"
	case KindSpecialWarning:
		return "special_warning"
	case KindVolcano:
		return "volcano"
	default:
		return "unknown"
	}
}

// Label is the short English heading a person reads above JMA's own text.
func (k Kind) Label() string {
	switch k {
	case KindEarlyWarning:
		return "Earthquake early warning"
	case KindEarthquakeReport:
		return "Earthquake report"
	case KindTsunami:
		return "Tsunami warning"
	case KindSpecialWarning:
		return "Special warning"
	case KindVolcano:
		return "Volcano warning"
	default:
		return "Alert"
	}
}

// Handler takes one alert from a source. Returning an error says it could not be delivered, and a source may offer it
// again.
type Handler func(ctx context.Context, a Alert) error

// Area is one prefecture an alert applies to.
type Area struct {
	Prefecture int // JIS code, as in region.Prefecture
	// Intensity is the shaking reported in this prefecture. It is set for earthquake reports and ignored otherwise.
	Intensity intensity.Scale
}

// Alert is one message from a source, with JMA's text kept exactly as received.
type Alert struct {
	// ID tells repeats apart from new alerts, and the source decides which is which: an ID that includes an update
	// number sends each update, and one that does not sends the alert once.
	ID     string
	Kind   Kind
	Issued time.Time
	Areas  []Area
	// Text is relayed unchanged: reword or add to it and it is no longer JMA's message.
	Text string
	// Late marks an alert that reached us long after JMA issued it, such as one found after a restart or a sleep, so it
	// is shown as late and never passes for a live one.
	Late bool
}
