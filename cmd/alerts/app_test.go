package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
	"github.com/tienduc120/japan-emergency-alerts/internal/config"
	"github.com/tienduc120/japan-emergency-alerts/internal/jma"
	"github.com/tienduc120/japan-emergency-alerts/internal/web"
)

// watcher is a source that tells the test each time the program starts watching, and then waits to be stopped.
type watcher struct {
	started chan struct{}
	src     *fakeSource
}

func newWatcher(emit func(ctx context.Context, handle jma.Handler)) *watcher {
	w := &watcher{started: make(chan struct{}, 8)}
	w.src = &fakeSource{run: func(ctx context.Context, handle jma.Handler) error {
		w.started <- struct{}{}
		if emit != nil {
			emit(ctx, handle)
		}
		<-ctx.Done()
		return nil
	}}
	return w
}

func (w *watcher) waitForStart(t *testing.T) {
	t.Helper()
	select {
	case <-w.started:
	case <-time.After(waitLimit):
		t.Fatal("the program never started watching")
	}
}

func saveForm(webhook, location string) url.Values {
	return url.Values{"webhook": {webhook}, "location": {location}, "min_intensity": {"5-"}}
}

func TestFirstRunOpensTheSettingsPageAndWatchesNothing(t *testing.T) {
	t.Parallel()
	w := newWatcher(nil)
	h := newHarness(t, nil, fakeSources(w.src))
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)

	var opened string
	select {
	case opened = <-h.opened:
	case <-time.After(waitLimit):
		t.Fatalf("the browser was never opened:\n%s", h.stdout.String())
	}

	out := h.stdout.String()
	if opened == "" || !strings.Contains(out, opened) {
		t.Errorf(
			"opened %q, stdout:\n%s\nwant the same address printed for people whose browser did not open",
			opened,
			out,
		)
	}
	if !strings.Contains(out, "Not set up yet") {
		t.Errorf("stdout must say nothing is set up yet:\n%s", out)
	}
	if len(w.started) != 0 {
		t.Error("nothing should be watched before there is a webhook")
	}
	if code := stopped(t, cancel, done); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func TestAFirstRunPageDoesNotShoutAboutTheMissingWebhook(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil, fakeSources(newWatcher(nil).src))
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)
	page := newBrowser(t, h.address())

	_, body := page.signIn(h.address())

	if strings.Contains(body, "unless POPUP") || strings.Contains(body, "WEBHOOK_URL") {
		t.Errorf("a first run is not an error, and the page must not talk in setting names:\n%s", body)
	}
	stopped(t, cancel, done)
}

func TestAConfiguredStartWatchesAtOnceAndLeavesTheBrowserAlone(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t, nil)
	w := newWatcher(nil)
	h := newHarness(t, map[string]string{"WEBHOOK_URL": rec.webhook()}, fakeSources(w.src))
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)

	w.waitForStart(t)
	address := h.address()

	out := h.stdout.String()
	if !strings.Contains(out, "Watching Tokyo") || !strings.Contains(out, address) {
		t.Errorf("stdout must say what is watched and where the settings are:\n%s", out)
	}
	if len(h.opened) != 0 {
		t.Error("the browser must not open when the program can already run")
	}
	if code := stopped(t, cancel, done); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

func TestSavingThroughThePageStartsWatchingWithWhatWasSaved(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t, nil)
	w := newWatcher(nil)
	h := newHarness(t, nil, fakeSources(w.src))
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)
	page := newBrowser(t, h.address())
	if status, body := page.signIn(h.address()); status != 200 || !strings.Contains(body, "Not set up yet") {
		t.Fatalf("the first page = %d:\n%s", status, body)
	}

	status, _ := page.post("/save", saveForm(rec.webhook(), "27"))
	w.waitForStart(t)

	if status != 303 {
		t.Errorf("save answered %d, want a redirect", status)
	}
	saved, err := os.ReadFile(h.configPath)
	if err != nil || !strings.Contains(string(saved), "Osaka") || !strings.Contains(string(saved), rec.webhook()) {
		t.Errorf("settings file %q (%v), want the webhook and Osaka", saved, err)
	}
	_, body := page.get("/status.json")
	var reply struct {
		Running bool   `json:"running"`
		Summary string `json:"summary"`
	}
	if err = json.Unmarshal(
		[]byte(body),
		&reply,
	); err != nil || !reply.Running ||
		!strings.Contains(reply.Summary, "Osaka") {
		t.Errorf("status %q (%v), want running and watching Osaka", body, err)
	}
	stopped(t, cancel, done)
}

