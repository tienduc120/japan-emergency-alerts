// Package web is the settings page: a small, bilingual page served on this computer only, where a person pastes a
// webhook, ticks the prefectures they are in, and sees what the program has been doing.
//
// Any web page open in the person's browser can try to reach a local server, so the Host header must be one of ours
// (against DNS rebinding) and every change must come from this page's own origin. There is no key: the maintainer
// chose to open the page from its plain address, which also lets any program or account on this computer open it.
package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
	"github.com/tienduc120/japan-emergency-alerts/internal/config"
)

// ServerName is sent with every answer, so a second copy of the program that finds the settings port taken can tell
// whether the owner is another copy of this program.
const ServerName = "japan-emergency-alerts"

const (
	againParam   = "again"
	nonceBytes   = 16
	maxFormBytes = 1 << 16
	jstOffset    = 9 * 60 * 60
	timeLayout   = "2006-01-02 15:04"
)

// Activity is one thing the program did with an alert, for the "Recent activity" list.
type Activity struct {
	When   time.Time
	Label  string
	Title  string
	Text   string
	Result string
	Sent   bool
	// Elsewhere marks an alert for a place that is not watched. There are many and they are about someone else, so the
	// list shows them last.
	Elsewhere bool
}

// Source is one line about the health of a feed, and the colour of its light.
type Source struct {
	Text  string       `json:"text"`
	State alert.Health `json:"state"`
}

// Snapshot is what the page shows, taken at one moment.
type Snapshot struct {
	// Saved is the settings file's content. The webhook in it is never shown in full.
	Saved config.File
	// Notes are things the person should know, such as a setting the environment overrides.
	Notes []string
	// PopupDefault says whether the pop-up box starts ticked when nothing has been saved yet, which suits a computer
	// with a screen in front of someone, and not a server.
	PopupDefault bool
	Running      bool
	Summary      string
	// Sources are one line each about the health of the feeds.
	Sources []Source
	// Recent is newest first.
	Recent []Activity
}

// Settings is what the page needs from the program.
type Settings interface {
	Snapshot() Snapshot
	// Save checks and keeps the settings and starts watching with them. Problems with a setting come back as
	// config.FieldError values, joined.
	Save(ctx context.Context, f config.File) error
	// Test sends a test message to the webhook. An empty webhook means the saved one.
	Test(ctx context.Context, webhook string) error
	// TestPopup shows a test pop-up window on this computer.
	TestPopup(ctx context.Context) error
	// Quit stops the program. The program has no window of its own on Windows, so the page is where a person stops it.
	Quit()
}

// Options say how the server identifies itself and who may talk to it.
type Options struct {
	// Hosts are the Host header values that are ours, such as "127.0.0.1:47600".
	Hosts   []string
	Version string
}

type server struct {
	app     Settings
	hosts   []string
	version string
	tmpl    *template.Template
}

// New returns the page's handler.
func New(app Settings, opts Options) http.Handler {
	return &server{
		app: app, hosts: opts.Hosts, version: opts.Version,
		// The page is built into the program, so a mistake in it shows at start-up in every test, not on a visitor.
		tmpl: template.Must(template.New("page").Parse(pageSource)),
	}
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setHeaders(w, "")
	if !slices.Contains(s.hosts, r.Host) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	switch r.URL.Path {
	case "/favicon.ico":
		w.Header().Set("Content-Type", "image/x-icon")
		_, _ = w.Write(faviconData)
	case "/":
		s.page(w, r)
	case "/status.json":
		s.status(w, r)
	case "/save":
		s.post(w, r, s.save)
	case "/test":
		s.post(w, r, s.test)
	case "/test-popup":
		s.post(w, r, s.testPopup)
	case "/quit":
		s.post(w, r, s.quit)
	default:
		http.NotFound(w, r)
	}
}

