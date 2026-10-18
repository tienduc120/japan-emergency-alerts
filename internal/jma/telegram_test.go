package jma_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
	"github.com/tienduc120/japan-emergency-alerts/internal/intensity"
	"github.com/tienduc120/japan-emergency-alerts/internal/jma"
)

const dataBase = "https://www.data.jma.go.jp/developer/xml/data/"

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

func entry(file, content string) jma.Entry {
	updated := time.Date(2026, time.October, 18, 7, 40, 0, 0, time.UTC)
	return jma.Entry{ID: dataBase + file, Content: content, Updated: updated}
}

func everyPrefecture() []alert.Area {
	var areas []alert.Area
	for code := 1; code <= 47; code++ {
		areas = append(areas, alert.Area{Prefecture: code})
	}
	return areas
}

const (
	kumamotoFile = "20261018074002_0_VXSE53_270000.xml"
	strongFile   = "20261018050230_0_VXSE53_130000.xml"
	flashFile    = "20261018050110_0_VXSE51_130000.xml"
	tsunamiFile  = "20261018054959_0_VTSE41_100000.xml"
	ontakeFile   = "20261018030012_0_VFVO56_200000.xml"
	hakoneFile   = "20261018042636_0_VFVO50_140000.xml"
	warningFile  = "20261018203000_0_VPWW53_290000.xml"

	specialSummary = "【奈良県気象特別警報・警報・注意報】【特別警報（大雨）】奈良県では、４日昼過ぎまで土砂災害に警戒して下さい。"
)

func TestAQuakeReportCarriesEachPrefecturesIntensityAndJMAsFields(t *testing.T) {
	t.Parallel()
	e := entry(kumamotoFile, "【震源・震度情報】１８日１６時３７分ころ、地震がありました。")

	got, ok, err := jma.Decode(e, testdata(t, "VXSE53_kumamoto.xml"))

	if err != nil || !ok {
		t.Fatalf("ok, err = %v, %v, want a relayable alert", ok, err)
	}
	wantText := "【震源・震度情報】１８日１６時３７分ころ、地震がありました。\n" +
		"震央地名：熊本県天草・芦北地方\n" +
		"北緯３２．６度　東経１３０．６度　深さ　１０ｋｍ\n" +
		"Ｍ３．０\n" +
		"最大震度：1\n" +
		"長崎県：震度1\n" +
		"熊本県：震度1\n" +
		"この地震による津波の心配はありません。"
	if got.Text != wantText {
		t.Errorf("Text =\n%s\nwant\n%s", got.Text, wantText)
	}
	if got.ID != "20261018074002_0_VXSE53_270000" || got.Kind != alert.KindEarthquakeReport {
		t.Errorf("ID %q, Kind %v, want the telegram name and an earthquake report", got.ID, got.Kind)
	}
	wantAreas := []alert.Area{{Prefecture: 42, Intensity: intensity.One}, {Prefecture: 43, Intensity: intensity.One}}
	if !slices.Equal(got.Areas, wantAreas) {
		t.Errorf("Areas = %v, want %v", got.Areas, wantAreas)
	}
	wantIssued := time.Date(2026, time.October, 18, 7, 40, 0, 0, time.UTC)
	if !got.Issued.Equal(wantIssued) {
		t.Errorf("Issued = %v, want %v", got.Issued, wantIssued)
	}
}

