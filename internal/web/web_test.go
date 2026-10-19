package web_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/tienduc120/japan-emergency-alerts/internal/config"
	"github.com/tienduc120/japan-emergency-alerts/internal/web"
)

const (
	key     = "one-time-key"
	host    = "127.0.0.1:47600"
	origin  = "http://" + host
	token   = "very-secret-token"
	webhook = "https://discord.com/api/webhooks/123/" + token
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

type fakeSettings struct {
	mu          sync.Mutex
	snap        web.Snapshot
	saveErr     error
	testErr     error
	popupErr    error
	saved       []config.File
	tested      []string
	popupsShown int
	quits       int
}

func (f *fakeSettings) Quit() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.quits++
}

func (f *fakeSettings) TestPopup(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.popupsShown++
	return f.popupErr
}

func (f *fakeSettings) Snapshot() web.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap
}

func (f *fakeSettings) Save(_ context.Context, file config.File) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saved = append(f.saved, file)
	return f.saveErr
}

func (f *fakeSettings) Test(_ context.Context, hook string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tested = append(f.tested, hook)
	return f.testErr
}

func (f *fakeSettings) savedFiles() []config.File {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]config.File(nil), f.saved...)
}

func newHandler(app web.Settings) http.Handler {
	return web.New(app, web.Options{Key: key, Hosts: []string{host, "localhost:47600"}, Version: "test"})
}

type response struct {
	code   int
	body   string
	header http.Header
	result *http.Response
}

func send(
	h http.Handler,
	method, path string,
	form url.Values,
	cookie *http.Cookie,
	headers map[string]string,
) response {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, body)
	req.Host = host
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for name, value := range headers {
		if name == "Host" {
			req.Host = value
			continue
		}
		req.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	res := rec.Result()
	return response{code: res.StatusCode, body: rec.Body.String(), header: res.Header, result: res}
}

func login(t *testing.T, h http.Handler) *http.Cookie {
	t.Helper()
	res := send(h, http.MethodGet, "/?key="+key, nil, nil, nil)
	for _, c := range res.result.Cookies() {
		if c.Value != "" {
			return c
		}
	}
	t.Fatalf("logging in gave status %d and no cookie", res.code)
	return nil
}

func sameOrigin() map[string]string { return map[string]string{"Origin": origin} }

func running() web.Snapshot {
	return web.Snapshot{
		Saved:   config.File{WebhookURL: webhook, Location: "Tokyo,Kanagawa", MinIntensity: "6-"},
		Running: true,
		Summary: "Watching Tokyo, Kanagawa (earthquake reports from 5弱)",
		Sources: []string{"JMA feeds: OK, last read in full less than a minute ago", "Early-warning stream: connected"},
	}
}

func TestWithoutTheKeyNothingIsShown(t *testing.T) {
	t.Parallel()
	h := newHandler(&fakeSettings{snap: running()})

	for _, path := range []string{"/", "/status.json", "/?key=wrong"} {
		res := send(h, http.MethodGet, path, nil, nil, nil)

		if res.code != http.StatusForbidden {
			t.Errorf("GET %s = %d, want 403", path, res.code)
		}
		if strings.Contains(res.body, "Tokyo") || strings.Contains(res.body, token) {
			t.Errorf("GET %s leaked settings:\n%s", path, res.body)
		}
	}
	if res := send(h, http.MethodGet, "/", nil, nil, nil); !strings.Contains(res.body, "window") {
		t.Errorf("the refusal must tell the user where to find the address:\n%s", res.body)
	}
}

func TestTheKeyIsExchangedForACookieAndRemovedFromTheAddress(t *testing.T) {
	t.Parallel()
	h := newHandler(&fakeSettings{snap: running()})

	res := send(h, http.MethodGet, "/?key="+key, nil, nil, nil)

	if res.code != http.StatusSeeOther || res.header.Get("Location") != "/" {
		t.Fatalf("status %d, Location %q, want a redirect to /", res.code, res.header.Get("Location"))
	}
	cookies := res.result.Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Errorf(
			"cookies = %v, want one HttpOnly SameSite=Strict cookie: other sites must not be able to send it",
			cookies,
		)
	}
	if page := send(h, http.MethodGet, "/", nil, cookies[0], nil); page.code != http.StatusOK {
		t.Errorf("the page with the cookie = %d, want 200", page.code)
	}
}