func TestSavingAgainRestartsTheWatchingWithTheNewSettings(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t, nil)
	w := newWatcher(nil)
	h := newHarness(t, map[string]string{"WEBHOOK_URL": rec.webhook()}, fakeSources(w.src))
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)
	w.waitForStart(t)
	page := newBrowser(t, h.address())
	page.signIn(h.address())

	page.post("/save", saveForm("", "1"))
	w.waitForStart(t)

	_, body := page.get("/status.json")
	if !strings.Contains(body, "Hokkaido") {
		t.Errorf("status %q, want the new location", body)
	}
	stopped(t, cancel, done)
}

func TestSettingsThatDoNotPassChangeNothing(t *testing.T) {
	t.Parallel()
	w := newWatcher(nil)
	h := newHarness(t, nil, fakeSources(w.src))
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)
	page := newBrowser(t, h.address())
	page.signIn(h.address())

	status, body := page.post("/save", saveForm("not-a-web-address", "13"))

	if status != 422 || !strings.Contains(body, "https://") {
		t.Errorf("status %d, body:\n%s\nwant a refusal that explains the address", status, body)
	}
	if _, err := os.Stat(h.configPath); err == nil {
		t.Error("a refused save must not create the settings file")
	}
	if len(w.started) != 0 {
		t.Error("nothing should start watching")
	}
	stopped(t, cancel, done)
}

func TestTheTestButtonSendsWithoutSaving(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t, nil)
	h := newHarness(t, nil, fakeSources(newWatcher(nil).src))
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)
	page := newBrowser(t, h.address())
	page.signIn(h.address())

	_, body := page.post("/test", url.Values{"webhook": {rec.webhook()}})

	if rec.count() != 1 || !strings.Contains(body, `"ok":true`) {
		t.Errorf("%d posts, reply %s, want one test message and ok", rec.count(), body)
	}
	if _, err := os.Stat(h.configPath); err == nil {
		t.Error("testing must not save the settings")
	}
	stopped(t, cancel, done)
}

func TestTestingWithNoAddressAsksForOne(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil, fakeSources(newWatcher(nil).src))
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)
	page := newBrowser(t, h.address())
	page.signIn(h.address())

	_, body := page.post("/test", url.Values{"webhook": {""}})

	if !strings.Contains(body, `"ok":false`) || !strings.Contains(body, "paste") {
		t.Errorf("reply %s, want a request to paste an address first", body)
	}
	stopped(t, cancel, done)
}

func TestClearingTheWebhookAndSavingRemovesItFromTheSettingsFile(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t, nil)
	w := newWatcher(nil)
	h := newHarness(t, nil, fakeSources(w.src))
	if err := config.Save(
		h.configPath,
		config.File{WebhookURL: rec.webhook(), Location: "Tokyo", MinIntensity: "5-", Popup: true},
	); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)
	w.waitForStart(t)
	page := newBrowser(t, h.address())
	_, body := page.signIn(h.address())
	if !strings.Contains(body, rec.webhook()) {
		t.Fatalf("the box should show the saved address:\n%s", body)
	}

	status, _ := page.post(
		"/save",
		url.Values{"webhook": {""}, "popup": {"on"}, "location": {"13"}, "min_intensity": {"5-"}},
	)
	w.waitForStart(t)

	saved, err := os.ReadFile(h.configPath)
	if status != 303 || err != nil || strings.Contains(string(saved), rec.webhook()) {
		t.Errorf("status %d, file %q (%v), want the webhook gone from the file", status, saved, err)
	}
	stopped(t, cancel, done)
}