func TestAStrongReportKeepsTheOrderAndToleratesStrayWhitespace(t *testing.T) {
	t.Parallel()
	e := entry(strongFile, "")

	got, ok, err := jma.Decode(e, testdata(t, "VXSE53_strong.xml"))

	if err != nil || !ok {
		t.Fatalf("ok, err = %v, %v, want a relayable alert", ok, err)
	}
	wantAreas := []alert.Area{
		{Prefecture: 13, Intensity: intensity.FiveLower},
		{Prefecture: 14, Intensity: intensity.Four},
		{Prefecture: 12, Intensity: intensity.Three},
	}
	if !slices.Equal(got.Areas, wantAreas) {
		t.Errorf("Areas = %v, want %v: the telegram says \"4 \" with a space for Kanagawa", got.Areas, wantAreas)
	}
	wantText := "【震源・震度情報】１８日１３時５９分ころ、地震がありました。\n" +
		"震央地名：東京湾\n" +
		"北緯３５．５度　東経１３９．８度　深さ　４０ｋｍ\n" +
		"Ｍ５．８\n" +
		"最大震度：5弱\n" +
		"東京都：震度5弱\n" +
		"神奈川県：震度4\n" +
		"千葉県：震度3\n" +
		"この地震による津波の心配はありません。"
	if got.Text != wantText {
		t.Errorf("Text =\n%s\nwant\n%s", got.Text, wantText)
	}
}

func TestAFlashReportHasIntensitiesButNoEpicentre(t *testing.T) {
	t.Parallel()
	e := entry(flashFile, "")

	got, ok, err := jma.Decode(e, testdata(t, "VXSE51_flash.xml"))

	if err != nil || !ok {
		t.Fatalf("ok, err = %v, %v, want a relayable alert", ok, err)
	}
	wantText := "【震度速報】＜震度速報＞　１８日１３時５９分ころ、強い揺れを感じた地域があります。\n" +
		"最大震度：5強\n" +
		"静岡県：震度5強\n" +
		"東京都：震度4"
	if got.Text != wantText {
		t.Errorf("Text =\n%s\nwant\n%s", got.Text, wantText)
	}
	wantAreas := []alert.Area{
		{Prefecture: 22, Intensity: intensity.FiveUpper},
		{Prefecture: 13, Intensity: intensity.Four},
	}
	if !slices.Equal(got.Areas, wantAreas) {
		t.Errorf("Areas = %v, want %v", got.Areas, wantAreas)
	}
}

func TestAMajorTsunamiWarningGoesToEveryPrefecture(t *testing.T) {
	t.Parallel()
	e := entry(tsunamiFile, "")

	got, ok, err := jma.Decode(e, testdata(t, "VTSE41_major.xml"))

	if err != nil || !ok {
		t.Fatalf("ok, err = %v, %v, want a relayable alert", ok, err)
	}
	if got.Kind != alert.KindTsunami || !slices.Equal(got.Areas, everyPrefecture()) {
		t.Errorf(
			"Kind %v, %d areas: a tsunami warning must reach every location, because the forecast zones are not prefectures",
			got.Kind,
			len(got.Areas),
		)
	}
	wantText := "【大津波警報・津波警報・津波注意報・津波予報】東日本大震災クラスの津波が来襲します。\n" +
		"大津波警報・津波警報を発表しました。\n" +
		"ただちに避難してください。\n" +
		"大津波警報：東北地方太平洋沿岸\n" +
		"津波警報：北海道太平洋沿岸中部、茨城県"
	if got.Text != wantText {
		t.Errorf("Text =\n%s\nwant\n%s", got.Text, wantText)
	}
}

func TestATsunamiAdvisoryAloneIsNotRelayed(t *testing.T) {
	t.Parallel()
	e := entry("20261018031000_0_VTSE41_100000.xml", "")

	_, ok, err := jma.Decode(e, testdata(t, "VTSE41_advisory.xml"))

	if err != nil || ok {
		t.Errorf(
			"ok, err = %v, %v, want not relayed and no error: a resident's phone does not alert for an advisory",
			ok,
			err,
		)
	}
}

