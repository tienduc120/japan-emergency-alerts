package eew_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
	"github.com/tienduc120/japan-emergency-alerts/internal/eew"
	"github.com/tienduc120/japan-emergency-alerts/internal/region"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func testdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func everyPrefecture() []alert.Area {
	var areas []alert.Area
	for code := 1; code <= 47; code++ {
		areas = append(areas, alert.Area{Prefecture: code})
	}
	return areas
}

// oneRegion is a warning whose only warned region is the one given.
func oneRegion(pref string) []byte {
	return fmt.Appendf(
		nil,
		`{"code":556,"cancelled":false,"issue":{"eventId":"E1","serial":"1","time":"2026/10/18 14:00:24"},
		"earthquake":{"originTime":"2026/10/18 14:00:03","arrivalTime":"2026/10/18 14:00:10",
		"hypocenter":{"name":"どこか","magnitude":6.0,"depth":10,"latitude":35,"longitude":139}},
		"areas":[{"pref":%q,"name":"どこかの地域","scaleFrom":45,"scaleTo":50,"kindCode":"10"}]}`,
		pref,
	)
}

func TestAWarningBecomesAFrameWithPrefecturesAndJMAsFields(t *testing.T) {
	t.Parallel()

	got, ok, err := eew.Decode(testdata(t, "warning_chiba.json"))

	if err != nil || !ok {
		t.Fatalf("ok, err = %v, %v, want a warning", ok, err)
	}
	wantText := "【緊急地震速報（警報）】\n" +
		"震央地名：千葉県東方沖\n" +
		"マグニチュード：6.4\n" +
		"深さ：40km\n" +
		"地震発生時刻：2026/10/18 14:00:03\n" +
		"予測震度：\n" +
		"千葉：震度5強～6弱\n" +
		"東京：震度4～5強\n" +
		"神奈川：震度4\n" +
		"伊豆諸島：震度5弱程度以上"
	if got.Text != wantText {
		t.Errorf("Text =\n%s\nwant\n%s", got.Text, wantText)
	}
	wantAreas := []alert.Area{{Prefecture: 12}, {Prefecture: 13}, {Prefecture: 14}}
	if !slices.Equal(got.Areas, wantAreas) {
		t.Errorf("Areas = %v, want %v: the Izu Islands belong to Tokyo and must not repeat it", got.Areas, wantAreas)
	}
	if got.EventID != "20261018140005" || got.Cancelled {
		t.Errorf("EventID %q, Cancelled %v", got.EventID, got.Cancelled)
	}
	wantIssued := time.Date(2026, time.October, 18, 14, 0, 24, 0, time.FixedZone("JST", 9*60*60))
	if !got.Issued.Equal(wantIssued) {
		t.Errorf("Issued = %v, want %v: the service writes Japan time without a zone", got.Issued, wantIssued)
	}
}

func TestACancellationNamesTheEventAndWarnsNoRegion(t *testing.T) {
	t.Parallel()

	got, ok, err := eew.Decode(testdata(t, "cancel.json"))

	if err != nil || !ok {
		t.Fatalf("ok, err = %v, %v, want a cancellation", ok, err)
	}
	if !got.Cancelled || got.EventID != "20261018140005" || len(got.Areas) != 0 || got.Text != "" {
		t.Errorf("got %+v, want a cancellation of event 20261018140005 with no areas and no text", got)
	}
}

func TestEveryPrefectureNameInTheServiceMapsToItsNumber(t *testing.T) {
	t.Parallel()

	// The service names a region by the prefecture without its 都, 道, 府 or 県 suffix, except Hokkaido, which it splits.
	for _, p := range region.Prefectures() {
		short := p.Japanese
		for _, suffix := range []string{"都", "府", "県"} {
			short = strings.TrimSuffix(short, suffix)
		}
		if p.Code == 1 {
			short = "北海道道央"
		}
		got, ok, err := eew.Decode(oneRegion(short))
		if err != nil || !ok || !slices.Equal(got.Areas, []alert.Area{{Prefecture: p.Code}}) {
			t.Errorf(
				"region %q: ok %v, err %v, areas %v, want prefecture %d (%s)",
				short,
				ok,
				err,
				got.Areas,
				p.Code,
				p.English,
			)
		}
	}
}

