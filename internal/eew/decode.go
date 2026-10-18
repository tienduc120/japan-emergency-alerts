// Package eew relays earthquake early warnings (緊急地震速報, warnings only) from P2P Quake's live WebSocket stream.
//
// P2P Quake is a community service that receives JMA's warnings from a licensed distributor and republishes them. It
// says plainly that content and delivery are not guaranteed, that nothing is re-sent after an outage, and that it is
// not recommended as an early-warning service. This package is a convenience on top of that, not a replacement for the
// alerts a Japanese phone receives.
package eew

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
	"github.com/tienduc120/japan-emergency-alerts/internal/intensity"
	"github.com/tienduc120/japan-emergency-alerts/internal/region"
)

const (
	// warningCode is the stream's code for an early-warning (警報) message; other codes carry quake reports and the like.
	warningCode = 556
	// overCode in a forecast's upper bound means "or more".
	overCode = 99

	prefectureCount = 47
	jstOffset       = 9 * 60 * 60
	timeLayout      = "2006/01/02 15:04:05"

	// The prefecture numbers that island groups and Hokkaido's four regions are mapped to by name.
	hokkaidoCode  = 1
	tokyoCode     = 13
	kagoshimaCode = 46
	okinawaCode   = 47
)

// Frame is a warning, or the cancellation of one, read from one message of the stream.
type Frame struct {
	EventID   string
	Cancelled bool
	Issued    time.Time
	// Areas are the prefectures the warning covers, once each. A cancellation has none.
	Areas []alert.Area
	// Text is JMA's warning data laid out for a person. A cancellation has none.
	Text string
}

type message struct {
	Code       int  `json:"code"`
	Test       bool `json:"test"`
	Cancelled  bool `json:"cancelled"`
	Earthquake *struct {
		OriginTime string `json:"originTime"`
		Hypocenter struct {
			Name      string   `json:"name"`
			Depth     *float64 `json:"depth"`
			Magnitude *float64 `json:"magnitude"`
		} `json:"hypocenter"`
	} `json:"earthquake"`
	Issue struct {
		Time    string `json:"time"`
		EventID string `json:"eventId"`
	} `json:"issue"`
	Areas []struct {
		Pref      string  `json:"pref"`
		ScaleFrom float64 `json:"scaleFrom"`
		ScaleTo   float64 `json:"scaleTo"`
	} `json:"areas"`
}

// Decode reads one message. The second result is false for anything that is not a real warning: another kind of
// message, or a test. A warning naming a region this program does not know is still returned, for every prefecture,
// with an error to log: when unsure, warn.
func Decode(data []byte) (Frame, bool, error) {
	var m message
	if err := json.Unmarshal(data, &m); err != nil {
		return Frame{}, false, fmt.Errorf("read early-warning message: %w", err)
	}
	if m.Code != warningCode || m.Test {
		return Frame{}, false, nil
	}
	if m.Issue.EventID == "" {
		return Frame{}, false, errors.New("early-warning message has no event id")
	}
	f := Frame{EventID: m.Issue.EventID, Cancelled: m.Cancelled, Issued: parseTime(m.Issue.Time)}
	if m.Cancelled {
		return f, true, nil
	}

	var (
		regions  []regionRange
		unknown  []string
		prefects = map[int]bool{}
	)
	for _, a := range m.Areas {
		regions = addRange(regions, a.Pref, int(a.ScaleFrom), int(a.ScaleTo))
		code, ok := prefectureOf(a.Pref)
		switch {
		case !ok:
			if !slices.Contains(unknown, a.Pref) {
				unknown = append(unknown, a.Pref)
			}
		case !prefects[code]:
			prefects[code] = true
			f.Areas = append(f.Areas, alert.Area{Prefecture: code})
		}
	}
	f.Text = text(m, regions)

	if len(unknown) > 0 {
		f.Areas = allAreas()
		return f, true, fmt.Errorf("early-warning region %s is not a prefecture this program knows; warning everyone",
			strings.Join(unknown, ", "))
	}
	return f, true, nil
}

type regionRange struct {
	name     string
	from, to int
}