func TestAVolcanoFlashGoesToThePrefecturesOfTheAffectedTowns(t *testing.T) {
	t.Parallel()
	e := entry(ontakeFile, "")

	got, ok, err := jma.Decode(e, testdata(t, "VFVO56_ontake.xml"))

	if err != nil || !ok {
		t.Fatalf("ok, err = %v, %v, want a relayable alert", ok, err)
	}
	wantAreas := []alert.Area{{Prefecture: 20}, {Prefecture: 21}}
	if got.Kind != alert.KindVolcano || !slices.Equal(got.Areas, wantAreas) {
		t.Errorf("Kind %v, Areas %v, want a volcano alert for Nagano and Gifu once each", got.Kind, got.Areas)
	}
	wantText := "【火山名　御嶽山　噴火速報】＜御嶽山で噴火が発生＞\n御嶽山で、平成２６年９月２７日１１時５３分頃、噴火が発生しました。"
	if got.Text != wantText {
		t.Errorf("Text =\n%s\nwant\n%s", got.Text, wantText)
	}
}

func TestAnEruptionWarningForResidentialAreasIsRelayedButACraterOneIsNot(t *testing.T) {
	t.Parallel()

	got, ok, err := jma.Decode(entry(hakoneFile, ""), testdata(t, "VFVO50_hakone.xml"))
	if err != nil || !ok {
		t.Fatalf("residential: ok, err = %v, %v, want a relayable alert", ok, err)
	}
	if !slices.Equal(got.Areas, []alert.Area{{Prefecture: 14}}) {
		t.Errorf("Areas = %v, want Kanagawa only", got.Areas)
	}
	for _, want := range []string{"【火山名  箱根山  噴火警報（居住地域）】", "噴火警戒レベル５、避難", "噴火が発生しました"} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("Text lacks %q:\n%s", want, got.Text)
		}
	}

	_, ok, err = jma.Decode(entry("20200522040000_0_VFVO50_100000.xml", ""), testdata(t, "VFVO50_crater.xml"))
	if err != nil || ok {
		t.Errorf(
			"crater: ok, err = %v, %v, want not relayed and no error: only residential-area warnings reach phones",
			ok,
			err,
		)
	}
}

func TestASpecialWarningIsRelayedForItsPrefectureWithJMAsOwnSummary(t *testing.T) {
	t.Parallel()

	got, ok, err := jma.Decode(entry(warningFile, specialSummary), testdata(t, "VPWW53_special.xml"))

	if err != nil || !ok {
		t.Fatalf("ok, err = %v, %v, want a relayable alert", ok, err)
	}
	if got.Kind != alert.KindSpecialWarning || !slices.Equal(got.Areas, []alert.Area{{Prefecture: 29}}) {
		t.Errorf(
			"Kind %v, Areas %v, want a special warning for Nara (29), read from the file name",
			got.Kind,
			got.Areas,
		)
	}
	if got.Text != specialSummary {
		t.Errorf("Text = %q, want JMA's own summary unchanged", got.Text)
	}
}

func TestASpecialWarningWithAnUnreadableAreaCodeGoesToEveryPrefecture(t *testing.T) {
	t.Parallel()

	got, ok, err := jma.Decode(
		entry("20261018203000_0_VPWW53_000000.xml", specialSummary),
		testdata(t, "VPWW53_special.xml"),
	)

	if err != nil || !ok || !slices.Equal(got.Areas, everyPrefecture()) {
		t.Errorf(
			"ok %v, err %v, %d areas, want relayed to every prefecture: when unsure, warn",
			ok,
			err,
			len(got.Areas),
		)
	}
}

func TestADrillOrTestTelegramIsNeverRelayed(t *testing.T) {
	t.Parallel()

	cases := []struct{ file, name, content string }{
		{"VXSE53_kumamoto.xml", kumamotoFile, ""},
		{"VXSE51_flash.xml", flashFile, ""},
		{"VTSE41_major.xml", tsunamiFile, ""},
		{"VFVO56_ontake.xml", ontakeFile, ""},
		{"VFVO50_hakone.xml", hakoneFile, ""},
		{"VPWW53_special.xml", warningFile, specialSummary},
	}
	for _, tc := range cases {
		for _, status := range []string{"訓練", "試験"} {
			t.Run(tc.file+"/"+status, func(t *testing.T) {
				t.Parallel()
				data := strings.Replace(
					string(testdata(t, tc.file)),
					"<Status>通常</Status>",
					"<Status>"+status+"</Status>",
					1,
				)

				_, ok, err := jma.Decode(entry(tc.name, tc.content), []byte(data))

				if err != nil || ok {
					t.Errorf(
						"ok, err = %v, %v, want not relayed and no error: a drill must never look like a real alert",
						ok,
						err,
					)
				}
			})
		}
	}
}