func TestClearingTheWebhookWithNoPopupIsRefusedAndKeepsTheFile(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t, nil)
	h := newHarness(t, nil, fakeSources(newWatcher(nil).src))
	if err := config.Save(
		h.configPath,
		config.File{WebhookURL: rec.webhook(), Location: "Tokyo", MinIntensity: "5-"},
	); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)
	page := newBrowser(t, h.address())
	page.signIn(h.address())

	status, _ := page.post("/save", url.Values{"webhook": {""}, "location": {"13"}, "min_intensity": {"5-"}})

	saved, err := os.ReadFile(h.configPath)
	if status != 422 || err != nil || !strings.Contains(string(saved), rec.webhook()) {
		t.Errorf(
			"status %d, file %q (%v), want a refusal: alerts need a webhook or a pop-up, and nothing changes",
			status,
			saved,
			err,
		)
	}
	stopped(t, cancel, done)
}

func TestTestingWithAnEmptyBoxDoesNotReachForTheSavedAddress(t *testing.T) {
	t.Parallel()
	saved := newRecorder(t, nil)
	h := newHarness(t, nil, fakeSources(newWatcher(nil).src))
	if err := config.Save(
		h.configPath,
		config.File{WebhookURL: saved.webhook(), Location: "Tokyo", MinIntensity: "5-"},
	); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)
	page := newBrowser(t, h.address())
	page.signIn(h.address())

	_, body := page.post("/test", url.Values{"webhook": {""}})

	if saved.count() != 0 || !strings.Contains(body, `"ok":false`) {
		t.Errorf(
			"%d posts, reply %s, want nothing sent: the box shows the saved address, so an empty box has none to test",
			saved.count(),
			body,
		)
	}
	stopped(t, cancel, done)
}

func TestTestingWithAnEmptyBoxUsesTheWebhookThatIsInUse(t *testing.T) {
	t.Parallel()
	inUse := newRecorder(t, nil)
	saved := newRecorder(t, nil)
	h := newHarness(t, map[string]string{"WEBHOOK_URL": inUse.webhook()}, fakeSources(newWatcher(nil).src))
	if err := config.Save(
		h.configPath,
		config.File{WebhookURL: saved.webhook(), Location: "Tokyo", MinIntensity: "5-"},
	); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)
	page := newBrowser(t, h.address())
	page.signIn(h.address())

	_, body := page.post("/test", url.Values{"webhook": {""}})

	if inUse.count() != 1 || saved.count() != 0 || !strings.Contains(body, `"ok":true`) {
		t.Errorf(
			"in use %d, saved %d, reply %s, want the test to reach the webhook the environment sets, which wins",
			inUse.count(),
			saved.count(),
			body,
		)
	}
	stopped(t, cancel, done)
}

func TestRecentActivityShowsWhatWasSentAndWhatWasNot(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t, nil)
	sent := make(chan struct{})
	w := newWatcher(func(ctx context.Context, handle jma.Handler) {
		_ = handle(ctx, tsunamiFor(13, "tokyo"))
		_ = handle(ctx, tsunamiFor(1, "hokkaido"))
		close(sent)
	})
	h := newHarness(t, map[string]string{"WEBHOOK_URL": rec.webhook()}, fakeSources(w.src))
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)
	<-sent
	page := newBrowser(t, h.address())
	page.signIn(h.address())

	_, body := page.get("/status.json")

	var reply struct {
		Recent []struct {
			Result string `json:"result"`
			Label  string `json:"label"`
		} `json:"recent"`
	}
	if err := json.Unmarshal([]byte(body), &reply); err != nil || len(reply.Recent) != 2 {
		t.Fatalf("status %q (%v), want two recent items", body, err)
	}
	if reply.Recent[0].Result != "Sent" || reply.Recent[1].Result != "Not sent: outside the watched location" {
		t.Errorf(
			"recent = %+v, want sent (Tokyo), then the newer one for elsewhere (Hokkaido) at the end",
			reply.Recent,
		)
	}
	if reply.Recent[0].Label != "Tsunami warning" {
		t.Errorf("label = %q, want the kind of alert", reply.Recent[0].Label)
	}
	stopped(t, cancel, done)
}