func TestEveryAnswerNamesTheProgramSoASecondCopyCanRecognizeIt(t *testing.T) {
	t.Parallel()
	h := newHandler(&fakeSettings{snap: running()})

	for name, res := range map[string]response{
		"no key":     send(h, http.MethodGet, "/", nil, nil, nil),
		"favicon":    send(h, http.MethodGet, "/favicon.ico", nil, nil, nil),
		"wrong host": send(h, http.MethodGet, "/", nil, nil, map[string]string{"Host": "evil.example"}),
		"signed in":  send(h, http.MethodGet, "/", nil, login(t, h), nil),
	} {
		if got := res.header.Get("Server"); got != "japan-emergency-alerts" {
			t.Errorf("%s: Server = %q, want the program's name, which a second copy looks for", name, got)
		}
	}
}

func TestAnotherHostNameIsRefusedAgainstDNSRebinding(t *testing.T) {
	t.Parallel()
	h := newHandler(&fakeSettings{snap: running()})
	cookie := login(t, h)

	res := send(h, http.MethodGet, "/", nil, cookie, map[string]string{"Host": "evil.example:47600"})

	if res.code != http.StatusForbidden {
		t.Errorf("status = %d, want 403: a web page must not reach this server under another name", res.code)
	}
}

func TestThePageIsBilingualAndOffersEveryPrefectureAsAChecklist(t *testing.T) {
	t.Parallel()
	h := newHandler(&fakeSettings{snap: web.Snapshot{}})
	cookie := login(t, h)

	res := send(h, http.MethodGet, "/", nil, cookie, nil)

	for _, want := range []string{
		"Where should alerts go?", "通知先", "Where are you?", "現在地", "Whole Japan", "日本全国",
		"Kanto", "関東", "Kansai", "近畿", "Tokyo", "東京都", "Okinawa", "沖縄県",
		"Save and start", "保存して開始", "テスト", "Recent activity", "最近の通知",
	} {
		if !strings.Contains(res.body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	boxes := regexp.MustCompile(`<input type="checkbox" name="location" value="\d+"`).FindAllString(res.body, -1)
	if len(boxes) != 47 {
		t.Errorf("%d prefecture checkboxes, want 47", len(boxes))
	}
}

func TestTheSavedSettingsArePreselected(t *testing.T) {
	t.Parallel()
	h := newHandler(&fakeSettings{snap: running()})
	cookie := login(t, h)

	res := send(h, http.MethodGet, "/", nil, cookie, nil)

	checked := regexp.MustCompile(`name="location" value="(\d+)" checked`).FindAllStringSubmatch(res.body, -1)
	var codes []string
	for _, m := range checked {
		codes = append(codes, m[1])
	}
	if strings.Join(codes, ",") != "13,14" {
		t.Errorf("checked prefectures = %v, want Tokyo (13) and Kanagawa (14) only", codes)
	}
	if !regexp.MustCompile(`value="6-" selected`).MatchString(res.body) {
		t.Error("the saved earthquake strength (6-) is not selected")
	}
}

func TestWithNothingSavedTokyoAndFiveLowerAreTheDefaults(t *testing.T) {
	t.Parallel()
	h := newHandler(&fakeSettings{snap: web.Snapshot{}})
	cookie := login(t, h)

	res := send(h, http.MethodGet, "/", nil, cookie, nil)

	if !regexp.MustCompile(`name="location" value="13" checked`).MatchString(res.body) ||
		!regexp.MustCompile(`value="5-" selected`).MatchString(res.body) {
		t.Error("with nothing saved, Tokyo and 5- should be preselected")
	}
}

func TestWholeJapanIsPreselectedWhenSaved(t *testing.T) {
	t.Parallel()
	snap := running()
	snap.Saved.Location = "all"
	h := newHandler(&fakeSettings{snap: snap})
	cookie := login(t, h)

	res := send(h, http.MethodGet, "/", nil, cookie, nil)

	if !regexp.MustCompile(`name="everywhere"[^>]* checked`).MatchString(res.body) {
		t.Error("Whole Japan should be checked")
	}
}

func TestTheSavedWebhookFillsTheBoxSoItCanBeChangedOrCleared(t *testing.T) {
	t.Parallel()
	h := newHandler(&fakeSettings{snap: running()})
	cookie := login(t, h)

	page := send(h, http.MethodGet, "/", nil, cookie, nil)
	status := send(h, http.MethodGet, "/status.json", nil, cookie, nil)

	if !regexp.MustCompile(`id="webhook"[^>]*value="` + regexp.QuoteMeta(webhook) + `"`).MatchString(page.body) {
		t.Errorf("the box must hold the saved address, or clearing it means nothing:\n%s", page.body)
	}
	if !strings.Contains(page.body, "Clear") {
		t.Error("the page should say that clearing the box and saving removes the address")
	}
	if strings.Contains(status.body, token) {
		t.Errorf("the live status has no use for the secret part of the webhook:\n%s", status.body)
	}
}

func TestSavingSendsTheChosenSettingsAndWelcomesTheUserBack(t *testing.T) {
	t.Parallel()
	app := &fakeSettings{snap: running()}
	h := newHandler(app)
	cookie := login(t, h)
	form := url.Values{
		"webhook":       {"https://discord.com/api/webhooks/9/new"},
		"location":      {"13", "14", "27"},
		"min_intensity": {"5-"},
	}

	res := send(h, http.MethodPost, "/save", form, cookie, sameOrigin())

	if res.code != http.StatusSeeOther || !strings.HasPrefix(res.header.Get("Location"), "/?saved=1") {
		t.Fatalf("status %d, Location %q, want a redirect back to the page", res.code, res.header.Get("Location"))
	}
	want := config.File{
		WebhookURL:   "https://discord.com/api/webhooks/9/new",
		Location:     "Tokyo,Kanagawa,Osaka",
		MinIntensity: "5-",
	}
	if got := app.savedFiles(); len(got) != 1 || got[0] != want {
		t.Errorf("saved %+v, want %+v", got, want)
	}
}

func TestAnEmptiedWebhookBoxRemovesTheSavedAddress(t *testing.T) {
	t.Parallel()
	app := &fakeSettings{snap: running()}
	h := newHandler(app)
	cookie := login(t, h)

	send(
		h,
		http.MethodPost,
		"/save",
		url.Values{"webhook": {"  "}, "location": {"13"}, "min_intensity": {"4"}},
		cookie,
		sameOrigin(),
	)

	got := app.savedFiles()
	if len(got) != 1 || got[0].WebhookURL != "" {
		t.Errorf("saved %+v, want no webhook: the box shows the saved address, so an empty box means remove it", got)
	}
}

// browserFormOrigin is the Origin a browser puts on a form posted from this page. The Fetch standard makes it "null"
// when the page's Referrer-Policy is no-referrer, which is how Save came to be refused for real people while every
// test that wrote the header by hand passed.
func browserFormOrigin(referrerPolicy string) string {
	if referrerPolicy == "no-referrer" {
		return "null"
	}
	return origin
}

func TestSavingWorksFromTheOriginABrowserReallySends(t *testing.T) {
	t.Parallel()
	app := &fakeSettings{snap: running()}
	h := newHandler(app)
	cookie := login(t, h)
	page := send(h, http.MethodGet, "/", nil, cookie, nil)
	form := url.Values{"webhook": {webhook}, "location": {"13"}, "min_intensity": {"5-"}}

	res := send(h, http.MethodPost, "/save", form, cookie,
		map[string]string{"Origin": browserFormOrigin(page.header.Get("Referrer-Policy"))})

	if res.code != http.StatusSeeOther || len(app.savedFiles()) != 1 {
		t.Errorf(
			"status %d with %d saves, want the form the page offers to be accepted",
			res.code,
			len(app.savedFiles()),
		)
	}
}

func rows(body string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`(?s)<div class="row">(.*?)</div>`).FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestEachTestButtonSitsAtTheEndOfTheLineOfItsField(t *testing.T) {
	t.Parallel()
	h := newHandler(&fakeSettings{snap: running()})
	cookie := login(t, h)

	res := send(h, http.MethodGet, "/", nil, cookie, nil)

	together := func(field, button string) bool {
		for _, row := range rows(res.body) {
			f, b := strings.Index(row, field), strings.Index(row, button)
			if f >= 0 && b > f {
				return true
			}
		}
		return false
	}
	if !together(`id="webhook"`, `id="test"`) {
		t.Error("the webhook's test button must follow the webhook box on the same line")
	}
	if !together(`id="popup"`, `id="test-popup"`) {
		t.Error("the pop-up's test button must follow the pop-up checkbox on the same line")
	}
}

func TestTheTestButtonsAreSmallAndThePopupBoxIsBigger(t *testing.T) {
	t.Parallel()
	h := newHandler(&fakeSettings{snap: running()})
	cookie := login(t, h)

	res := send(h, http.MethodGet, "/", nil, cookie, nil)

	for _, id := range []string{"test", "test-popup"} {
		tag := regexp.MustCompile(`<button[^>]*id="` + id + `"[^>]*>`).FindString(res.body)
		if !strings.Contains(tag, `class="small"`) {
			t.Errorf("the %s button is %q, want the small style", id, tag)
		}
	}
	if !regexp.MustCompile(`#popup\s*\{[^}]*width:\s*1\.[4-9]rem`).MatchString(res.body) {
		t.Error("the pop-up checkbox should be bigger than an ordinary one")
	}
}

func TestWholeJapanOverridesTheChecklist(t *testing.T) {
	t.Parallel()
	app := &fakeSettings{snap: running()}
	h := newHandler(app)
	cookie := login(t, h)

	send(h, http.MethodPost, "/save",
		url.Values{"everywhere": {"on"}, "location": {"13", "14"}, "min_intensity": {"5-"}}, cookie, sameOrigin())

	if got := app.savedFiles(); len(got) != 1 || got[0].Location != "all" {
		t.Errorf("saved %+v, want location all", got)
	}
}

func TestAProblemIsShownBesideItsSettingWithTheUsersChoicesKept(t *testing.T) {
	t.Parallel()
	app := &fakeSettings{
		snap:    web.Snapshot{},
		saveErr: errors.Join(config.FieldError{Field: "WEBHOOK_URL", Problem: "must be an http or https URL"}),
	}
	h := newHandler(app)
	cookie := login(t, h)

	res := send(h, http.MethodPost, "/save",
		url.Values{"webhook": {"not-a-url"}, "location": {"27"}, "min_intensity": {"6+"}}, cookie, sameOrigin())

	if res.code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: nothing was saved", res.code)
	}
	for _, want := range []string{"Webhook address", "https://"} {
		if !strings.Contains(res.body, want) {
			t.Errorf("page lacks %q:\n%s", want, res.body)
		}
	}
	if !regexp.MustCompile(`name="location" value="27" checked`).MatchString(res.body) ||
		// Go's templates write a plus sign as &#43; inside an attribute, which browsers read as a plus sign.
		!regexp.MustCompile(`value="6(?:\+|&#43;)" selected`).MatchString(res.body) {
		t.Error("the user's choices must survive a failed save")
	}
	if strings.Contains(res.body, "Saved") {
		t.Error("a failed save must not claim to have saved")
	}
}

func TestSavingNeedsTheCookieAndTheRightOrigin(t *testing.T) {
	t.Parallel()
	app := &fakeSettings{snap: running()}
	h := newHandler(app)
	cookie := login(t, h)
	form := url.Values{"webhook": {webhook}, "location": {"13"}, "min_intensity": {"5-"}}

	cases := map[string]response{
		"no cookie":   send(h, http.MethodPost, "/save", form, nil, sameOrigin()),
		"no origin":   send(h, http.MethodPost, "/save", form, cookie, nil),
		"null origin": send(h, http.MethodPost, "/save", form, cookie, map[string]string{"Origin": "null"}),
		"foreign origin": send(
			h,
			http.MethodPost,
			"/save",
			form,
			cookie,
			map[string]string{"Origin": "https://evil.example"},
		),
		"foreign host": send(
			h,
			http.MethodPost,
			"/save",
			form,
			cookie,
			map[string]string{"Origin": origin, "Host": "evil.example"},
		),
	}
	for name, res := range cases {
		if res.code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", name, res.code)
		}
	}
	if len(app.savedFiles()) != 0 {
		t.Error("a refused request must not change the settings")
	}
}

