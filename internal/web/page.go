package web

import (
	_ "embed"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/tienduc120/japan-emergency-alerts/internal/config"
	"github.com/tienduc120/japan-emergency-alerts/internal/region"
)

//go:embed page.html
var pageSource string

// faviconData is the page's icon, an .ico file with sizes from 16 to 256 pixels. It is also the program's icon on
// Windows, through cmd/alerts/rsrc_windows_amd64.syso.
//
//go:embed favicon.ico
var faviconData []byte

const (
	defaultLocation  = "Tokyo"
	defaultIntensity = "5-"
)

type prefectureView struct {
	Code              int
	English, Japanese string
	Checked           bool
}

type groupView struct {
	English, Japanese string
	Prefectures       []prefectureView
}

type optionView struct {
	Value, English, Japanese string
	Selected                 bool
}

type activityView struct {
	When, Label, Title, Text, Result string
	Sent                             bool
}

type pageData struct {
	Nonce, Version   string
	Saved            bool
	Again            bool
	Running          bool
	Summary          string
	Sources          []Source
	Notes            []string
	Groups           []groupView
	Everywhere       bool
	Options          []optionView
	Popup            bool
	StartupOffered   bool
	StartWithWindows bool
	WebhookSaved     bool
	WebhookTyped     string
	WebhookErrors    []string
	LocationErrors   []string
	IntensityErrors  []string
	ErrorList        []string
	Recent           []activityView
}

// choice is what the form holds, whether it came from the saved settings or from a submission that did not pass.
type choice struct {
	webhookTyped     string
	popup            bool
	places           map[int]bool
	everywhere       bool
	intensity        string
	startWithWindows bool
}

func (s *server) page(w http.ResponseWriter, r *http.Request) {
	snap := s.app.Snapshot()
	chosen := fromSaved(snap.Saved, snap.PopupDefault)
	chosen.startWithWindows = snap.StartupOn
	s.render(w, http.StatusOK, snap, chosen, nil, banner{
		saved: r.URL.Query().Get("saved") == "1",
		again: r.URL.Query().Get(againParam) == "1",
	})
}

// banner is which of the notes at the top of the page apply to this visit.
type banner struct{ saved, again bool }

func (s *server) save(w http.ResponseWriter, r *http.Request) {
	snap := s.app.Snapshot()
	chosen := fromForm(r.PostForm)

	file := config.File{
		WebhookURL:   chosen.webhookTyped,
		Location:     chosen.locationText(),
		MinIntensity: chosen.intensity,
		Popup:        chosen.popup,
	}
	var problems error
	if file.Location == "" {
		problems = config.FieldError{Field: config.EnvLocation, Problem: "needs at least one place"}
	} else {
		problems = s.app.Save(r.Context(), file, chosen.startWithWindows)
	}
	if problems != nil {
		s.render(w, http.StatusUnprocessableEntity, snap, chosen, problems, banner{})
		return
	}
	http.Redirect(w, r, "/?saved=1", http.StatusSeeOther)
}

func (s *server) render(w http.ResponseWriter, code int, snap Snapshot, chosen choice, problems error, note banner) {
	data := pageData{
		Nonce:   nonce(),
		Version: s.version,
		Saved:   note.saved,
		Again:   note.again,
		Running: snap.Running,
		Summary: snap.Summary,
		Sources: snap.Sources,
		Notes:   snap.Notes,
		Groups: groupsFor(
			chosen,
		),
		Everywhere:       chosen.everywhere,
		Popup:            chosen.popup,
		StartupOffered:   snap.StartupOffered,
		StartWithWindows: chosen.startWithWindows,
		Options:          optionsFor(chosen.intensity),
		WebhookSaved:     strings.TrimSpace(snap.Saved.WebhookURL) != "",
		WebhookTyped:     chosen.webhookTyped,
	}
	for _, a := range listed(snap.Recent) {
		data.Recent = append(data.Recent, activityView{
			When: inJST(a.When), Label: a.Label, Title: a.Title, Text: a.Text, Result: a.Result, Sent: a.Sent,
		})
	}
	why := explain(problems)
	data.WebhookErrors, data.LocationErrors, data.IntensityErrors, data.ErrorList = why.webhook, why.location, why.intensity, why.other

	setHeaders(w, data.Nonce)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	_ = s.tmpl.Execute(w, data)
}

func fromSaved(saved config.File, popupDefault bool) choice {
	// The box holds the saved address, so leaving it alone keeps it, typing replaces it and clearing it removes it.
	c := choice{
		places: map[int]bool{}, intensity: strings.TrimSpace(saved.MinIntensity), popup: saved.Popup,
		webhookTyped: strings.TrimSpace(saved.WebhookURL),
	}
	if saved == (config.File{}) {
		// Nothing has been saved yet, so offer what suits this kind of computer.
		c.popup = popupDefault
	}
	if c.intensity == "" {
		c.intensity = defaultIntensity
	}
	location := strings.TrimSpace(saved.Location)
	if location == "" {
		location = defaultLocation
	}
	for part := range strings.SplitSeq(location, ",") {
		part = strings.TrimSpace(part)
		if strings.EqualFold(part, "all") {
			c.everywhere = true
		} else if p, ok := region.Lookup(part); ok {
			c.places[p.Code] = true
		}
	}
	return c
}