func TestThePageShowsALightForEachSource(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t, nil)
	w := newWatcher(nil)
	w.src.status, w.src.health = "Early-warning stream: NOT connected since 2026-10-18 22:00 JST", alert.Down
	h := newHarness(t, map[string]string{"WEBHOOK_URL": rec.webhook()}, fakeSources(w.src))
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)
	w.waitForStart(t)
	page := newBrowser(t, h.address())
	page.signIn(h.address())

	_, body := page.get("/status.json")

	var reply struct {
		Sources []web.Source `json:"sources"`
	}
	want := []web.Source{{Text: w.src.status, State: alert.Down}}
	if err := json.Unmarshal([]byte(body), &reply); err != nil || !slices.Equal(reply.Sources, want) {
		t.Errorf("sources %+v (%v), want %+v", reply.Sources, err, want)
	}
	stopped(t, cancel, done)
}

func TestAnEnvironmentSettingThatOverridesThePageIsExplained(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t, nil)
	h := newHarness(
		t,
		map[string]string{"WEBHOOK_URL": rec.webhook(), "LOCATION": "Osaka"},
		fakeSources(newWatcher(nil).src),
	)
	if err := config.Save(
		h.configPath,
		config.File{WebhookURL: rec.webhook(), Location: "Tokyo", MinIntensity: "5-"},
	); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)
	page := newBrowser(t, h.address())

	_, body := page.signIn(h.address())

	if !strings.Contains(body, "LOCATION") || !strings.Contains(body, "overrides") {
		t.Errorf("the page must say that LOCATION in the environment wins over what is saved here:\n%s", body)
	}
	stopped(t, cancel, done)
}

func TestADamagedSettingsFileIsExplainedAndReplacedBySaving(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t, nil)
	w := newWatcher(nil)
	h := newHarness(t, nil, fakeSources(w.src))
	if err := os.WriteFile(h.configPath, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)
	page := newBrowser(t, h.address())

	_, first := page.signIn(h.address())
	page.post("/save", saveForm(rec.webhook(), "13"))
	w.waitForStart(t)

	if !strings.Contains(first, "not usable") {
		t.Errorf("the page must explain that the settings file is damaged:\n%s", first)
	}
	if saved, err := os.ReadFile(h.configPath); err != nil || !strings.Contains(string(saved), "Tokyo") {
		t.Errorf("settings file %q (%v), want it replaced by the new settings", saved, err)
	}
	stopped(t, cancel, done)
}

func TestWhenTheUsualPortIsBusyAnotherOneIsUsed(t *testing.T) {
	t.Parallel()
	var lc net.ListenConfig
	busy, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = busy.Close() })

	got, err := listenPreferring(t.Context(), busy.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = got.Close() })
	if got.Addr().String() == busy.Addr().String() {
		t.Error("got the busy address back")
	}
	if host, _, splitErr := net.SplitHostPort(got.Addr().String()); splitErr != nil || host != "127.0.0.1" {
		t.Errorf("address %q, want the local computer only", got.Addr())
	}
}

func TestAnotherProgramOnTheUsualPortDoesNotStopTheStart(t *testing.T) {
	t.Parallel()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(other.Close)

	got, err := listenPreferring(t.Context(), strings.TrimPrefix(other.URL, "http://"))
	if err != nil {
		t.Fatalf(
			"listenPreferring = %v, want another port: a stranger on the usual port is not a copy of this program",
			err,
		)
	}
	t.Cleanup(func() { _ = got.Close() })
}