func TestTheTestButtonSendsThroughTheProgramAndReportsTheOutcome(t *testing.T) {
	t.Parallel()
	app := &fakeSettings{snap: running()}
	h := newHandler(app)
	cookie := login(t, h)

	ok := send(
		h,
		http.MethodPost,
		"/test",
		url.Values{"webhook": {"https://discord.com/api/webhooks/9/new"}},
		cookie,
		sameOrigin(),
	)
	app.testErr = errors.New("webhook answered 404 Not Found")
	failed := send(h, http.MethodPost, "/test", url.Values{"webhook": {""}}, cookie, sameOrigin())

	var good, bad struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(ok.body), &good); err != nil || !good.OK {
		t.Errorf("success response %q (%v), want ok=true", ok.body, err)
	}
	if err := json.Unmarshal([]byte(failed.body), &bad); err != nil || bad.OK || !strings.Contains(bad.Message, "404") {
		t.Errorf("failure response %q (%v), want ok=false and the reason", failed.body, err)
	}
	if len(app.tested) != 2 || app.tested[0] != "https://discord.com/api/webhooks/9/new" || app.tested[1] != "" {
		t.Errorf("tested %q, want the typed address and then an empty one, which means the saved address", app.tested)
	}
}

func TestTestingNeedsTheSameProtectionAsSaving(t *testing.T) {
	t.Parallel()
	app := &fakeSettings{snap: running()}
	h := newHandler(app)

	res := send(h, http.MethodPost, "/test", url.Values{"webhook": {webhook}}, nil, sameOrigin())

	if res.code != http.StatusForbidden || len(app.tested) != 0 {
		t.Errorf("status %d, tested %v, want a refusal that sends nothing", res.code, app.tested)
	}
}

