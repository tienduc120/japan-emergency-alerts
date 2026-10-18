// Package web is the settings page: a small, bilingual page served on this computer only, where a person pastes a
// webhook, ticks the prefectures they are in, and sees what the program has been doing.
//
// It is protected because anything on the machine, and any web page open in the person's browser, can try to reach a
// local server. The address carries a secret key that is swapped for a cookie, the cookie is SameSite=Strict, the
// Host header must be one of ours (against DNS rebinding), and every change must come from this page's own origin.
package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/config"
)

// ServerName is sent with every answer, so a second copy of the program that finds the settings port taken can tell
// whether the owner is another copy of this program.
const ServerName = "japan-emergency-alerts"

const (
	cookieName   = "session"
	sessionBytes = 16
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
	Sources []string
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
}

// Options say how the server identifies itself and who may talk to it.
type Options struct {
	// Key is the secret in the address the person opens. It stays valid while the program runs, because the cookie it
	// is swapped for ends with the browser.
	Key string
	// Hosts are the Host header values that are ours, such as "127.0.0.1:47600".
	Hosts   []string
	Version string
}

type server struct {
	app     Settings
	key     string
	hosts   []string
	version string
	session string
	tmpl    *template.Template
}

// New returns the page's handler.
func New(app Settings, opts Options) http.Handler {
	return &server{
		app: app, key: opts.Key, hosts: opts.Hosts, version: opts.Version, session: random(sessionBytes),
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
	if r.URL.Path == "/favicon.ico" && r.Method == http.MethodGet {
		// A browser asks for the icon without the page's cookie, and the icon is not a secret.
		w.Header().Set("Content-Type", "image/x-icon")
		_, _ = w.Write(faviconData)
		return
	}
	if r.URL.Path == "/" && r.Method == http.MethodGet && r.URL.Query().Has("key") {
		s.login(w, r)
		return
	}
	if !s.authenticated(r) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("Open the settings address shown in the program's window; it carries a key.\n" +
			"プログラムのウィンドウに表示されているアドレスを開いてください。\n"))
		return
	}

	switch r.URL.Path {
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
	default:
		http.NotFound(w, r)
	}
}

func (s *server) login(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("key")), []byte(s.key)) != 1 {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	http.SetCookie(
		w,
		&http.Cookie{ //nolint:gosec // plain http on loopback: browsers that refuse Secure there would drop it
			Name: cookieName, Value: s.session, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode,
		},
	)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) authenticated(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	return err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.session)) == 1
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
		Sources []string `json:"sources"`
		Recent  []item   `json:"recent"`
	}{Running: snap.Running, Summary: snap.Summary, Sources: nonNil(snap.Sources), Recent: []item{}}
	for _, a := range snap.Recent {
		reply.Recent = append(reply.Recent, item{
			When: inJST(a.When), Label: a.Label, Title: a.Title, Text: a.Text, Result: a.Result, Sent: a.Sent,
		})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(reply)
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
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

func random(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("no randomness available: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func nonce() string {
	b := make([]byte, nonceBytes)
	if _, err := rand.Read(b); err != nil {
		panic("no randomness available: " + err.Error())
	}
	return base64.StdEncoding.EncodeToString(b)
}
