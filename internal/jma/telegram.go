package jma

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
	"github.com/tienduc120/japan-emergency-alerts/internal/intensity"
)

// Telegram type codes, as they appear in file names.
const (
	typeFlash           = "VXSE51" // seismic intensity flash report
	typeQuake           = "VXSE53" // earthquake and seismic intensity report
	typeTsunami         = "VTSE41" // tsunami warning, advisory, forecast
	typeEruptionFlash   = "VFVO56" // eruption flash report
	typeEruptionWarning = "VFVO50" // eruption warning and forecast
	typeWarning         = "VPWW53" // weather warnings and advisories for a prefecture

	// Control/Status values that mark a telegram as not real. Only these are refused, so a change in how JMA writes the
	// normal status can never silence a real alert.
	statusDrill = "訓練"
	statusTest  = "試験"

	specialWarning      = "特別警報"
	residentialEruption = "噴火警報（居住地域）"
	townCodeType        = "市町村"

	prefectureCount  = 47
	prefectureDigits = 2
	// Tsunami kind codes: 51 warning, 52 major warning, 53 major warning in the per-area listing.
	tsunamiWarning, tsunamiMajor, tsunamiMajorListed = "51", "52", "53"
)

// Wanted says whether an entry is worth downloading. It looks at the file name's type code and, for weather warnings,
// at JMA's own summary, so the many telegrams nobody wants cost nothing.
func Wanted(e Entry) bool {
	switch e.Type() {
	case typeFlash, typeQuake, typeTsunami, typeEruptionFlash, typeEruptionWarning:
		return true
	case typeWarning:
		// Ordinary warnings and advisories fill the feed all day. Special warnings name themselves.
		return strings.Contains(e.Content, specialWarning)
	default:
		return false
	}
}

type report struct {
	Control struct {
		Status   string `xml:"Status"`
		DateTime string `xml:"DateTime"`
	} `xml:"Control"`
	Head struct {
		Title          string `xml:"Title"`
		ReportDateTime string `xml:"ReportDateTime"`
		Headline       struct {
			Text        string `xml:"Text"`
			Information []struct {
				Items []item `xml:"Item"`
			} `xml:"Information"`
		} `xml:"Headline"`
	} `xml:"Head"`
	Body struct {
		Earthquake struct {
			Hypocenter struct {
				Area struct {
					Name       string `xml:"Name"`
					Coordinate struct {
						Description string `xml:"description,attr"`
					} `xml:"Coordinate"`
				} `xml:"Area"`
			} `xml:"Hypocenter"`
			Magnitude struct {
				Description string `xml:"description,attr"`
			} `xml:"Magnitude"`
		} `xml:"Earthquake"`
		Intensity struct {
			Observation struct {
				MaxInt string `xml:"MaxInt"`
				Pref   []struct {
					Name   string `xml:"Name"`
					Code   string `xml:"Code"`
					MaxInt string `xml:"MaxInt"`
				} `xml:"Pref"`
			} `xml:"Observation"`
		} `xml:"Intensity"`
		Comments struct {
			ForecastComment struct {
				Text string `xml:"Text"`
			} `xml:"ForecastComment"`
		} `xml:"Comments"`
		Tsunami struct {
			Forecast struct {
				Items []struct {
					Category struct {
						Kind struct {
							Code string `xml:"Code"`
						} `xml:"Kind"`
					} `xml:"Category"`
				} `xml:"Item"`
			} `xml:"Forecast"`
		} `xml:"Tsunami"`
		VolcanoInfo []struct {
			Items []item `xml:"Item"`
		} `xml:"VolcanoInfo"`
		VolcanoInfoContent struct {
			Activity string `xml:"VolcanoActivity"`
		} `xml:"VolcanoInfoContent"`
	} `xml:"Body"`
}

type item struct {
	Kind struct {
		Name string `xml:"Name"`
		Code string `xml:"Code"`
	} `xml:"Kind"`
	Areas struct {
		CodeType string `xml:"codeType,attr"`
		Area     []struct {
			Name string `xml:"Name"`
			Code string `xml:"Code"`
		} `xml:"Area"`
	} `xml:"Areas"`
}