func TestStatusIsAvailableAsJSONForTheLiveUpdate(t *testing.T) {
	t.Parallel()
	snap := running()
	snap.Recent = []web.Activity{{
		When: time.Date(2026, time.October, 18, 3, 0, 0, 0, time.UTC), Label: "Earthquake report",
		Title: "【震源・震度情報】", Result: "Not sent: outside the watched location",
	}}
	h := newHandler(&fakeSettings{snap: snap})
	cookie := login(t, h)

	res := send(h, http.MethodGet, "/status.json", nil, cookie, nil)

	var got struct {
		Running bool     `json:"running"`
		Summary string   `json:"summary"`
		Sources []string `json:"sources"`
		Recent  []struct {
			When   string `json:"when"`
			Label  string `json:"label"`
			Result string `json:"result"`
		} `json:"recent"`
	}
	if err := json.Unmarshal([]byte(res.body), &got); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, res.body)
	}
	if !got.Running || !strings.Contains(got.Summary, "Tokyo") || len(got.Sources) != 2 || len(got.Recent) != 1 {
		t.Errorf("status = %+v, want the running summary, two sources and one recent item", got)
	}
	if got.Recent[0].When != "2026-10-18 12:00 JST" || !strings.Contains(got.Recent[0].Result, "outside") {
		t.Errorf("recent = %+v, want the time in JST and the reason", got.Recent[0])
	}
}