// addRange widens the forecast range of a region as more of its areas are seen. A region is one of JMA's forecast
// regions, such as 東京 or 北海道道東, and the numbers are the lowest and highest forecast intensity across its areas.
func addRange(list []regionRange, name string, from, to int) []regionRange {
	for i := range list {
		if list[i].name != name {
			continue
		}
		if from >= 0 && (list[i].from < 0 || from < list[i].from) {
			list[i].from = from
		}
		list[i].to = max(list[i].to, to)
		return list
	}
	return append(list, regionRange{name: name, from: from, to: to})
}

func text(m message, regions []regionRange) string {
	lines := []string{"【緊急地震速報（警報）】"}
	if quake := m.Earthquake; quake != nil {
		if name := strings.TrimSpace(quake.Hypocenter.Name); name != "" {
			lines = append(lines, "震央地名："+name)
		}
		if mag := quake.Hypocenter.Magnitude; mag != nil && *mag >= 0 {
			lines = append(lines, "マグニチュード："+strconv.FormatFloat(*mag, 'f', 1, 64))
		}
		if depth := quake.Hypocenter.Depth; depth != nil && *depth >= 0 {
			lines = append(lines, "深さ："+strconv.FormatFloat(*depth, 'f', 0, 64)+"km")
		}
		if quake.OriginTime != "" {
			lines = append(lines, "地震発生時刻："+quake.OriginTime)
		}
	}
	if len(regions) > 0 {
		lines = append(lines, "予測震度：")
	}
	for _, r := range regions {
		lines = append(lines, r.name+"：震度"+rangeText(r.from, r.to))
	}
	return strings.Join(lines, "\n")
}

// rangeText writes a forecast the way JMA words it: 5弱, 4～5強, 5弱程度以上.
func rangeText(from, to int) string {
	low, high := scaleText(from), scaleText(to)
	switch {
	case to == overCode && low == "":
		return "不明"
	case to == overCode:
		return low + "程度以上"
	case low == "" && high == "":
		return "不明"
	case low == "":
		return high + "以下"
	case high == "":
		return low + "以上"
	case from == to:
		return high
	default:
		return low + "～" + high
	}
}

// scaleText turns the service's intensity code (10 is 震度1, 45 is 震度5弱, and so on) into JMA's wording, or "" when
// the code means unknown.
func scaleText(code int) string {
	scales := map[int]intensity.Scale{
		0:  intensity.Zero,
		10: intensity.One,
		20: intensity.Two,
		30: intensity.Three,
		40: intensity.Four,
		45: intensity.FiveLower,
		50: intensity.FiveUpper,
		55: intensity.SixLower,
		60: intensity.SixUpper,
		70: intensity.Seven,
	}
	if scale, ok := scales[code]; ok {
		return scale.Japanese()
	}
	return ""
}

// prefectureOf maps one of the service's forecast regions to a prefecture. Most regions are the prefecture's name
// without its 都, 道, 府 or 県; Hokkaido is split in four, and a few island groups have names of their own.
func prefectureOf(name string) (int, bool) {
	name = strings.TrimSpace(name)
	switch {
	case strings.HasPrefix(name, "北海道"):
		return hokkaidoCode, true
	case hasAnyPrefix(name, "伊豆", "小笠原"):
		return tokyoCode, true
	case hasAnyPrefix(name, "種子島", "屋久島", "奄美"):
		return kagoshimaCode, true
	case hasAnyPrefix(name, "大東", "宮古", "八重山"):
		return okinawaCode, true
	}
	for _, p := range region.Prefectures() {
		short := p.Japanese
		for _, suffix := range []string{"都", "府", "県"} {
			short = strings.TrimSuffix(short, suffix)
		}
		if name == short || (p.Code == okinawaCode && strings.HasPrefix(name, "沖縄")) {
			return p.Code, true
		}
	}
	return 0, false
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(s, prefix) {
			return true
		}
	}
	return false
}

func allAreas() []alert.Area {
	areas := make([]alert.Area, 0, prefectureCount)
	for code := 1; code <= prefectureCount; code++ {
		areas = append(areas, alert.Area{Prefecture: code})
	}
	return areas
}

// parseTime reads the service's times, which are Japan time written without a zone. An unreadable one gives the zero
// time, which the sender leaves out.
func parseTime(s string) time.Time {
	t, err := time.ParseInLocation(timeLayout, strings.TrimSpace(s), time.FixedZone("JST", jstOffset))
	if err != nil {
		return time.Time{}
	}
	return t
}