// Decode turns a downloaded telegram into an alert. The second result says whether it should be relayed at all: a
// drill, a tsunami advisory, or a crater-only eruption warning is not, and that is not an error.
//
// An error with relay true means the telegram was unreadable but is of a kind that must not be lost, so the alert is
// built from JMA's feed summary instead and the caller should log the error. Earthquakes are never guessed.
func Decode(e Entry, data []byte) (alert.Alert, bool, error) {
	if !Wanted(e) {
		return alert.Alert{}, false, fmt.Errorf("telegram %q is not a type this program relays", e.Name())
	}
	var r report
	if err := xml.Unmarshal(data, &r); err != nil {
		return unreadable(e, fmt.Errorf("read telegram %s: %w", e.Name(), err))
	}
	if status := strings.TrimSpace(r.Control.Status); status == statusDrill || status == statusTest {
		return alert.Alert{}, false, nil
	}

	a := alert.Alert{ID: e.Name(), Issued: issued(r, e)}
	switch e.Type() {
	case typeFlash, typeQuake:
		return earthquake(a, r)
	case typeTsunami:
		return tsunami(a, r)
	case typeEruptionFlash, typeEruptionWarning:
		return volcano(a, r, e)
	default:
		a.Kind = alert.KindSpecialWarning
		a.Areas = warningAreas(e)
		a.Text = e.Content
		return a, true, nil
	}
}

// unreadable keeps a warning from being lost to a telegram format we cannot read, by relaying JMA's own summary.
func unreadable(e Entry, cause error) (alert.Alert, bool, error) {
	a := alert.Alert{ID: e.Name(), Issued: e.Updated, Text: e.Content}
	switch e.Type() {
	case typeTsunami:
		a.Kind, a.Areas = alert.KindTsunami, allAreas()
	case typeEruptionFlash, typeEruptionWarning:
		a.Kind, a.Areas = alert.KindVolcano, allAreas()
	case typeWarning:
		a.Kind, a.Areas = alert.KindSpecialWarning, warningAreas(e)
	default:
		return alert.Alert{}, false, cause
	}
	return a, true, cause
}

func issued(r report, e Entry) time.Time {
	for _, raw := range []string{r.Head.ReportDateTime, r.Control.DateTime} {
		if t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw)); err == nil {
			return t
		}
	}
	return e.Updated
}

// earthquake keeps JMA's own wording and fields, in the order a person wants them, and reads the per-prefecture
// intensities the filter needs.
func earthquake(a alert.Alert, r report) (alert.Alert, bool, error) {
	a.Kind = alert.KindEarthquakeReport
	lines := []string{headline(r)}
	quake := r.Body.Earthquake
	if name := strings.TrimSpace(quake.Hypocenter.Area.Name); name != "" {
		lines = append(lines, "震央地名："+name)
	}
	for _, field := range []string{quake.Hypocenter.Area.Coordinate.Description, quake.Magnitude.Description} {
		if field = strings.TrimSpace(field); field != "" {
			lines = append(lines, field)
		}
	}

	obs := r.Body.Intensity.Observation
	if highest, err := intensity.Parse(obs.MaxInt); err == nil {
		lines = append(lines, "最大震度："+highest.Japanese())
	}
	for _, pref := range obs.Pref {
		code, ok := prefectureNumber(pref.Code)
		if !ok {
			return alert.Alert{}, false, fmt.Errorf("prefecture code %q is not 1 to %d", pref.Code, prefectureCount)
		}
		level, err := intensity.Parse(pref.MaxInt)
		if err != nil {
			return alert.Alert{}, false, fmt.Errorf("intensity for %s: %w", strings.TrimSpace(pref.Name), err)
		}
		a.Areas = append(a.Areas, alert.Area{Prefecture: code, Intensity: level})
		lines = append(lines, strings.TrimSpace(pref.Name)+"：震度"+level.Japanese())
	}
	if comment := strings.TrimSpace(r.Body.Comments.ForecastComment.Text); comment != "" {
		lines = append(lines, comment)
	}
	a.Text = strings.Join(lines, "\n")
	return a, true, nil
}