func TestRecentActivityIsEscapedBecauseItComesFromTheOutsideWorld(t *testing.T) {
	t.Parallel()
	snap := running()
	snap.Recent = []web.Activity{
		{
			Label:  "Special warning",
			Title:  "<script>alert(1)</script>",
			Text:   "<img src=x onerror=alert(2)>",
			Result: "Sent",
			Sent:   true,
		},
	}
	h := newHandler(&fakeSettings{snap: snap})
	cookie := login(t, h)

	res := send(h, http.MethodGet, "/", nil, cookie, nil)

	if strings.Contains(res.body, "<script>alert(1)") || strings.Contains(res.body, "<img src=x") {
		t.Errorf("activity text was not escaped:\n%s", res.body)
	}
	if !strings.Contains(res.body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Error("the text should still be visible, as text")
	}
}

func TestNotesTheUserShouldKnowAreShown(t *testing.T) {
	t.Parallel()
	snap := web.Snapshot{Notes: []string{"LOCATION is set in the environment and overrides this page."}}
	h := newHandler(&fakeSettings{snap: snap})
	cookie := login(t, h)

	res := send(h, http.MethodGet, "/", nil, cookie, nil)

	if !strings.Contains(res.body, "overrides this page") {
		t.Errorf("the note is missing:\n%s", res.body)
	}
	if !strings.Contains(res.body, "Not set up yet") {
		t.Error("with nothing running the page should say it is not set up yet")
	}
}

func TestTheBrowserIsToldNotToKeepOrEmbedOrSniffThePage(t *testing.T) {
	t.Parallel()
	h := newHandler(&fakeSettings{snap: running()})
	cookie := login(t, h)

	res := send(h, http.MethodGet, "/", nil, cookie, nil)

	csp := res.header.Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "frame-ancestors 'none'", "form-action 'self'", "script-src 'nonce-"} {
		if !strings.Contains(csp, want) {
			t.Errorf("Content-Security-Policy %q lacks %q", csp, want)
		}
	}
	if res.header.Get("X-Content-Type-Options") != "nosniff" || res.header.Get("Cache-Control") != "no-store" ||
		res.header.Get("Referrer-Policy") != "same-origin" {
		t.Errorf("headers = %v, want nosniff, no-store and a referrer sent to this page only", res.header)
	}
}

func popupBox(body string) string {
	m := regexp.MustCompile(`<input type="checkbox" name="popup"[^>]*>`).FindString(body)
	return m
}