func TestASecondCopyDoesNotStartWhileOneIsRunning(t *testing.T) {
	t.Parallel()
	first := newHarness(t, nil, fakeSources(newWatcher(nil).src))
	ctx, cancel := context.WithCancel(t.Context())
	done := first.start(ctx)
	usual := ""
	if u, err := url.Parse(first.address()); err == nil {
		usual = u.Host
	}

	w := newWatcher(nil)
	second := newHarness(t, nil, fakeSources(w.src))
	second.env.listen = func(ctx context.Context) (net.Listener, error) { return listenPreferring(ctx, usual) }
	// A second copy that wrongly carried on would run until its context ended.
	give, stop := context.WithTimeout(t.Context(), 3*time.Second)
	defer stop()
	code := execute(give, nil, second.env)

	if code == 0 || !strings.Contains(second.stderr.String(), "already running") {
		t.Errorf(
			"exit %d, stderr %q, want a refusal that says the program is already running",
			code,
			second.stderr.String(),
		)
	}
	if len(w.started) != 0 || len(second.opened) != 0 {
		t.Error("a second copy must not watch, and must not open another settings page")
	}
	stopped(t, cancel, done)
}

func TestASecondCopyOpensTheSettingsPageOfTheFirst(t *testing.T) {
	t.Parallel()
	first := newHarness(t, nil, fakeSources(newWatcher(nil).src))
	ctx, cancel := context.WithCancel(t.Context())
	done := first.start(ctx)
	address := first.address()
	usual := ""
	if u, err := url.Parse(address); err == nil {
		usual = u.Host
	}

	w := newWatcher(nil)
	second := newHarness(t, map[string]string{"CONFIG_FILE": first.configPath}, fakeSources(w.src))
	second.env.listen = func(ctx context.Context) (net.Listener, error) { return listenPreferring(ctx, usual) }
	give, stop := context.WithTimeout(t.Context(), 3*time.Second)
	defer stop()
	code := execute(give, nil, second.env)

	if code != 0 || !strings.Contains(second.stdout.String(), "already running") {
		t.Errorf(
			"exit %d, stdout %q, want a calm exit that says the program is already running",
			code,
			second.stdout.String(),
		)
	}
	select {
	case opened := <-second.opened:
		if opened != address+"?again=1" {
			t.Errorf(
				"opened %q, want the first copy's own settings address %q, marked as a second opening",
				opened,
				address,
			)
		}
	default:
		t.Error("the first copy's settings page should have been opened")
	}
	if len(w.started) != 0 {
		t.Error("a second copy must not watch")
	}
	stopped(t, cancel, done)
}

func TestTheSettingsAddressIsForgottenWhenTheProgramStops(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil, fakeSources(newWatcher(nil).src))
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)
	address := h.address()
	kept := config.AddressFilePath(h.env.getenv)
	if data, err := os.ReadFile(kept); err != nil || strings.TrimSpace(string(data)) != address {
		t.Fatalf("address file %q (%v), want the settings address %q while running", data, err, address)
	}

	stopped(t, cancel, done)

	if _, err := os.Stat(kept); err == nil {
		t.Error("the address must not outlive the program, or a second copy would open a page that is gone")
	}
}

