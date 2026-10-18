package region_test

import (
	"testing"

	"github.com/tienduc120/japan-emergency-alerts/internal/region"
)

func mustLookup(t *testing.T, name string) region.Prefecture {
	t.Helper()
	p, ok := region.Lookup(name)
	if !ok {
		t.Fatalf("no prefecture named %q", name)
	}
	return p
}

func TestLookupFindsAPrefectureByItsNumber(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"13": "Tokyo", "1": "Hokkaido", "01": "Hokkaido", "27": "Osaka",
		" 47 ": "Okinawa", //nolint:gocritic // the spaces are the point: input is trimmed
	}
	for in, want := range tests {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			got, ok := region.Lookup(in)

			if !ok || got.English != want {
				t.Errorf("Lookup(%q) = %+v, %v, want %s", in, got, ok, want)
			}
		})
	}
}

func TestLookupRefusesANumberThatIsNotAPrefecture(t *testing.T) {
	t.Parallel()

	// "+13" and "1e1" are refused although a number parser would take them: one plain spelling per prefecture.
	for _, in := range []string{"0", "00", "48", "99", "-1", "+13", "1e1", "013", "13.0"} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()

			if got, ok := region.Lookup(in); ok {
				t.Errorf("Lookup(%q) = %+v, want no match", in, got)
			}
		})
	}
}

func TestSeveralPrefecturesAreWatchedTogether(t *testing.T) {
	t.Parallel()
	tokyo := mustLookup(t, "Tokyo")
	kanagawa := mustLookup(t, "Kanagawa")
	osaka := mustLookup(t, "Osaka")

	loc := region.InPrefectures(tokyo, kanagawa)

	if !loc.Covers(tokyo.Code) || !loc.Covers(kanagawa.Code) {
		t.Error("every listed prefecture must be covered")
	}
	if loc.Covers(osaka.Code) {
		t.Error("an unlisted prefecture must not be covered")
	}
	if loc.IsEverywhere() {
		t.Error("a list of prefectures is not all Japan")
	}
}

func TestTheOrderAndRepeatsOfAListDoNotMatter(t *testing.T) {
	t.Parallel()
	tokyo := mustLookup(t, "Tokyo")
	kanagawa := mustLookup(t, "Kanagawa")

	a := region.InPrefectures(tokyo, kanagawa, tokyo)
	b := region.InPrefectures(kanagawa, tokyo)

	if a != b {
		t.Error("the same prefectures must compare equal however they were listed")
	}
	if region.InPrefecture(tokyo) != region.InPrefectures(tokyo) {
		t.Error("one prefecture on its own must equal a list of one")
	}
}

func TestAListPrintsInCodeOrder(t *testing.T) {
	t.Parallel()
	tokyo := mustLookup(t, "Tokyo")
	kanagawa := mustLookup(t, "Kanagawa")

	got := region.InPrefectures(kanagawa, tokyo).String()

	if got != "Tokyo, Kanagawa" {
		t.Errorf("String() = %q, want %q", got, "Tokyo, Kanagawa")
	}
}

func TestNoPrefecturesIsAnUnsetLocationNotAllJapan(t *testing.T) {
	t.Parallel()

	loc := region.InPrefectures()

	if loc != (region.Location{}) {
		t.Errorf("InPrefectures() = %v, want the unset location: an empty list must not watch everything", loc)
	}
}

func TestAPrefectureOutsideTheTableCoversNothing(t *testing.T) {
	t.Parallel()
	loc := region.InPrefectures(mustLookup(t, "Tokyo"))

	for _, code := range []int{0, -1, 48, 64, 1 << 20} {
		if loc.Covers(code) {
			t.Errorf("Covers(%d) = true, want false: there is no such prefecture", code)
		}
	}
}
