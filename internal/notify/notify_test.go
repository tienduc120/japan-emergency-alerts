package notify_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
	"github.com/tienduc120/japan-emergency-alerts/internal/intensity"
	"github.com/tienduc120/japan-emergency-alerts/internal/notify"
)

const token = "very-secret-token"

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

type request struct {
	method string
	header http.Header
	body   []byte
}

// hook is a fake webhook that records every request and answers as the test says.
type hook struct {
	mu   sync.Mutex
	seen []request
	srv  *httptest.Server
	url  *url.URL
}

func newHook(t *testing.T, respond func(n int, w http.ResponseWriter)) *hook {
	t.Helper()
	h := &hook{}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		h.mu.Lock()
		h.seen = append(h.seen, request{method: r.Method, header: r.Header.Clone(), body: body})
		n := len(h.seen)
		h.mu.Unlock()
		respond(n, w)
	}))
	t.Cleanup(h.srv.Close)
	u, err := url.Parse(h.srv.URL + "/api/webhooks/123/" + token)
	if err != nil {
		t.Fatal(err)
	}
	h.url = u
	return h
}

func (h *hook) requests() []request {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]request(nil), h.seen...)
}

func accept(_ int, w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

func status(code int) func(int, http.ResponseWriter) {
	return func(_ int, w http.ResponseWriter) { w.WriteHeader(code) }
}

func fast() notify.Options { return notify.Options{Backoff: time.Millisecond} }

func tsunami() alert.Alert {
	return alert.Alert{
		ID:     "t1",
		Kind:   alert.KindTsunami,
		Issued: time.Date(2026, time.October, 18, 3, 0, 0, 0, time.UTC),
		Areas:  []alert.Area{{Prefecture: 13}},
		Text:   "津波警報が発表されました。\nただちに高台へ避難してください。",
	}
}

type discordBody struct {
	Content string `json:"content"`
}

func contentOf(t *testing.T, r request) string {
	t.Helper()
	var b discordBody
	if err := json.Unmarshal(r.body, &b); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, r.body)
	}
	return b.Content
}

func TestDiscordGetsOnePostWithTheTextUnchanged(t *testing.T) {
	t.Parallel()
	h := newHook(t, accept)
	s := notify.New(h.url, notify.FormatDiscord, fast())
	a := tsunami()

	if err := s.Send(t.Context(), a); err != nil {
		t.Fatal(err)
	}

	reqs := h.requests()
	if len(reqs) != 1 {
		t.Fatalf("got %d requests, want 1", len(reqs))
	}
	if reqs[0].method != http.MethodPost || reqs[0].header.Get("Content-Type") != "application/json" {
		t.Errorf("method %q, content type %q, want a JSON POST", reqs[0].method, reqs[0].header.Get("Content-Type"))
	}
	if reqs[0].header.Get("User-Agent") == "" {
		t.Error("a webhook receiver should be able to tell who is calling")
	}
	content := contentOf(t, reqs[0])
	wants := []string{a.Text, "Tsunami warning", "2026-10-18 12:00 JST", "Japan Meteorological Agency", "Unofficial"}
	for _, want := range wants {
		if !strings.Contains(content, want) {
			t.Errorf("content lacks %q:\n%s", want, content)
		}
	}
}

func TestAnAlertWithoutAnIssueTimeHasNoTimeInTheHeader(t *testing.T) {
	t.Parallel()
	h := newHook(t, accept)
	s := notify.New(h.url, notify.FormatDiscord, fast())
	a := tsunami()
	a.Issued = time.Time{}

	if err := s.Send(t.Context(), a); err != nil {
		t.Fatal(err)
	}

	if content := contentOf(t, h.requests()[0]); strings.Contains(content, "JST") {
		t.Errorf("a made-up time would mislead:\n%s", content)
	}
}

func TestMentionsInTheTextCannotPingAnyone(t *testing.T) {
	t.Parallel()
	h := newHook(t, accept)
	s := notify.New(h.url, notify.FormatDiscord, fast())
	a := tsunami()
	a.Text = "@everyone @here **bold** `code`"

	if err := s.Send(t.Context(), a); err != nil {
		t.Fatal(err)
	}

	var body map[string]any
	if err := json.Unmarshal(h.requests()[0].body, &body); err != nil {
		t.Fatal(err)
	}
	mentions, _ := body["allowed_mentions"].(map[string]any)
	parse, ok := mentions["parse"].([]any)
	if !ok || len(parse) != 0 {
		t.Errorf("allowed_mentions = %v, want an empty parse list: JMA text must never ping a channel",
			body["allowed_mentions"])
	}
	if !strings.Contains(body["content"].(string), a.Text) {
		t.Error("the text must still arrive unchanged")
	}
}