func fromForm(form url.Values) choice {
	c := choice{
		webhookTyped:     strings.TrimSpace(form.Get("webhook")),
		popup:            form.Get("popup") == "on",
		places:           map[int]bool{},
		everywhere:       form.Get("everywhere") == "on",
		intensity:        strings.TrimSpace(form.Get("min_intensity")),
		startWithWindows: form.Get("start_with_windows") == "on",
	}
	for _, value := range form["location"] {
		if code, err := strconv.Atoi(value); err == nil {
			c.places[code] = true
		}
	}
	return c
}

// locationText is the choice written as the LOCATION setting: "all", or the English names in prefecture order.
func (c choice) locationText() string {
	if c.everywhere {
		return "all"
	}
	var names []string
	for _, p := range region.Prefectures() {
		if c.places[p.Code] {
			names = append(names, p.English)
		}
	}
	return strings.Join(names, ",")
}

// regionRange is a block of prefecture numbers; the numbers run north to south, so a region is a range.
type regionRange struct {
	english, japanese string
	from, to          int
}

func regionRanges() []regionRange {
	return []regionRange{
		{"Hokkaido", "北海道", 1, 1},
		{"Tohoku", "東北", 2, 7},
		{"Kanto", "関東", 8, 14},
		{"Chubu", "中部", 15, 23},
		{"Kansai", "近畿", 24, 30},
		{"Chugoku", "中国", 31, 35},
		{"Shikoku", "四国", 36, 39},
		{"Kyushu and Okinawa", "九州・沖縄", 40, 47},
	}
}

func groupsFor(c choice) []groupView {
	prefectures := region.Prefectures()
	var groups []groupView
	for _, rr := range regionRanges() {
		g := groupView{English: rr.english, Japanese: rr.japanese}
		for _, p := range prefectures {
			if p.Code >= rr.from && p.Code <= rr.to {
				g.Prefectures = append(g.Prefectures, prefectureView{
					Code: p.Code, English: p.English, Japanese: p.Japanese, Checked: c.places[p.Code],
				})
			}
		}
		groups = append(groups, g)
	}
	return groups
}

func optionsFor(selected string) []optionView {
	if selected == "" {
		selected = defaultIntensity
	}
	options := []optionView{
		{Value: "1", English: "Intensity 1", Japanese: "震度1"},
		{Value: "2", English: "Intensity 2", Japanese: "震度2"},
		{Value: "3", English: "Intensity 3", Japanese: "震度3"},
		{Value: "4", English: "Intensity 4", Japanese: "震度4"},
		{Value: "5-", English: "Intensity 5-lower (recommended)", Japanese: "震度5弱（おすすめ）"},
		{Value: "5+", English: "Intensity 5-upper", Japanese: "震度5強"},
		{Value: "6-", English: "Intensity 6-lower", Japanese: "震度6弱"},
		{Value: "6+", English: "Intensity 6-upper", Japanese: "震度6強"},
		{Value: "7", English: "Intensity 7 or over", Japanese: "震度7以上"},
	}
	for i := range options {
		options[i].Selected = options[i].Value == selected
	}
	return options
}

// explain turns the problems from Save into sentences a person can act on, grouped by the setting they belong to.
func explain(problems error) explained {
	var out explained
	if problems == nil {
		return out
	}
	found := config.Problems(problems)
	if len(found) == 0 {
		out.other = []string{problems.Error()}
		return out
	}
	for _, p := range found {
		switch p.Field {
		case config.EnvWebhookURL:
			text := "Webhook address: paste the address here. / Webhook のアドレスを貼り付けてください。"
			switch {
			case strings.Contains(p.Problem, "POPUP"):
				text = "Add a webhook address, or turn on the pop-up. / Webhook のアドレスを入力するか、ポップアップをオンにしてください。"
			case strings.Contains(p.Problem, "http"):
				text = "Webhook address: it must start with https:// — copy it again from Discord. / アドレスは https:// で始まります。もう一度コピーしてください。"
			}
			out.webhook = append(out.webhook, text)
			out.other = append(out.other, text)
		case config.EnvLocation:
			text := "Location: choose at least one place, or Whole Japan. / 場所を1つ以上選ぶか、日本全国を選んでください。"
			out.location = append(out.location, text)
			out.other = append(out.other, text)
		case config.EnvMinIntensity:
			text := "Minimum earthquake strength: choose one from the list. / 一覧から最小の震度を選んでください。"
			out.intensity = append(out.intensity, text)
			out.other = append(out.other, text)
		default:
			out.other = append(out.other, p.Error())
		}
	}
	return out
}

// explained is the sentences for each setting, and all of them together for the list at the top of the page.
type explained struct {
	webhook, location, intensity, other []string
}