func TestAnUnreadableTelegramFailsOpenForEveryKindExceptEarthquakes(t *testing.T) {
	t.Parallel()
	garbage := []byte("<Report><Control>")

	for _, name := range []string{tsunamiFile, ontakeFile, hakoneFile} {
		got, ok, err := jma.Decode(entry(name, "JMAの要約"), garbage)
		if err == nil || !ok {
			t.Errorf("%s: ok, err = %v, %v, want relayed with an error to log", name, ok, err)
		}
		if got.Text != "JMAの要約" || !slices.Equal(got.Areas, everyPrefecture()) {
			t.Errorf(
				"%s: Text %q, %d areas, want the feed summary sent to every prefecture",
				name,
				got.Text,
				len(got.Areas),
			)
		}
	}

	got, ok, err := jma.Decode(entry(warningFile, specialSummary), garbage)
	if err == nil || !ok || got.Text != specialSummary || !slices.Equal(got.Areas, []alert.Area{{Prefecture: 29}}) {
		t.Errorf("special warning: %+v, %v, %v, want the summary for Nara and an error to log", got, ok, err)
	}

	if _, ok, err = jma.Decode(entry(kumamotoFile, ""), garbage); err == nil || ok {
		t.Errorf(
			"earthquake: ok, err = %v, %v, want not relayed with an error: guessing an intensity would be worse",
			ok,
			err,
		)
	}
}

func TestAnUnreadableIntensityIsAnErrorNotAGuess(t *testing.T) {
	t.Parallel()
	data := strings.ReplaceAll(string(testdata(t, "VXSE53_kumamoto.xml")), "<MaxInt>1</MaxInt>", "<MaxInt>x</MaxInt>")

	_, ok, err := jma.Decode(entry(kumamotoFile, ""), []byte(data))

	if err == nil || ok {
		t.Errorf("ok, err = %v, %v, want not relayed with an error", ok, err)
	}
}

func TestATelegramTypeWeDoNotRelayIsAnError(t *testing.T) {
	t.Parallel()

	_, ok, err := jma.Decode(entry("20261018110033_0_VFVO53_010000.xml", ""), []byte("<Report/>"))

	if err == nil || ok {
		t.Errorf(
			"ok, err = %v, %v, want not relayed with an error: Decode is only for telegrams Wanted accepts",
			ok,
			err,
		)
	}
}

func TestWantedPicksTheTelegramsWorthDownloading(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{"20261018074002_0_VXSE53_270000.xml", "", true},
		{"20261018050110_0_VXSE51_130000.xml", "", true},
		{"20261018054959_0_VTSE41_100000.xml", "", true},
		{"20261018030012_0_VFVO56_200000.xml", "", true},
		{"20261018042636_0_VFVO50_140000.xml", "", true},
		{"20261018203000_0_VPWW53_290000.xml", specialSummary, true},
		{"20261018203000_0_VPWW53_290000.xml", "【奈良県気象警報・注意報】注意報を解除します。", false},
		{"20261018110033_0_VFVO53_010000.xml", "", false},
		{"20261018081200_0_VFVO52_400000.xml", "", false},
		{"20261018074002_0_VXSE52_270000.xml", "", false},
		{"not-a-telegram.xml", "", false},
	}
	for _, tc := range tests {
		if got := jma.Wanted(entry(tc.name, tc.content)); got != tc.want {
			t.Errorf("Wanted(%s, %q) = %v, want %v", tc.name, tc.content, got, tc.want)
		}
	}
}