// post runs a handler that changes something, after checking that the request came from this page and not from
// another site.
func (s *server) post(w http.ResponseWriter, r *http.Request, handle func(http.ResponseWriter, *http.Request)) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Header.Get("Origin") != "http://"+r.Host {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBytes)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	handle(w, r)
}

func (s *server) test(w http.ResponseWriter, r *http.Request) {
	typed := strings.TrimSpace(r.PostForm.Get("webhook"))
	reply := struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}{OK: true, Message: "Test message sent. Check your channel. / テストを送信しました。チャンネルを確認してください。"}
	if err := s.app.Test(r.Context(), typed); err != nil {
		reply.OK, reply.Message = false, err.Error()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(reply)
}

func (s *server) testPopup(w http.ResponseWriter, r *http.Request) {
	reply := struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}{OK: true, Message: "A test pop-up was sent to your screen. / テストのポップアップを表示しました。"}
	if err := s.app.TestPopup(r.Context()); err != nil {
		reply.OK, reply.Message = false, err.Error()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(reply)
}

// quit stops the program and answers first, so the person is told rather than left with a broken connection.
func (s *server) quit(w http.ResponseWriter, _ *http.Request) {
	s.app.Quit()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>Stopped / 停止しました</title>` +
		`<body style="font:16px/1.6 system-ui,sans-serif;margin:2rem;background:#fff;color:#1a1a1a">` +
		`<p>The program has stopped. You can close this tab. To start it again, open the program.</p>` +
		`<p>プログラムを停止しました。このタブを閉じてください。もう一度起動するには、プログラムを開いてください。</p></body>`))
}

func (s *server) status(w http.ResponseWriter, _ *http.Request) {
	snap := s.app.Snapshot()
	type item struct {
		When   string `json:"when"`
		Label  string `json:"label"`
		Title  string `json:"title"`
		Text   string `json:"text"`
		Result string `json:"result"`
		Sent   bool   `json:"sent"`
	}
	reply := struct {
		Running bool     `json:"running"`
		Summary string   `json:"summary"`
		Sources []Source `json:"sources"`
		Recent  []item   `json:"recent"`
	}{Running: snap.Running, Summary: snap.Summary, Sources: nonNil(snap.Sources), Recent: []item{}}
	for _, a := range listed(snap.Recent) {
		reply.Recent = append(reply.Recent, item{
			When: inJST(a.When), Label: a.Label, Title: a.Title, Text: a.Text, Result: a.Result, Sent: a.Sent,
		})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(reply)
}

// listed is the order the page shows activity in: as it came, newest first, with the alerts for elsewhere moved last.
func listed(recent []Activity) []Activity {
	out := slices.Clone(recent)
	slices.SortStableFunc(out, func(a, b Activity) int {
		switch {
		case a.Elsewhere == b.Elsewhere:
			return 0
		case b.Elsewhere:
			return -1
		default:
			return 1
		}
	})
	return out
}

func nonNil[T any](list []T) []T {
	if list == nil {
		return []T{}
	}
	return list
}

func inJST(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.In(time.FixedZone("JST", jstOffset)).Format(timeLayout) + " JST"
}

// setHeaders tells the browser not to keep, embed or guess at the page. nonce, when given, allows the page's one script.
func setHeaders(w http.ResponseWriter, nonce string) {
	script := ""
	if nonce != "" {
		script = "script-src 'nonce-" + nonce + "'; "
	}
	h := w.Header()
	h.Set("Server", ServerName)
	h.Set("Content-Security-Policy", "default-src 'none'; "+script+
		"style-src 'unsafe-inline'; img-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Cache-Control", "no-store")
	// Not no-referrer: with that policy a browser sends "Origin: null" when the page's own form is posted, and the check
	// on POST would refuse every save. same-origin still tells other sites nothing.
	h.Set("Referrer-Policy", "same-origin")
}

func nonce() string {
	b := make([]byte, nonceBytes)
	if _, err := rand.Read(b); err != nil {
		panic("no randomness available: " + err.Error())
	}
	return base64.StdEncoding.EncodeToString(b)
}
