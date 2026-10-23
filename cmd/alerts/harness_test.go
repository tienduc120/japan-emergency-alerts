package main

import (
	"context"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// testEnvironment replaces everything outside the program: no browser opens, no fixed port is used, and the settings
// file is read from wherever the test's getenv says.
func testEnvironment(getenv func(string) string, stdout, stderr io.Writer, sources sourceFactory) environment {
	return environment{
		getenv:   getenv,
		readFile: os.ReadFile,
		open:     func(context.Context, string) error { return nil },
		listen: func(ctx context.Context) (net.Listener, error) {
			var lc net.ListenConfig
			return lc.Listen(ctx, "tcp", "127.0.0.1:0")
		},
		sources: sources,
		stdout:  stdout,
		stderr:  stderr,
	}
}

var settingsAddress = regexp.MustCompile(`Settings page: (http://127\.0\.0\.1:\d+/)\s`)

// harness runs the whole program in the background against a temporary settings file.
type harness struct {
	t          *testing.T
	stdout     syncBuffer
	stderr     syncBuffer
	opened     chan string
	windows    chan [2]string
	configPath string
	statePath  string
	env        environment
}

func newHarness(t *testing.T, settings map[string]string, sources sourceFactory) *harness {
	t.Helper()
	h := &harness{
		t:          t,
		opened:     make(chan string, 4),
		windows:    make(chan [2]string, 8),
		configPath: filepath.Join(t.TempDir(), "config.json"),
		statePath:  filepath.Join(t.TempDir(), "state.json"),
	}
	values := map[string]string{"CONFIG_FILE": h.configPath, "STATE_FILE": h.statePath}
	maps.Copy(values, settings)
	h.env = testEnvironment(func(k string) string { return values[k] }, &h.stdout, &h.stderr, sources)
	h.env.open = func(_ context.Context, address string) error {
		h.opened <- address
		return nil
	}
	h.env.popupShow = func(_ context.Context, title, text string) error {
		h.windows <- [2]string{title, text}
		return nil
	}
	return h
}

const fakeExe = `C:\apps\alerts.exe`

// fakeStartup stands for the start-with-Windows entry, so no test touches the registry. Setting one on a harness also
// makes the program behave as on Windows.
type fakeStartup struct {
	mu        sync.Mutex
	on        bool
	exe       string
	enableErr error
	enabled   []string
	disabled  int
}

func (f *fakeStartup) State() (bool, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.on, f.exe, nil
}

func (f *fakeStartup) Enable(exe string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.enableErr != nil {
		return f.enableErr
	}
	f.on, f.exe = true, exe
	f.enabled = append(f.enabled, exe)
	return nil
}

func (f *fakeStartup) Disable() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.on = false
	f.disabled++
	return nil
}

func (f *fakeStartup) calls() ([]string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.enabled...), f.disabled
}

// onWindows gives the harness a start-with-Windows entry and a path for the program.
func (h *harness) onWindows(entry *fakeStartup) {
	h.env.startup = entry
	h.env.executable = func() (string, error) { return fakeExe, nil }
}

// start runs the program until ctx ends and delivers its exit code.
func (h *harness) start(ctx context.Context) <-chan int { return h.startWith(ctx, nil) }

// startWith is start with a command line, such as the marker the start-with-Windows entry adds.
func (h *harness) startWith(ctx context.Context, args []string) <-chan int {
	done := make(chan int, 1)
	go func() { done <- execute(ctx, args, h.env) }()
	return done
}

// address waits for the program to print its settings address, and returns it.
func (h *harness) address() string {
	h.t.Helper()
	deadline := time.After(waitLimit)
	for {
		if m := settingsAddress.FindStringSubmatch(h.stdout.String()); m != nil {
			return m[1]
		}
		select {
		case <-deadline:
			h.t.Fatalf("the program never printed its settings address:\n%s\n%s", h.stdout.String(), h.stderr.String())
		case <-time.After(2 * time.Millisecond):
		}
	}
}

// browser is a person at the settings page: it says where its requests come from, as a browser does.
type browser struct {
	t      *testing.T
	client *http.Client
	base   string
}

func newBrowser(t *testing.T, address string) *browser {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	return &browser{
		t: t,
		client: &http.Client{
			Jar:       jar,
			Timeout:   waitLimit,
			Transport: &http.Transport{DisableKeepAlives: true},
			// Stop at redirects, so a test can see what the server answered.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		base: u.Scheme + "://" + u.Host,
	}
}

// signIn opens the address the program printed, as a person does.
func (b *browser) signIn(address string) (int, string) {
	b.t.Helper()
	u, err := url.Parse(address)
	if err != nil {
		b.t.Fatal(err)
	}
	return b.get(u.RequestURI())
}

// get follows one redirect, as a browser does.
func (b *browser) get(path string) (int, string) {
	b.t.Helper()
	status, body := b.request(http.MethodGet, path, nil)
	if status == http.StatusSeeOther {
		return b.request(http.MethodGet, "/", nil)
	}
	return status, body
}

func (b *browser) post(path string, form url.Values) (int, string) {
	b.t.Helper()
	return b.request(http.MethodPost, path, form)
}

func (b *browser) request(method, path string, form url.Values) (int, string) {
	b.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(b.t.Context(), method, b.base+path, body)
	if err != nil {
		b.t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", b.base)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		b.t.Fatal(err)
	}
	return resp.StatusCode, string(data)
}

// window waits for a pop-up window to be shown and returns its title and text.
func (h *harness) window() [2]string {
	h.t.Helper()
	select {
	case w := <-h.windows:
		return w
	case <-time.After(waitLimit):
		h.t.Fatal("no pop-up window was shown")
		return [2]string{}
	}
}

// stopped asks the program to stop and returns its exit code.
func stopped(t *testing.T, cancel context.CancelFunc, done <-chan int) int {
	t.Helper()
	cancel()
	select {
	case code := <-done:
		return code
	case <-time.After(waitLimit):
		t.Fatal("the program did not stop")
		return -1
	}
}
