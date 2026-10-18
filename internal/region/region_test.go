package region_test

import (
	"testing"

	"github.com/tienduc120/japan-emergency-alerts/internal/region"
)

func TestThereAreFortySevenPrefecturesWithUniqueCodesAndNames(t *testing.T) {
	t.Parallel()

	prefectures := region.Prefectures()

	if len(prefectures) != 47 {
		t.Fatalf("got %d prefectures, want 47", len(prefectures))
	}
	english := map[string]bool{}
	japanese := map[string]bool{}
	for i, p := range prefectures {
		if p.Code != i+1 {
			t.Errorf("prefecture %d has code %d: codes are the JIS numbers 1 to 47 in order", i+1, p.Code)
		}
		if english[p.English] || japanese[p.Japanese] {
			t.Errorf("%s / %s appears twice", p.English, p.Japanese)
		}
		english[p.English] = true
		japanese[p.Japanese] = true
	}
}

func TestLookupFindsAPrefectureByEitherLanguage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		wantCode int
	}{
		{"Tokyo", 13},
		{"tokyo", 13},
		{" TOKYO ", 13},
		{"東京都", 13},
		{"Hokkaido", 1},
		{"北海道", 1},
		{"Osaka", 27},
		{"沖縄県", 47},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := region.Lookup(tc.name)

			if !ok || got.Code != tc.wantCode {
				t.Errorf("Lookup(%q) = %+v, %v, want code %d", tc.name, got, ok, tc.wantCode)
			}
		})
	}
}

func TestLookupRefusesAnUnknownName(t *testing.T) {
	t.Parallel()

	// A short Japanese form such as 東京 is refused on purpose: one exact spelling per language keeps matching honest.
	for _, name := range []string{"", "Atlantis", "東京", "Tokyo-to"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got, ok := region.Lookup(name); ok {
				t.Errorf("Lookup(%q) = %+v, want no match", name, got)
			}
		})
	}
}

func TestALocationPrintsWhereItIs(t *testing.T) {
	t.Parallel()

	tokyo, ok := region.Lookup("Tokyo")
	if !ok {
		t.Fatal("Tokyo must exist")
	}

	tests := []struct {
		name string
		loc  region.Location
		want string
	}{
		{"everywhere", region.Everywhere(), "all Japan"},
		{"prefecture", region.InPrefecture(tokyo), "Tokyo"},
		{"unset", region.Location{}, "unset"},
	}
	for _, tc := range tests {
		if got := tc.loc.String(); got != tc.want {
			t.Errorf("%s: String() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestTwoLocationsAreEqualWhenTheyMeanTheSamePlace(t *testing.T) {
	t.Parallel()

	tokyo, _ := region.Lookup("Tokyo")
	osaka, _ := region.Lookup("Osaka")

	first, again := region.InPrefecture(tokyo), region.InPrefecture(tokyo)
	if first != again {
		t.Error("the same prefecture must compare equal")
	}
	if region.InPrefecture(tokyo) == region.InPrefecture(osaka) {
		t.Error("different prefectures must not compare equal")
	}
	if region.Everywhere() == (region.Location{}) {
		t.Error("an unset location must not count as all Japan: a forgotten setting would send every alert")
	}
}

func TestCoversSaysWhichPrefecturesAreWatched(t *testing.T) {
	t.Parallel()

	tokyo, _ := region.Lookup("Tokyo")
	kanagawa, _ := region.Lookup("Kanagawa")

	if !region.InPrefecture(tokyo).Covers(tokyo.Code) {
		t.Error("a prefecture must cover itself")
	}
	if region.InPrefecture(tokyo).Covers(kanagawa.Code) {
		t.Error("a prefecture must not cover its neighbour: matching is by prefecture, not by distance")
	}
	for _, p := range region.Prefectures() {
		if !region.Everywhere().Covers(p.Code) {
			t.Errorf("all Japan must cover %s", p.English)
		}
		if (region.Location{}).Covers(p.Code) {
			t.Errorf("an unset location must cover nothing, but covers %s", p.English)
		}
	}
}

func TestOnlyAllJapanIsEverywhere(t *testing.T) {
	t.Parallel()

	tokyo, _ := region.Lookup("Tokyo")

	if !region.Everywhere().IsEverywhere() {
		t.Error("all Japan must be everywhere")
	}
	if region.InPrefecture(tokyo).IsEverywhere() || (region.Location{}).IsEverywhere() {
		t.Error("a prefecture or an unset location must not be everywhere")
	}
}