func TestThePageOffersAPopupBesideTheWebhookInBothLanguages(t *testing.T) {
	t.Parallel()
	h := newHandler(&fakeSettings{snap: running()})
	cookie := login(t, h)

	res := send(h, http.MethodGet, "/", nil, cookie, nil)

	for _, want := range []string{"Show a pop-up window on this computer", "このパソコンにポップアップを表示"} {
		if !strings.Contains(res.body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if popupBox(res.body) == "" {
		t.Fatal("no pop-up checkbox")
	}
	if tag := regexp.MustCompile(`(?s)<button[^>]*id="test-popup"[^>]*>.*?</button>`).
		FindString(res.body); !strings.Contains(
		tag,
		"テスト",
	) {
		t.Errorf("the pop-up's test button is %q, want a button with a Japanese label too", tag)
	}
}

func TestAFieldAndItsTestButtonNeverSplitAcrossLines(t *testing.T) {
	t.Parallel()
	h := newHandler(&fakeSettings{snap: running()})
	cookie := login(t, h)

	res := send(h, http.MethodGet, "/", nil, cookie, nil)

	if !regexp.MustCompile(`\.row\s*\{[^}]*flex-wrap:\s*nowrap`).MatchString(res.body) {
		t.Error("a row must not wrap, or the test button drops under its field on a narrow window")
	}
	if !regexp.MustCompile(`\.row label\.check\s*\{[^}]*min-width:\s*0`).MatchString(res.body) {
		t.Error("the label in a row must be allowed to shrink, so its words wrap and the button stays put")
	}
}

func TestTheSaveButtonShowsItsJapaneseAsClearlyAsItsEnglish(t *testing.T) {
	t.Parallel()
	h := newHandler(&fakeSettings{snap: running()})
	cookie := login(t, h)

	res := send(h, http.MethodGet, "/", nil, cookie, nil)

	if !regexp.MustCompile(`button\.primary\s+\.ja\s*\{[^}]*color:\s*inherit`).MatchString(res.body) {
		t.Error("the grey Japanese text is hard to read on the red button; it must take the button's colour")
	}
}

func TestTheDiscordStepsAreTuckedAwayUntilAskedFor(t *testing.T) {
	t.Parallel()
	h := newHandler(&fakeSettings{snap: running()})
	cookie := login(t, h)

	res := send(h, http.MethodGet, "/", nil, cookie, nil)

	if !regexp.MustCompile(`(?s)<details[^>]*>\s*<summary>[^<]*Discord.*?Copy Webhook URL.*?</details>`).
		MatchString(res.body) {
		t.Errorf("the steps for getting a Discord webhook belong in a closed section of their own:\n%s", res.body)
	}
}

func TestTheStopButtonIsAtTheTopSoNobodyHasToScrollForIt(t *testing.T) {
	t.Parallel()
	h := newHandler(&fakeSettings{snap: running()})
	cookie := login(t, h)

	res := send(h, http.MethodGet, "/", nil, cookie, nil)

	stop, save := strings.Index(res.body, `action="/quit"`), strings.Index(res.body, `action="/save"`)
	if stop < 0 || save < 0 || stop > save {
		t.Errorf("the Stop button is at %d and the settings form at %d, want the button first", stop, save)
	}
}

func TestTheStopButtonCannotBePressedWhileNothingIsRunning(t *testing.T) {
	t.Parallel()
	stopTag := func(snap web.Snapshot) string {
		h := newHandler(&fakeSettings{snap: snap})
		res := send(h, http.MethodGet, "/", nil, login(t, h), nil)
		return regexp.MustCompile(`<button[^>]*id="stop"[^>]*>`).FindString(res.body)
	}

	idle, watching := stopTag(web.Snapshot{}), stopTag(running())

	if idle == "" || !strings.Contains(idle, " disabled") {
		t.Errorf("with nothing running the button is %q, want it disabled", idle)
	}
	if watching == "" || strings.Contains(watching, "disabled") {
		t.Errorf("while watching the button is %q, want it enabled", watching)
	}
	h := newHandler(&fakeSettings{snap: running()})
	page := send(h, http.MethodGet, "/", nil, login(t, h), nil)
	if !strings.Contains(page.body, "$('stop').disabled") {
		t.Error("the live update must also enable and disable the button, or it would go stale after saving")
	}
}

func TestOpeningTheProgramAgainSaysItIsAlreadyRunning(t *testing.T) {
	t.Parallel()
	h := newHandler(&fakeSettings{snap: running()})

	again := send(h, http.MethodGet, "/?key="+key+"&again=1", nil, nil, nil)
	first := send(h, http.MethodGet, "/?key="+key, nil, nil, nil)
	cookie := login(t, h)
	withNote := send(h, http.MethodGet, "/?again=1", nil, cookie, nil)
	without := send(h, http.MethodGet, "/", nil, cookie, nil)

	if again.header.Get("Location") != "/?again=1" || first.header.Get("Location") != "/" {
		t.Errorf("redirects %q and %q, want the marker kept only when the second copy asked for it",
			again.header.Get("Location"), first.header.Get("Location"))
	}
	if !strings.Contains(withNote.body, "already running") || !strings.Contains(withNote.body, "すでに起動") {
		t.Errorf("the page lacks a note, in both languages, that the program was already running:\n%s", withNote.body)
	}
	if strings.Contains(without.body, "already running") {
		t.Error("an ordinary visit must not claim the program was opened again")
	}
}

func TestThePageCanStopTheProgramAndOnlyForSomeoneSignedInFromThisPage(t *testing.T) {
	t.Parallel()
	app := &fakeSettings{snap: running()}
	h := newHandler(app)
	cookie := login(t, h)

	page := send(h, http.MethodGet, "/", nil, cookie, nil)
	noCookie := send(h, http.MethodPost, "/quit", url.Values{}, nil, sameOrigin())
	foreign := send(
		h,
		http.MethodPost,
		"/quit",
		url.Values{},
		cookie,
		map[string]string{"Origin": "https://evil.example"},
	)
	get := send(h, http.MethodGet, "/quit", nil, cookie, nil)
	if app.quits != 0 {
		t.Fatalf("%d stops after refused requests, want none", app.quits)
	}
	ok := send(h, http.MethodPost, "/quit", url.Values{}, cookie, sameOrigin())

	if !strings.Contains(page.body, `action="/quit"`) || !strings.Contains(page.body, "Stop running") ||
		!strings.Contains(page.body, "停止") {
		t.Error("the page needs a Stop button, in both languages: with no window there is no other way to stop it")
	}
	if noCookie.code != http.StatusForbidden || foreign.code != http.StatusForbidden || get.code == http.StatusOK {
		t.Errorf("no cookie %d, foreign origin %d, GET %d, want all refused", noCookie.code, foreign.code, get.code)
	}
	if ok.code != http.StatusOK || app.quits != 1 || !strings.Contains(ok.body, "停止") {
		t.Errorf(
			"status %d, %d stops, body:\n%s\nwant one stop and a page saying so in both languages",
			ok.code,
			app.quits,
			ok.body,
		)
	}
}

func TestThePopupBoxFollowsWhatWasSavedAndOtherwiseTheSystemDefault(t *testing.T) {
	t.Parallel()
	saved := running()
	saved.Saved.Popup = true
	savedOff := running()
	fresh := web.Snapshot{PopupDefault: true}
	freshOff := web.Snapshot{PopupDefault: false}

	cases := map[string]struct {
		snap web.Snapshot
		want bool
	}{
		"saved on":                {saved, true},
		"saved off":               {savedOff, false},
		"nothing saved, desktop":  {fresh, true},
		"nothing saved, headless": {freshOff, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newHandler(&fakeSettings{snap: tc.snap})
			cookie := login(t, h)

			res := send(h, http.MethodGet, "/", nil, cookie, nil)

			if got := strings.Contains(popupBox(res.body), " checked"); got != tc.want {
				t.Errorf("checked = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSavingWithAPopupAndNoWebhookIsAllowed(t *testing.T) {
	t.Parallel()
	app := &fakeSettings{snap: web.Snapshot{}}
	h := newHandler(app)
	cookie := login(t, h)

	res := send(h, http.MethodPost, "/save",
		url.Values{"popup": {"on"}, "location": {"13"}, "min_intensity": {"5-"}}, cookie, sameOrigin())

	want := config.File{Location: "Tokyo", MinIntensity: "5-", Popup: true}
	if got := app.savedFiles(); res.code != http.StatusSeeOther || len(got) != 1 || got[0] != want {
		t.Errorf("status %d, saved %+v, want %+v", res.code, got, want)
	}
}

func TestLeavingThePopupBoxEmptySavesItAsOff(t *testing.T) {
	t.Parallel()
	snap := running()
	snap.Saved.Popup = true
	app := &fakeSettings{snap: snap}
	h := newHandler(app)
	cookie := login(t, h)

	send(h, http.MethodPost, "/save", url.Values{"location": {"13"}, "min_intensity": {"5-"}}, cookie, sameOrigin())

	if got := app.savedFiles(); len(got) != 1 || got[0].Popup {
		t.Errorf("saved %+v, want pop-ups off: an unticked box is a choice", got)
	}
}

func TestNeitherAWebhookNorAPopupIsExplainedInPlainWords(t *testing.T) {
	t.Parallel()
	app := &fakeSettings{
		snap: web.Snapshot{},
		saveErr: config.FieldError{
			Field: "WEBHOOK_URL", Problem: "is required, unless POPUP is on to show alerts in a window instead",
		},
	}
	h := newHandler(app)
	cookie := login(t, h)

	res := send(
		h,
		http.MethodPost,
		"/save",
		url.Values{"location": {"13"}, "min_intensity": {"5-"}},
		cookie,
		sameOrigin(),
	)

	if res.code != http.StatusUnprocessableEntity || !strings.Contains(res.body, "turn on the pop-up") {
		t.Errorf(
			"status %d, body:\n%s\nwant a plain sentence saying to add a webhook or turn on the pop-up",
			res.code,
			res.body,
		)
	}
	if strings.Contains(res.body, "POPUP") {
		t.Error("the page must not talk in setting names")
	}
}

func TestTheTestPopupButtonShowsAWindowAndReportsTheOutcome(t *testing.T) {
	t.Parallel()
	app := &fakeSettings{snap: running()}
	h := newHandler(app)
	cookie := login(t, h)

	ok := send(h, http.MethodPost, "/test-popup", url.Values{}, cookie, sameOrigin())
	app.popupErr = errors.New("no pop-up tool found")
	failed := send(h, http.MethodPost, "/test-popup", url.Values{}, cookie, sameOrigin())
	refused := send(h, http.MethodPost, "/test-popup", url.Values{}, nil, sameOrigin())

	var good, bad struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(ok.body), &good); err != nil || !good.OK {
		t.Errorf("success response %q (%v)", ok.body, err)
	}
	if err := json.Unmarshal(
		[]byte(failed.body),
		&bad,
	); err != nil || bad.OK ||
		!strings.Contains(bad.Message, "no pop-up tool") {
		t.Errorf("failure response %q (%v), want the reason", failed.body, err)
	}
	if refused.code != http.StatusForbidden || app.popupsShown != 2 {
		t.Errorf("refused status %d after %d windows, want a refusal that shows nothing", refused.code, app.popupsShown)
	}
}

func TestThePageHasAWhiteBackgroundWhateverTheSystemTheme(t *testing.T) {
	t.Parallel()
	h := newHandler(&fakeSettings{snap: running()})
	cookie := login(t, h)

	res := send(h, http.MethodGet, "/", nil, cookie, nil)

	if !strings.Contains(res.body, "color-scheme: light;") ||
		!regexp.MustCompile(`background:\s*#fff`).MatchString(res.body) {
		t.Error("the page must declare a light scheme and a white background")
	}
	if strings.Contains(res.body, "light dark") || strings.Contains(res.body, "prefers-color-scheme") {
		t.Error("the page must not switch to a dark theme when the system uses one")
	}
}

func TestTheEarthquakeStrengthChoiceReadsNaturally(t *testing.T) {
	t.Parallel()
	h := newHandler(&fakeSettings{snap: running()})
	cookie := login(t, h)

	res := send(h, http.MethodGet, "/", nil, cookie, nil)

	for _, want := range []string{"Minimum earthquake strength", "通知する最小の震度", "Intensity 7 or over", "震度7以上"} {
		if !strings.Contains(res.body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, unwanted := range []string{"How strong should an earthquake be", "the strongest", "最大の震度"} {
		if strings.Contains(res.body, unwanted) {
			t.Errorf("page still says %q", unwanted)
		}
	}
}

func TestTheIconIsServedAndNamedInThePage(t *testing.T) {
	t.Parallel()
	h := newHandler(&fakeSettings{snap: running()})
	cookie := login(t, h)

	icon := send(h, http.MethodGet, "/favicon.ico", nil, nil, nil)
	page := send(h, http.MethodGet, "/", nil, cookie, nil)

	if icon.code != http.StatusOK || icon.header.Get("Content-Type") != "image/x-icon" {
		t.Fatalf("icon: status %d, type %q, want 200 image/x-icon (a browser asks before it has the cookie)",
			icon.code, icon.header.Get("Content-Type"))
	}
	if !strings.HasPrefix(icon.body, "\x00\x00\x01\x00") {
		t.Errorf("icon starts %x, want the ICO file signature 00 00 01 00", icon.body[:min(6, len(icon.body))])
	}
	if !strings.Contains(page.body, `<link rel="icon" href="/favicon.ico"`) {
		t.Error("the page must name its icon")
	}
	if csp := page.header.Get("Content-Security-Policy"); !strings.Contains(csp, "img-src 'self'") {
		t.Errorf("Content-Security-Policy %q must allow the page's own images, or the icon is blocked", csp)
	}
}

func TestTheIconIsNotServedUnderAnotherHostName(t *testing.T) {
	t.Parallel()
	h := newHandler(&fakeSettings{snap: running()})

	res := send(h, http.MethodGet, "/favicon.ico", nil, nil, map[string]string{"Host": "evil.example"})

	if res.code != http.StatusForbidden {
		t.Errorf("status = %d, want 403: the host check covers every path", res.code)
	}
}

func TestUnknownPathsAndWrongMethodsAreRefused(t *testing.T) {
	t.Parallel()
	h := newHandler(&fakeSettings{snap: running()})
	cookie := login(t, h)

	if res := send(h, http.MethodGet, "/nothing", nil, cookie, nil); res.code != http.StatusNotFound {
		t.Errorf("GET /nothing = %d, want 404", res.code)
	}
	if res := send(h, http.MethodGet, "/save", nil, cookie, nil); res.code != http.StatusMethodNotAllowed {
		t.Errorf("GET /save = %d, want 405", res.code)
	}
}