func TestAnAddressFileThatPointsElsewhereIsNeverOpened(t *testing.T) {
	t.Parallel()
	first := newHarness(t, nil, fakeSources(newWatcher(nil).src))
	ctx, cancel := context.WithCancel(t.Context())
	done := first.start(ctx)
	usual := ""
	if u, err := url.Parse(first.address()); err == nil {
		usual = u.Host
	}
	if err := os.WriteFile(
		config.AddressFilePath(first.env.getenv),
		[]byte("https://example.com/\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	second := newHarness(t, map[string]string{"CONFIG_FILE": first.configPath}, fakeSources(newWatcher(nil).src))
	second.env.listen = func(ctx context.Context) (net.Listener, error) { return listenPreferring(ctx, usual) }
	give, stop := context.WithTimeout(t.Context(), 3*time.Second)
	defer stop()
	code := execute(give, nil, second.env)

	if code == 0 || len(second.opened) != 0 {
		t.Errorf(
			"exit %d, %d pages opened, want nothing opened: only this computer's own settings page may be",
			code,
			len(second.opened),
		)
	}
	stopped(t, cancel, done)
}

var startupBox = regexp.MustCompile(`<input type="checkbox" name="start_with_windows"[^>]*>`)

func popupOnlyForm(startWithWindows bool) url.Values {
	form := url.Values{"popup": {"on"}, "location": {"13"}, "min_intensity": {"5-"}}
	if startWithWindows {
		form.Set("start_with_windows", "on")
	}
	return form
}

func TestSavingWithTheBoxTickedTurnsTheEntryOn(t *testing.T) {
	t.Parallel()
	w, entry := newWatcher(nil), &fakeStartup{}
	h := newHarness(t, nil, fakeSources(w.src))
	h.onWindows(entry)
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)
	page := newBrowser(t, h.address())
	page.signIn(h.address())

	status, _ := page.post("/save", popupOnlyForm(true))
	w.waitForStart(t)
	_, body := page.get("/")

	enabled, disabled := entry.calls()
	if status != http.StatusSeeOther || !slices.Equal(enabled, []string{fakeExe}) || disabled != 0 {
		t.Errorf(
			"status %d, enabled %v, disabled %d times, want the entry pointed at this program once",
			status,
			enabled,
			disabled,
		)
	}
	if !strings.Contains(startupBox.FindString(body), "checked") {
		t.Errorf("box %q, want it ticked after saving", startupBox.FindString(body))
	}
	stopped(t, cancel, done)
}

func TestSavingWithTheBoxEmptyTurnsTheEntryOff(t *testing.T) {
	t.Parallel()
	w, entry := newWatcher(nil), &fakeStartup{on: true, exe: fakeExe}
	h := newHarness(t, nil, fakeSources(w.src))
	h.onWindows(entry)
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)
	page := newBrowser(t, h.address())
	page.signIn(h.address())

	status, _ := page.post("/save", popupOnlyForm(false))
	w.waitForStart(t)
	_, body := page.get("/")

	if _, disabled := entry.calls(); status != http.StatusSeeOther || disabled != 1 {
		t.Errorf("status %d, disabled %d times, want the entry removed once", status, disabled)
	}
	if box := startupBox.FindString(body); box == "" || strings.Contains(box, "checked") {
		t.Errorf("box %q, want it offered and unticked", box)
	}
	stopped(t, cancel, done)
}

func TestAnEntryThatCannotBeWrittenIsReportedAndNothingIsSaved(t *testing.T) {
	t.Parallel()
	entry := &fakeStartup{enableErr: errors.New("access denied")}
	h := newHarness(t, nil, fakeSources(newWatcher(nil).src))
	h.onWindows(entry)
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)
	page := newBrowser(t, h.address())
	page.signIn(h.address())

	status, body := page.post("/save", popupOnlyForm(true))

	if status != http.StatusUnprocessableEntity ||
		!strings.Contains(body, "start with Windows could not be set: access denied") {
		t.Errorf("status %d, want the page back saying that starting with Windows could not be set:\n%s", status, body)
	}
	if _, err := os.Stat(h.configPath); err == nil {
		t.Error("nothing may be kept unless every setting passes, the start-with-Windows box included")
	}
	stopped(t, cancel, done)
}

func TestTheSummaryIsAnEnglishLineAndAJapaneseLine(t *testing.T) {
	t.Parallel()
	cfg := configFor(t, "https://discord.com/api/webhooks/1/x")
	place, strength := cfg.Location.String(), cfg.MinIntensity.Japanese()

	got := strings.Split(summary(cfg), "\n")

	want := []string{"Watching " + place + " from intensity " + strength, "監視中：" + place + "・震度" + strength + "以上"}
	if !slices.Equal(got, want) {
		t.Errorf("summary lines %q, want %q: the page shows each language on its own line", got, want)
	}
}