func TestIslandRegionsMapToTheirPrefecture(t *testing.T) {
	t.Parallel()

	tests := map[string]int{
		"北海道道北": 1, "北海道道東": 1, "北海道道南": 1,
		"伊豆諸島": 13, "小笠原諸島": 13,
		"種子島・屋久島": 46, "奄美群島": 46,
		"沖縄本島": 47, "大東島": 47, "宮古島": 47, "八重山": 47,
	}
	for name, want := range tests {
		got, ok, err := eew.Decode(oneRegion(name))
		if err != nil || !ok || !slices.Equal(got.Areas, []alert.Area{{Prefecture: want}}) {
			t.Errorf("region %q: ok %v, err %v, areas %v, want prefecture %d", name, ok, err, got.Areas, want)
		}
	}
}

func TestARegionWeDoNotKnowWarnsEveryoneAndSaysSo(t *testing.T) {
	t.Parallel()

	got, ok, err := eew.Decode(oneRegion("未知の地域"))

	if !ok || err == nil || !slices.Equal(got.Areas, everyPrefecture()) {
		t.Errorf(
			"ok %v, err %v, %d areas, want a warning for every prefecture and an error to log",
			ok,
			err,
			len(got.Areas),
		)
	}
	if err != nil && !strings.Contains(err.Error(), "未知の地域") {
		t.Errorf("err = %v, want it to name the region so it can be added", err)
	}
}

func TestTestMessagesAndOtherCodesAreNotWarnings(t *testing.T) {
	t.Parallel()
	asTest := strings.Replace(
		string(testdata(t, "warning_chiba.json")),
		`"cancelled": false,`,
		`"cancelled": false, "test": true,`,
		1,
	)

	cases := map[string]string{
		"a test warning":  asTest,
		"a quake report":  `{"code":551,"id":"x"}`,
		"peer count":      `{"code":555,"areas":[{"id":10,"peer":3}]}`,
		"detection flash": `{"code":554,"type":"Full"}`,
	}
	for name, frame := range cases {
		got, ok, err := eew.Decode([]byte(frame))
		if ok || err != nil {
			t.Errorf("%s: ok %v, err %v, got %+v, want ignored without an error", name, ok, err, got)
		}
	}
}

func TestAFrameThatIsNotJSONIsAnError(t *testing.T) {
	t.Parallel()

	if _, ok, err := eew.Decode([]byte("not json")); ok || err == nil {
		t.Errorf("ok %v, err %v, want an error", ok, err)
	}
}

func TestForecastRangesReadTheWayJMAWritesThem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		from, to int
		want     string
	}{
		{45, 45, "震度5弱"},
		{40, 50, "震度4～5強"},
		{45, 99, "震度5弱程度以上"},
		{-1, 50, "震度5強以下"},
		{-1, -1, "震度不明"},
		{0, 30, "震度0～3"},
	}
	for _, tc := range tests {
		frame := fmt.Appendf(
			nil,
			`{"code":556,"cancelled":false,"issue":{"eventId":"E1","serial":"1","time":"2026/10/18 14:00:24"},
			"earthquake":{"originTime":"2026/10/18 14:00:03","arrivalTime":"","hypocenter":{"name":"x","magnitude":-1,"depth":-1}},
			"areas":[{"pref":"東京","name":"a","scaleFrom":%d,"scaleTo":%d,"kindCode":"10"}]}`,
			tc.from,
			tc.to,
		)

		got, ok, err := eew.Decode(frame)

		if err != nil || !ok || !strings.Contains(got.Text, "東京："+tc.want) {
			t.Errorf(
				"from %d to %d: ok %v, err %v, want the line 東京：%s in\n%s",
				tc.from,
				tc.to,
				ok,
				err,
				tc.want,
				got.Text,
			)
		}
	}
}

func TestAnUnknownMagnitudeAndDepthAreLeftOut(t *testing.T) {
	t.Parallel()
	frame := []byte(`{"code":556,"cancelled":false,"issue":{"eventId":"E1","serial":"1","time":"2026/10/18 14:00:24"},
		"earthquake":{"originTime":"2026/10/18 14:00:03","arrivalTime":"","hypocenter":{"name":"x","magnitude":-1,"depth":-1}},
		"areas":[{"pref":"東京","name":"a","scaleFrom":45,"scaleTo":45,"kindCode":"10"}]}`)

	got, _, err := eew.Decode(frame)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Text, "マグニチュード") || strings.Contains(got.Text, "深さ") {
		t.Errorf("Text shows a magnitude or depth the service does not have:\n%s", got.Text)
	}
}
