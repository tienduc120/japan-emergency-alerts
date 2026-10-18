package jma_test

import (
	"strings"
	"testing"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/jma"
)

func TestParseFeedReadsEveryEntry(t *testing.T) {
	t.Parallel()

	entries, err := jma.ParseFeed(strings.NewReader(string(testdata(t, "feed_sample.xml"))))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	last := entries[2]
	if last.ID != dataBase+"20261018074002_0_VXSE53_270000.xml" {
		t.Errorf("ID = %q", last.ID)
	}
	if last.Title != "震源・震度に関する情報" {
		t.Errorf("Title = %q", last.Title)
	}
	if want := "【震源・震度情報】１８日１６時３７分ころ、地震がありました。"; last.Content != want {
		t.Errorf("Content = %q, want %q", last.Content, want)
	}
	if want := time.Date(2026, time.October, 18, 7, 40, 2, 0, time.UTC); !last.Updated.Equal(want) {
		t.Errorf("Updated = %v, want %v", last.Updated, want)
	}
	if !strings.Contains(entries[1].Content, "\n") {
		t.Errorf("Content = %q, want the line breaks of the original kept", entries[1].Content)
	}
}

func TestParseFeedRefusesWhatIsNotAFeed(t *testing.T) {
	t.Parallel()

	if _, err := jma.ParseFeed(strings.NewReader("this is not xml")); err == nil {
		t.Error("want an error for text that is not XML")
	}
}

func TestAnEntryNamesItsTelegram(t *testing.T) {
	t.Parallel()
	e := jma.Entry{ID: dataBase + "20261018074002_0_VXSE53_270000.xml"}

	if e.Name() != "20261018074002_0_VXSE53_270000" || e.Type() != "VXSE53" || e.AreaCode() != "270000" {
		t.Errorf("Name %q, Type %q, AreaCode %q", e.Name(), e.Type(), e.AreaCode())
	}
}

func TestAnEntryWithAnOddIDHasNoType(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"", "not a url", dataBase + "weird.xml", dataBase + "a_b_c.xml", dataBase + "a_b_c_d_e.xml"} {
		if got := (jma.Entry{ID: id}).Type(); got != "" {
			t.Errorf("Type() for %q = %q, want empty", id, got)
		}
	}
}