func TestEveryDecisionIsRecordedForTheActivityList(t *testing.T) {
	t.Parallel()
	sender := &fakeSender{failNext: 1}
	var seen []string
	r := newRelay(t, sender, &bytes.Buffer{})
	r.activity = func(a web.Activity) { seen = append(seen, a.Result) }

	_ = r.handle(t.Context(), tsunamiFor(13, "fails-to-send"))
	_ = r.handle(t.Context(), tsunamiFor(13, "fails-to-send"))
	_ = r.handle(t.Context(), tsunamiFor(1, "elsewhere"))

	want := []string{"Could not send: webhook down", "Sent", "Not sent: outside the watched location"}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Errorf("recorded %v, want %v", seen, want)
	}
}

func TestWhenTheActivityListIsFullOlderAlertsForElsewhereMakeRoomFirst(t *testing.T) {
	t.Parallel()
	var log activityLog
	for i := range maxActivity + 1 {
		log.add(web.Activity{Title: "mine " + strconv.Itoa(i)})
	}
	for i := range 5 {
		log.add(web.Activity{Title: "elsewhere " + strconv.Itoa(i), Elsewhere: true})
	}

	var titles []string
	for _, a := range log.list() {
		titles = append(titles, a.Title)
	}

	want := []string{"elsewhere 4"}
	for i := maxActivity; i >= 2; i-- {
		want = append(want, "mine "+strconv.Itoa(i))
	}
	if !slices.Equal(titles, want) {
		t.Errorf("list = %v\nwant %v\nthe newest is always kept, and an older alert for elsewhere makes room before "+
			"any of mine", titles, want)
	}
}

func TestOnlyAnAlertForAPlaceNotWatchedIsMarkedAsElsewhere(t *testing.T) {
	t.Parallel()
	var elsewhere []bool
	r := newRelay(t, &fakeSender{}, &bytes.Buffer{})
	r.activity = func(a web.Activity) { elsewhere = append(elsewhere, a.Elsewhere) }

	_ = r.handle(t.Context(), tsunamiFor(1, "hokkaido"))
	_ = r.handle(t.Context(), tsunamiFor(13, "tokyo"))
	_ = r.handle(t.Context(), tsunamiFor(13, "tokyo"))

	if want := []bool{true, false, false}; !slices.Equal(elsewhere, want) {
		t.Errorf(
			"elsewhere = %v, want %v: only the alert outside the watched location, not one sent or already sent",
			elsewhere,
			want,
		)
	}
}

// emitOne is a source that offers one Tokyo tsunami warning and then waits to be stopped.
func emitOne(done chan<- struct{}) *watcher {
	return newWatcher(func(ctx context.Context, handle jma.Handler) {
		_ = handle(ctx, tsunamiFor(13, "tokyo"))
		close(done)
	})
}

func TestAPopupAloneIsEnoughToBeTold(t *testing.T) {
	t.Parallel()
	offered := make(chan struct{})
	h := newHarness(t, map[string]string{"POPUP": "on"}, fakeSources(emitOne(offered).src))
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)

	window := h.window()
	<-offered

	if !strings.Contains(window[0], "Tsunami warning") || !strings.Contains(window[1], "津波警報") {
		t.Errorf("window %q / %q, want the kind of alert and JMA's text", window[0], window[1])
	}
	page := newBrowser(t, h.address())
	_, body := page.signIn(h.address())
	_, status := page.get("/status.json")
	if !strings.Contains(status, `"result":"Sent"`) || strings.Contains(body, "WEBHOOK_URL") {
		t.Errorf("status %s, want the alert recorded as sent with no webhook needed", status)
	}
	stopped(t, cancel, done)
}

func TestAnAlertReachesTheWebhookAndAWindowTogether(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t, nil)
	offered := make(chan struct{})
	h := newHarness(
		t,
		map[string]string{"POPUP": "on", "WEBHOOK_URL": rec.webhook()},
		fakeSources(emitOne(offered).src),
	)
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)

	h.window()
	rec.waitForBody(t, "tsunami")
	<-offered

	if rec.count() != 1 {
		t.Errorf("%d webhook posts, want 1", rec.count())
	}
	stopped(t, cancel, done)
}

