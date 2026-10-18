// Package intensity is the JMA seismic intensity scale (shindo). The levels are ordered, so a threshold is a comparison.
package intensity

import (
	"errors"
	"fmt"
	"strings"
)

// Scale is one level of the JMA scale. JMA splits 5 and 6 into a lower and an upper level.
type Scale int

const (
	Zero Scale = iota
	One
	Two
	Three
	Four
	FiveLower
	FiveUpper
	SixLower
	SixUpper
	Seven
)

var ErrInvalid = errors.New("not a JMA seismic intensity")

// Parse reads the notation JMA uses in its data (5-, 5+) and the Japanese one (5弱, 5強).
func Parse(s string) (Scale, error) {
	trimmed := strings.TrimSpace(s)
	for level := Zero; level <= Seven; level++ {
		if trimmed == level.String() || trimmed == level.Japanese() {
			return level, nil
		}
	}
	return Zero, fmt.Errorf("%w: %q (use 0 to 4, 5-, 5+, 6-, 6+ or 7)", ErrInvalid, s)
}

// Japanese is how the level is said and written in Japanese, such as 5弱 for the lower 5.
func (s Scale) Japanese() string {
	switch s {
	case FiveLower:
		return "5弱"
	case FiveUpper:
		return "5強"
	case SixLower:
		return "6弱"
	case SixUpper:
		return "6強"
	case Zero, One, Two, Three, Four, Seven:
		return s.String()
	default:
		return s.String()
	}
}

func (s Scale) String() string {
	names := [...]string{"0", "1", "2", "3", "4", "5-", "5+", "6-", "6+", "7"}
	if s < Zero || int(s) >= len(names) {
		return "unknown"
	}
	return names[s]
}
