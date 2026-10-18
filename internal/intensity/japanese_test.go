package intensity_test

import (
	"testing"

	"github.com/tienduc120/japan-emergency-alerts/internal/intensity"
)

func TestJapaneseIsTheWordingPeopleSayAndRead(t *testing.T) {
	t.Parallel()

	tests := map[intensity.Scale]string{
		intensity.Zero:      "0",
		intensity.One:       "1",
		intensity.Two:       "2",
		intensity.Three:     "3",
		intensity.Four:      "4",
		intensity.FiveLower: "5弱",
		intensity.FiveUpper: "5強",
		intensity.SixLower:  "6弱",
		intensity.SixUpper:  "6強",
		intensity.Seven:     "7",
	}
	for level, want := range tests {
		if got := level.Japanese(); got != want {
			t.Errorf("%v.Japanese() = %q, want %q", level, got, want)
		}
	}
}

func TestJapaneseParsesBack(t *testing.T) {
	t.Parallel()

	for s := intensity.Zero; s <= intensity.Seven; s++ {
		back, err := intensity.Parse(s.Japanese())
		if err != nil || back != s {
			t.Errorf("Parse(%q) = %v, %v, want %v", s.Japanese(), back, err, s)
		}
	}
}