func TestADeadWebhookNeverCostsTheAlertItsWindow(t *testing.T) {
	t.Parallel()
	rec := newRecorder(t, func(int) int { return 404 })
	var calls atomic.Int32
	w := newWatcher(func(ctx context.Context, handle jma.Handler) {
		if err := handle(ctx, tsunamiFor(13, "tokyo")); err != nil {
			t.Errorf("handle = %v, want success: the window carried the alert", err)
		}
		calls.Add(1)
	})
	h := newHarness(t, map[string]string{"POPUP": "on", "WEBHOOK_URL": rec.webhook()}, fakeSources(w.src))
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)

	h.window()
	deadline := time.After(waitLimit)
	for calls.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("the source never got an answer")
		case <-time.After(time.Millisecond):
		}
	}

	stopped(t, cancel, done)
}

func TestASlowWebhookNeverDelaysTheWindow(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	var once sync.Once
	letGo := func() { once.Do(func() { close(release) }) }
	rec := newRecorder(t, func(int) int {
		<-release
		return http.StatusNoContent
	})
	t.Cleanup(letGo) // runs before the recorder's server is closed, which waits for the held request
	offered := make(chan struct{})
	h := newHarness(
		t,
		map[string]string{"POPUP": "on", "WEBHOOK_URL": rec.webhook()},
		fakeSources(emitOne(offered).src),
	)
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)

	h.window() // the webhook is still being held here
	letGo()
	<-offered

	stopped(t, cancel, done)
}

func TestStoppingFromThePageEndsTheProgramCleanly(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil, fakeSources(newWatcher(nil).src))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := h.start(ctx)
	page := newBrowser(t, h.address())
	page.signIn(h.address())

	status, body := page.post("/quit", url.Values{})

	if status != http.StatusOK || !strings.Contains(body, "停止") {
		t.Errorf("status %d, body %q, want a page that says the program has stopped", status, body)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit code = %d, want 0: stopping on purpose is not a failure", code)
		}
	case <-time.After(waitLimit):
		t.Fatal("the program did not stop")
	}
	if _, err := os.Stat(config.AddressFilePath(h.env.getenv)); err == nil {
		t.Error("the settings address must go with the program")
	}
}

func TestSavingAPopupOnlySetupThroughThePage(t *testing.T) {
	t.Parallel()
	w := newWatcher(nil)
	h := newHarness(t, nil, fakeSources(w.src))
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)
	page := newBrowser(t, h.address())
	page.signIn(h.address())

	status, _ := page.post("/save", url.Values{"popup": {"on"}, "location": {"13"}, "min_intensity": {"5-"}})
	w.waitForStart(t)

	saved, err := os.ReadFile(h.configPath)
	if status != 303 || err != nil || !strings.Contains(string(saved), `"popup": true`) {
		t.Errorf("status %d, file %q (%v), want a pop-up-only setup saved and watching", status, saved, err)
	}
	stopped(t, cancel, done)
}

func TestTheTestPopupButtonShowsAWindowThatSaysItIsATest(t *testing.T) {
	t.Parallel()
	h := newHarness(t, nil, fakeSources(newWatcher(nil).src))
	ctx, cancel := context.WithCancel(t.Context())
	done := h.start(ctx)
	page := newBrowser(t, h.address())
	page.signIn(h.address())

	_, body := page.post("/test-popup", url.Values{})
	window := h.window()

	if !strings.Contains(body, `"ok":true`) || !strings.Contains(window[1], "not an alert") {
		t.Errorf("reply %s, window %q, want ok and a window that says it is only a test", body, window)
	}
	stopped(t, cancel, done)
}

func TestTheTestCommandCanShowAPopupToo(t *testing.T) {
	t.Parallel()
	h := newHarness(t, map[string]string{"POPUP": "on"}, fakeSources(newWatcher(nil).src))

	code := execute(t.Context(), []string{"test"}, h.env)

	window := h.window()
	if code != 0 || !strings.Contains(window[1], "not an alert") || !strings.Contains(h.stdout.String(), "pop-up") {
		t.Errorf(
			"exit %d, window %q, stdout %q, want a test window and a line saying so",
			code,
			window,
			h.stdout.String(),
		)
	}
}