func TestTheGenericWebhookGetsStructuredJSON(t *testing.T) {
	t.Parallel()
	h := newHook(t, accept)
	s := notify.New(h.url, notify.FormatJSON, fast())
	a := tsunami()

	if err := s.Send(t.Context(), a); err != nil {
		t.Fatal(err)
	}

	var got struct {
		ID     string `json:"id"`
		Kind   string `json:"kind"`
		Issued string `json:"issued"`
		Areas  []struct {
			Prefecture int    `json:"prefecture"`
			Intensity  string `json:"intensity"`
		} `json:"areas"`
		Text    string `json:"text"`
		Source  string `json:"source"`
		Notice  string `json:"notice"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(h.requests()[0].body, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "t1" || got.Kind != "tsunami" || got.Text != a.Text || got.Issued != "2026-10-18T03:00:00Z" {
		t.Errorf("got %+v, want the alert's id, kind, text and issue time", got)
	}
	if len(got.Areas) != 1 || got.Areas[0].Prefecture != 13 || got.Areas[0].Intensity != "" {
		t.Errorf("areas = %+v, want Tokyo with no intensity: only earthquake reports carry one", got.Areas)
	}
	if !strings.Contains(got.Source, "Japan Meteorological Agency") || !strings.Contains(got.Notice, "Unofficial") {
		t.Errorf(
			"source %q, notice %q: the credit and the disclaimer must travel with the data",
			got.Source,
			got.Notice,
		)
	}
	if !strings.Contains(got.Message, a.Text) {
		t.Errorf("message lacks the text:\n%s", got.Message)
	}
}

func TestAnEarthquakeReportCarriesItsIntensityPerArea(t *testing.T) {
	t.Parallel()
	h := newHook(t, accept)
	s := notify.New(h.url, notify.FormatJSON, fast())
	a := alert.Alert{
		ID:   "q1",
		Kind: alert.KindEarthquakeReport,
		Text: "震度速報",
		Areas: []alert.Area{
			{Prefecture: 13, Intensity: intensity.FiveLower},
			{Prefecture: 14, Intensity: intensity.Four},
		},
	}

	if err := s.Send(t.Context(), a); err != nil {
		t.Fatal(err)
	}

	var got struct {
		Areas []struct {
			Prefecture int    `json:"prefecture"`
			Intensity  string `json:"intensity"`
		} `json:"areas"`
	}
	if err := json.Unmarshal(h.requests()[0].body, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Areas) != 2 || got.Areas[0].Intensity != "5-" || got.Areas[1].Intensity != "4" {
		t.Errorf("areas = %+v, want intensities 5- and 4", got.Areas)
	}
}

func TestFormatForPicksDiscordByHost(t *testing.T) {
	t.Parallel()

	tests := map[string]notify.Format{
		"https://discord.com/api/webhooks/1/x":              notify.FormatDiscord,
		"https://discordapp.com/api/webhooks/1/x":           notify.FormatDiscord,
		"https://ptb.discord.com/api/webhooks/1/x":          notify.FormatDiscord,
		"https://canary.discord.com/api/webhooks/1/x":       notify.FormatDiscord,
		"https://example.com/hook":                          notify.FormatJSON,
		"https://notdiscord.com/api/webhooks/1/x":           notify.FormatJSON,
		"https://discord.com.evil.example/api/webhooks/1/x": notify.FormatJSON,
	}
	for raw, want := range tests {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			u, err := url.Parse(raw)
			if err != nil {
				t.Fatal(err)
			}

			if got := notify.FormatFor(u); got != want {
				t.Errorf("FormatFor(%s) = %v, want %v", raw, got, want)
			}
		})
	}
}

func TestALongMessageIsSplitAtLineBreaksWithNothingLost(t *testing.T) {
	t.Parallel()
	h := newHook(t, accept)
	s := notify.New(h.url, notify.FormatDiscord, fast())
	a := tsunami()
	line := strings.Repeat("あ", 90)
	a.Text = strings.TrimSuffix(strings.Repeat(line+"\n", 60), "\n")

	if err := s.Send(t.Context(), a); err != nil {
		t.Fatal(err)
	}

	reqs := h.requests()
	if len(reqs) < 2 {
		t.Fatalf("got %d requests, want the text split over several", len(reqs))
	}
	var contents []string
	for _, r := range reqs {
		c := contentOf(t, r)
		if n := len([]rune(c)); n > 2000 {
			t.Errorf("a message has %d characters, over Discord's limit of 2000", n)
		}
		contents = append(contents, c)
	}
	if joined := strings.Join(contents, "\n"); !strings.Contains(joined, a.Text) {
		t.Error("the text did not arrive intact across the parts")
	}
	if !strings.Contains(contents[len(contents)-1], "Japan Meteorological Agency") {
		t.Error("the credit must be in the last part")
	}
}

func TestOneEnormousLineStillArrivesInFullAndInOrder(t *testing.T) {
	t.Parallel()
	h := newHook(t, accept)
	s := notify.New(h.url, notify.FormatDiscord, fast())
	a := tsunami()
	a.Text = strings.Repeat("津", 4500)

	if err := s.Send(t.Context(), a); err != nil {
		t.Fatal(err)
	}

	var all strings.Builder
	for _, r := range h.requests() {
		c := contentOf(t, r)
		if n := len([]rune(c)); n > 2000 {
			t.Errorf("a message has %d characters, over Discord's limit of 2000", n)
		}
		all.WriteString(strings.ReplaceAll(c, "\n", ""))
	}
	if !strings.Contains(all.String(), a.Text) {
		t.Error("a line longer than the limit lost characters")
	}
}

func TestATooManyRequestsAnswerIsRetriedAfterTheDelayItGives(t *testing.T) {
	t.Parallel()
	h := newHook(t, func(n int, w http.ResponseWriter) {
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	s := notify.New(h.url, notify.FormatDiscord, fast())

	if err := s.Send(t.Context(), tsunami()); err != nil {
		t.Fatal(err)
	}

	if got := len(h.requests()); got != 2 {
		t.Errorf("got %d requests, want 2: one refused, one retried", got)
	}
}

func TestAServerErrorIsRetriedThenGivenUp(t *testing.T) {
	t.Parallel()
	h := newHook(t, status(http.StatusServiceUnavailable))
	s := notify.New(h.url, notify.FormatDiscord, fast())

	err := s.Send(t.Context(), tsunami())

	if err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("err = %v, want a failure naming status 503", err)
	}
	if got := len(h.requests()); got != 3 {
		t.Errorf("got %d requests, want 3 attempts in all", got)
	}
}

func TestAClientErrorIsNotRetried(t *testing.T) {
	t.Parallel()
	h := newHook(t, status(http.StatusNotFound))
	s := notify.New(h.url, notify.FormatDiscord, fast())

	err := s.Send(t.Context(), tsunami())

	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want a failure naming status 404", err)
	}
	if got := len(h.requests()); got != 1 {
		t.Errorf("got %d requests, want 1: asking again cannot fix a wrong URL", got)
	}
}

func TestAFailureNeverQuotesTheWebhookURL(t *testing.T) {
	t.Parallel()

	t.Run("connection refused", func(t *testing.T) {
		t.Parallel()
		h := newHook(t, accept)
		h.srv.Close()
		s := notify.New(h.url, notify.FormatDiscord, fast())

		err := s.Send(t.Context(), tsunami())

		if err == nil {
			t.Fatal("sending to a closed server must fail")
		}
		if strings.Contains(err.Error(), token) {
			t.Errorf("the error quotes the webhook, whose path is a secret:\n%v", err)
		}
	})

	t.Run("refused with a status", func(t *testing.T) {
		t.Parallel()
		h := newHook(t, status(http.StatusUnauthorized))
		s := notify.New(h.url, notify.FormatDiscord, fast())

		err := s.Send(t.Context(), tsunami())

		if err == nil || strings.Contains(err.Error(), token) {
			t.Errorf("err = %v, want a failure that does not quote the webhook", err)
		}
	})
}

func TestACancelledContextStopsRetryingAtOnce(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	h := newHook(t, func(_ int, w http.ResponseWriter) {
		cancel()
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	s := notify.New(h.url, notify.FormatDiscord, notify.Options{Backoff: time.Hour})

	err := s.Send(ctx, tsunami())

	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want the cancellation: waiting an hour to retry after a shutdown is a hang", err)
	}
	if got := len(h.requests()); got != 1 {
		t.Errorf("got %d requests, want 1", got)
	}
}

func TestALateAlertSaysSoInBothFormats(t *testing.T) {
	t.Parallel()
	late := tsunami()
	late.Late = true

	t.Run("discord", func(t *testing.T) {
		t.Parallel()
		h := newHook(t, accept)
		s := notify.New(h.url, notify.FormatDiscord, fast())
		if err := s.Send(t.Context(), late); err != nil {
			t.Fatal(err)
		}
		if err := s.Send(t.Context(), tsunami()); err != nil {
			t.Fatal(err)
		}

		reqs := h.requests()
		if !strings.Contains(contentOf(t, reqs[0]), "sent late") {
			t.Errorf("a late alert must not pass for a live one:\n%s", contentOf(t, reqs[0]))
		}
		// Late also covers a retry after the webhook was down, when the program was running all the time.
		if strings.Contains(contentOf(t, reqs[0]), "offline") {
			t.Errorf("a late alert must not say why it is late, because it cannot know:\n%s", contentOf(t, reqs[0]))
		}
		if strings.Contains(contentOf(t, reqs[1]), "late") {
			t.Errorf("an alert on time must not claim to be late:\n%s", contentOf(t, reqs[1]))
		}
	})

	t.Run("json", func(t *testing.T) {
		t.Parallel()
		h := newHook(t, accept)
		if err := notify.New(h.url, notify.FormatJSON, fast()).Send(t.Context(), late); err != nil {
			t.Fatal(err)
		}

		var got struct {
			Late bool `json:"late"`
		}
		if err := json.Unmarshal(h.requests()[0].body, &got); err != nil {
			t.Fatal(err)
		}
		if !got.Late {
			t.Error("late must be true in the structured message")
		}
	})
}

func TestAStatusMessageIsPostedAsItIsWithoutTheAlertWrapper(t *testing.T) {
	t.Parallel()
	const text = "japan-emergency-alerts is running.\nJMA feeds: OK"

	t.Run("discord", func(t *testing.T) {
		t.Parallel()
		h := newHook(t, accept)

		if err := notify.New(h.url, notify.FormatDiscord, fast()).Status(t.Context(), text); err != nil {
			t.Fatal(err)
		}

		if got := contentOf(t, h.requests()[0]); got != text {
			t.Errorf("content = %q, want the text exactly: a status line is the program's own, not JMA's", got)
		}
	})

	t.Run("json", func(t *testing.T) {
		t.Parallel()
		h := newHook(t, accept)

		if err := notify.New(h.url, notify.FormatJSON, fast()).Status(t.Context(), text); err != nil {
			t.Fatal(err)
		}

		var got struct {
			Status  bool   `json:"status"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(h.requests()[0].body, &got); err != nil {
			t.Fatal(err)
		}
		if !got.Status || got.Message != text {
			t.Errorf("got %+v, want status=true and the text", got)
		}
	})
}

func TestTheTestMessageSaysItIsNotAnAlert(t *testing.T) {
	t.Parallel()

	t.Run("discord", func(t *testing.T) {
		t.Parallel()
		h := newHook(t, accept)

		if err := notify.New(h.url, notify.FormatDiscord, fast()).Test(t.Context()); err != nil {
			t.Fatal(err)
		}

		content := contentOf(t, h.requests()[0])
		if !strings.Contains(content, "Test message") || !strings.Contains(content, "not an alert") {
			t.Errorf("content = %q, want a test message that says it is not an alert", content)
		}
	})

	t.Run("json", func(t *testing.T) {
		t.Parallel()
		h := newHook(t, accept)

		if err := notify.New(h.url, notify.FormatJSON, fast()).Test(t.Context()); err != nil {
			t.Fatal(err)
		}

		var got struct {
			Test    bool   `json:"test"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(h.requests()[0].body, &got); err != nil {
			t.Fatal(err)
		}
		if !got.Test || !strings.Contains(got.Message, "not an alert") {
			t.Errorf("got %+v, want test=true and a message saying it is not an alert", got)
		}
	})
}