// tsunami relays warnings and major warnings, not advisories, as a resident's phone does. Its forecast zones are
// coastal strips, not prefectures, so the alert goes to every location: rare, and a miss costs far more than an extra.
func tsunami(a alert.Alert, r report) (alert.Alert, bool, error) {
	if !hasTsunamiWarning(r) {
		return alert.Alert{}, false, nil
	}
	a.Kind = alert.KindTsunami
	a.Areas = allAreas()
	lines := []string{headline(r)}
	for _, info := range r.Head.Headline.Information {
		for _, it := range info.Items {
			var names []string
			for _, area := range it.Areas.Area {
				if name := strings.TrimSpace(area.Name); name != "" {
					names = append(names, name)
				}
			}
			if len(names) > 0 {
				lines = append(lines, strings.TrimSpace(it.Kind.Name)+"："+strings.Join(names, "、"))
			}
		}
	}
	a.Text = strings.Join(lines, "\n")
	return a, true, nil
}

func hasTsunamiWarning(r report) bool {
	for _, info := range r.Head.Headline.Information {
		for _, it := range info.Items {
			if code := strings.TrimSpace(it.Kind.Code); code == tsunamiWarning || code == tsunamiMajor {
				return true
			}
		}
	}
	for _, it := range r.Body.Tsunami.Forecast.Items {
		switch strings.TrimSpace(it.Category.Kind.Code) {
		case tsunamiWarning, tsunamiMajor, tsunamiMajorListed:
			return true
		}
	}
	return false
}

// volcano relays eruption flash reports and warnings for residential areas. The prefectures come from the affected
// towns' codes, whose first two digits are the prefecture.
func volcano(a alert.Alert, r report, e Entry) (alert.Alert, bool, error) {
	if e.Type() == typeEruptionWarning && !strings.Contains(r.Head.Title, residentialEruption) {
		return alert.Alert{}, false, nil
	}
	a.Kind = alert.KindVolcano
	a.Areas = townPrefectures(r)
	if len(a.Areas) == 0 {
		a.Areas = allAreas()
	}
	lines := []string{headline(r)}
	if activity := strings.TrimSpace(r.Body.VolcanoInfoContent.Activity); activity != "" {
		lines = append(lines, activity)
	}
	a.Text = strings.Join(lines, "\n")
	return a, true, nil
}

func townPrefectures(r report) []alert.Area {
	var areas []alert.Area
	seen := map[int]bool{}
	for _, info := range r.Body.VolcanoInfo {
		for _, it := range info.Items {
			if !strings.Contains(it.Areas.CodeType, townCodeType) {
				continue
			}
			for _, area := range it.Areas.Area {
				if p, ok := prefectureOf(area.Code); ok && !seen[p] {
					seen[p] = true
					areas = append(areas, alert.Area{Prefecture: p})
				}
			}
		}
	}
	return areas
}

// warningAreas reads the prefecture from the file name, whose last part is the forecast area code. When it cannot be
// read the warning goes everywhere.
func warningAreas(e Entry) []alert.Area {
	if p, ok := prefectureOf(e.AreaCode()); ok {
		return []alert.Area{{Prefecture: p}}
	}
	return allAreas()
}

func allAreas() []alert.Area {
	areas := make([]alert.Area, 0, prefectureCount)
	for code := 1; code <= prefectureCount; code++ {
		areas = append(areas, alert.Area{Prefecture: code})
	}
	return areas
}

// headline is JMA's title in brackets followed by JMA's headline, the same shape as the feed's own summaries.
func headline(r report) string {
	title := strings.TrimSpace(r.Head.Title)
	text := strings.TrimSpace(r.Head.Headline.Text)
	if title == "" {
		return text
	}
	return "【" + title + "】" + text
}

// prefectureNumber reads a whole code such as "13".
func prefectureNumber(s string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	return n, err == nil && n >= 1 && n <= prefectureCount
}

// prefectureOf reads the first two digits of a longer code, as in a town code or a forecast area code.
func prefectureOf(code string) (int, bool) {
	code = strings.TrimSpace(code)
	if len(code) < prefectureDigits {
		return 0, false
	}
	return prefectureNumber(code[:prefectureDigits])
}
