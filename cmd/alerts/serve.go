package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/async"
	"github.com/tienduc120/japan-emergency-alerts/internal/config"
	"github.com/tienduc120/japan-emergency-alerts/internal/fileutil"
	"github.com/tienduc120/japan-emergency-alerts/internal/web"
)

const (
	// preferredAddress is where the settings page is served when it is free, so the address looks the same each time.
	preferredAddress = "127.0.0.1:47600"
	shutdownGrace    = 3 * time.Second
	headerTimeout    = 5 * time.Second
	readTimeout      = 10 * time.Second
	writeTimeout     = 30 * time.Second
	keyBytes         = 16
	// probeTimeout is long for a program on the same computer, so a copy that is merely busy is still recognized.
	probeTimeout = 2 * time.Second
)

// errAlreadyRunning means the settings port belongs to another copy of this program.
var errAlreadyRunning = errors.New("another copy of this program is already running")

// serve is the default command: it serves the settings page and watches whenever the settings allow. The first time,
// when there is no webhook yet, it opens the page in the browser.
func serve(ctx context.Context, env environment) int {
	logger := slog.New(slog.NewTextHandler(env.stdout, nil))
	a := newApp(env, logger)

	listener, err := env.listen(ctx)
	if errors.Is(err, errAlreadyRunning) {
		return showRunningCopy(ctx, env)
	}
	if err != nil {
		fmt.Fprintf(env.stderr, "could not start the settings page: %v\n", err)
		return 1
	}
	key := randomKey()
	address := listener.Addr().String()
	_, port, _ := net.SplitHostPort(address)
	pageURL := "http://" + address + "/?key=" + key
	srv := &http.Server{
		Handler: web.New(a, web.Options{
			Key: key, Hosts: []string{address, net.JoinHostPort("localhost", port)}, Version: version,
		}),
		ReadHeaderTimeout: headerTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
	}

	if path := config.AddressFilePath(env.getenv); path != "" {
		if writeErr := fileutil.WriteAtomic(path, []byte(pageURL+"\n")); writeErr != nil {
			logger.WarnContext(ctx, "the settings address could not be kept for a second copy", "error", writeErr)
		} else {
			// The address carries the key to the settings page, so it goes when the program does.
			defer func() { _ = os.Remove(path) }()
		}
	}

	fmt.Fprintf(env.stdout, "Japan Emergency Alerts %s\nSettings page: %s\n", version, pageURL)
	if loaded := a.reload(); loaded.Problems != nil {
		fmt.Fprintln(env.stdout, "Not set up yet. Opening the settings page in your browser. Leave this window open.")
		if openErr := env.open(ctx, pageURL); openErr != nil {
			fmt.Fprintf(
				env.stdout,
				"The browser could not be opened (%v). Copy the address above into your browser.\n",
				openErr,
			)
		}
	} else {
		fmt.Fprintf(
			env.stdout,
			"Watching %s (earthquake reports from intensity %s). Leave this window open; close it to stop.\n",
			loaded.Config.Location,
			loaded.Config.MinIntensity.Japanese(),
		)
	}

	if err = async.Run(ctx, a.supervise, serveHTTP(srv, listener), a.window.Run); err != nil {
		fmt.Fprintf(env.stderr, "stopped: %v\n", err)
		return 1
	}
	return 0
}

// showRunningCopy is what a second copy does instead of watching: it opens the first copy's settings page when it can
// find the address, and otherwise says where to look. Two copies would each send every alert.
func showRunningCopy(ctx context.Context, env environment) int {
	if address, ok := runningCopyAddress(env); ok && env.open(ctx, address) == nil {
		fmt.Fprint(
			env.stdout,
			"Japan Emergency Alerts is already running on this computer. Its settings page is open in your browser.\n"+
				"日本の緊急情報アラートはすでに起動しています。設定ページをブラウザで開きました。\n",
		)
		return 0
	}
	fmt.Fprint(
		env.stderr,
		"Japan Emergency Alerts is already running on this computer. Use its window (look in the taskbar): "+
			"it shows the settings address.\n日本の緊急情報アラートはすでに起動しています。起動済みのウィンドウをご確認ください。\n",
	)
	return 1
}

// runningCopyAddress reads the settings address the first copy left. Only this computer's own settings page is ever
// opened, whatever the file says.
func runningCopyAddress(env environment) (string, bool) {
	path := config.AddressFilePath(env.getenv)
	if path == "" {
		return "", false
	}
	data, err := env.readFile(path)
	if err != nil {
		return "", false
	}
	address := strings.TrimSpace(string(data))
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Query().Get("key") == "" {
		return "", false
	}
	return address, true
}

// serveHTTP runs the page's server until ctx ends, then lets requests in progress finish.
func serveHTTP(srv *http.Server, listener net.Listener) func(context.Context) error {
	return func(ctx context.Context) error {
		done := make(chan struct{})
		// AfterFunc starts the shutdown without a bare go statement; done lets this task wait for it.
		stop := context.AfterFunc(ctx, func() {
			defer close(done)
			shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
		})
		defer stop()

		err := srv.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			<-done
			return nil
		}
		return err
	}
}

// listenPreferring opens the preferred local address. When another copy of this program holds it, it says so with
// errAlreadyRunning; when some other program holds it, it takes any free local port instead. Only the local computer
// can ever connect.
func listenPreferring(ctx context.Context, preferred string) (net.Listener, error) {
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", preferred)
	if err == nil {
		return listener, nil
	}
	if isThisProgram(ctx, preferred) {
		return nil, errAlreadyRunning
	}
	host, _, splitErr := net.SplitHostPort(preferred)
	if splitErr != nil {
		return nil, splitErr
	}
	return lc.Listen(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(0)))
}

// isThisProgram asks whoever answers at address whether it is a copy of this program, by the name every answer of its
// settings page carries. Silence, or any other answer, means it is not.
func isThisProgram(ctx context.Context, address string) bool {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/", nil)
	if err != nil {
		return false
	}
	// No proxy is set on this transport: the address is on this computer.
	client := &http.Client{
		Transport:     &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.Header.Get("Server") == web.ServerName
}

func randomKey() string {
	b := make([]byte, keyBytes)
	if _, err := rand.Read(b); err != nil {
		panic("no randomness available: " + err.Error())
	}
	return hex.EncodeToString(b)
}
