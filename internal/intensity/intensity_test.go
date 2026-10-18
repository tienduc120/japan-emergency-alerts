package intensity_test

import (
	"errors"
	"testing"

	"github.com/tienduc120/japan-emergency-alerts/internal/intensity"
)

func TestParseReadsEveryLevelOfTheScale(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want intensity.Scale
	}{
		{"0", intensity.Zero},
		{"1", intensity.One},
		{"2", intensity.Two},
		{"3", intensity.Three},
		{"4", intensity.Four},
		{"5-", intensity.FiveLower},
		{"5+", intensity.FiveUpper},
		{"6-", intensity.SixLower},
		{"6+", intensity.SixUpper},
		{"7", intensity.Seven},
		{"5弱", intensity.FiveLower},
		{"5強", intensity.FiveUpper},
		{"6弱", intensity.SixLower},
		{"6強", intensity.SixUpper},
		{" 5- ", intensity.FiveLower},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			got, err := intensity.Parse(tc.in)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("Parse(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseRefusesWhatIsNotAnIntensity(t *testing.T) {
	t.Parallel()

	// A bare 5 or 6 is refused because JMA splits each into a lower and an upper level, and guessing would shift the
	// threshold without telling the user.
	for _, in := range []string{"", "8", "5", "6", "-1", "abc", "5++", "5弱強"} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			_, err := intensity.Parse(in)

			if !errors.Is(err, intensity.ErrInvalid) {
				t.Errorf("Parse(%q) err = %v, want ErrInvalid", in, err)
			}
		})
	}
}

func TestTheScaleIsOrderedSoAThresholdIsAComparison(t *testing.T) {
	t.Parallel()

	ascending := []intensity.Scale{
		intensity.Zero, intensity.One, intensity.Two, intensity.Three, intensity.Four,
		intensity.FiveLower, intensity.FiveUpper, intensity.SixLower, intensity.SixUpper, intensity.Seven,
	}

	for i := 1; i < len(ascending); i++ {
		if ascending[i-1] >= ascending[i] {
			t.Errorf("%v is not below %v: a threshold would let the weaker quake through", ascending[i-1], ascending[i])
		}
	}
}

func TestStringIsJMANotationAndParsesBack(t *testing.T) {
	t.Parallel()

	if got := intensity.FiveLower.String(); got != "5-" {
		t.Errorf("FiveLower.String() = %q, want %q", got, "5-")
	}
	for s := intensity.Zero; s <= intensity.Seven; s++ {
		back, err := intensity.Parse(s.String())
		if err != nil || back != s {
			t.Errorf("Parse(%q) = %v, %v, want %v: String must round-trip", s.String(), back, err, s)
		}
	}
}
