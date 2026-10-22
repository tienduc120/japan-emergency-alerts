// Command alerts relays major Japan emergency alerts to a webhook.
//
//	alerts         watch until stopped; opens a settings page in the browser the first time
//	alerts test    post one test message to the webhook and exit
//	alerts check   show what the last few days of JMA alerts would have done, and post nothing
package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/tienduc120/japan-emergency-alerts/internal/alert"
	"github.com/tienduc120/japan-emergency-alerts/internal/async"
	"github.com/tienduc120/japan-emergency-alerts/internal/clock"
	"github.com/tienduc120/japan-emergency-alerts/internal/config"
	"github.com/tienduc120/japan-emergency-alerts/internal/eew"
	"github.com/tienduc120/japan-emergency-alerts/internal/filter"
	"github.com/tienduc120/japan-emergency-alerts/internal/jma"
	"github.com/tienduc120/japan-emergency-alerts/internal/notify"
	"github.com/tienduc120/japan-emergency-alerts/internal/popup"
	"github.com/tienduc120/japan-emergency-alerts/internal/state"
	"github.com/tienduc120/japan-emergency-alerts/internal/web"
)

// version is the latest release. The release workflow replaces it with the tag through -ldflags "-X main.version=...",
// so a build made by hand, which has no tag to read, still says a real version and not "dev". Raise it when tagging.
var version = "v1.1.0"

// exitUsage is the conventional code for a command line that was wrong.
const exitUsage = 2

const (
	testCommand  = "test"
	checkCommand = "check"
)

// runner is anything that produces alerts until its context ends.
type runner interface {
	Run(ctx context.Context) error
}

// replayer is a runner that can also read the recent past once, for the check command.
type replayer interface {
	Replay(ctx context.Context) error
}

// horizoner is a source that knows how far back it has read everything, which is where the next run must catch up from.
type horizoner interface {
	Horizon() time.Time
}

// statuser is a source that can describe its own health in one line, for the heartbeat and the settings page.
type statuser interface {
	Status() string
}

// startupEntry is the start-with-Windows entry: whether Windows starts the program at sign-in, and which program.
type startupEntry interface {
	State() (bool, string, error)
	Enable(exe string) error
	Disable() error
}

// healther is a source that can also say whether it works, for the light beside its line on the settings page.
type healther interface {
	Health() alert.Health
}

// sourceConfig is what a source needs to start: where to hand alerts, where to log, and when the previous run last read
// everything (zero on a first run).
type sourceConfig struct {
	Handle alert.Handler
	Logger *slog.Logger
	Since  time.Time
}

// sourceFactory builds the alert sources. A test passes a fake, so nothing touches the network.
type sourceFactory func(sc sourceConfig) []runner

// liveSources are JMA's feeds, polled every minute, and the early-warning stream, which is pushed the moment a warning
// is issued. The stream has no history to catch up on: a warning that arrives late is worthless.
func liveSources(sc sourceConfig) []runner {
	return []runner{
		jma.NewPoller(jma.Config{Handle: sc.Handle, Logger: sc.Logger, CatchUpSince: sc.Since}),
		eew.NewStream(eew.Config{Handle: sc.Handle, Logger: sc.Logger, HistoryURL: eew.DefaultHistoryURL}),
	}
}

// environment is everything the program touches outside itself, so a test can replace it.
type environment struct {
	getenv   func(string) string
	readFile func(path string) ([]byte, error)
	// open shows a web address in the person's browser.
	open func(ctx context.Context, address string) error
	// listen opens the port for the settings page.
	listen func(ctx context.Context) (net.Listener, error)
	// popupShow shows one window; nil means the operating system's.
	popupShow popup.ShowFunc
	// startup is the start-with-Windows entry, nil where the system has none (all but Windows).
	startup startupEntry
	// executable is this program's path, which the start-with-Windows entry names.
	executable func() (string, error)
	sources    sourceFactory
	stdout     io.Writer
	stderr     io.Writer
}

func liveEnvironment(getenv func(string) string, stdout, stderr io.Writer) environment {
	return environment{
		getenv:     getenv,
		readFile:   os.ReadFile,
		open:       openBrowser,
		listen:     func(ctx context.Context) (net.Listener, error) { return listenPreferring(ctx, preferredAddress) },
		startup:    liveStartup(),
		executable: os.Executable,
		sources:    liveSources,
		stdout:     stdout,
		stderr:     stderr,
	}
}

func main() { os.Exit(runMain()) }

func runMain() int {
	tuneMemory(os.Getenv, runtime.GOMAXPROCS, debug.SetGCPercent)
	// Containers and service managers send SIGTERM, not an interrupt, before they kill a process.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	args := os.Args[1:]
	console, terminal := attachTerminal()
	stdout, stderr := io.Writer(os.Stdout), io.Writer(os.Stderr)
	if console != nil {
		stdout, stderr = console, console
	}
	var failure bytes.Buffer
	// The pop-up helper is left alone: it must not take over, and so empty, the log of the program that started it.
	if !terminal && !isPopupHelper(args) {
		log := openLogFile(os.Getenv)
		defer func() { _ = log.Close() }()
		stdout = log
		stderr = io.MultiWriter(log, &failure)
	}

	code := mainWith(ctx, args, os.Getenv, stdout, stderr)
	if holdsWindowOpen(args, code) {
		if terminal {
			waitForEnterIfDoubleClicked(stderr)
		} else {
			showFailure(failure.String())
		}
	}
	return code
}

func isPopupHelper(args []string) bool { return len(args) == 1 && args[0] == popup.WindowCommand }

// holdsWindowOpen says whether a person needs time to read the failure. The pop-up helper runs with nobody at its
// console, and its failure is reported by the program that started it.
func holdsWindowOpen(args []string, code int) bool {
	return code != 0 && !isPopupHelper(args)
}

// showFailure tells someone who started the program by double-clicking, with no terminal to print on, why it stopped.
func showFailure(text string) {
	if strings.TrimSpace(text) == "" {
		text = "The program stopped because of an error. The file log.txt beside the settings may say why."
	}
	_ = popup.ShowNow(context.Background(), "Japan Emergency Alerts", text)
}

func mainWith(ctx context.Context, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	return execute(ctx, args, liveEnvironment(getenv, stdout, stderr))
}

// execute runs one command of the program. Problems a person must fix go to stderr as plain sentences.
func execute(ctx context.Context, args []string, env environment) int {
	if isPopupHelper(args) {
		// Not for people: the program starts itself this way to show one window of its own on Windows.
		if err := popup.RunWindowCommand(os.Stdin); err != nil {
			fmt.Fprintf(env.stderr, "pop-up window: %v\n", err)
			return 1
		}
		return 0
	}
	command, ok := commandOf(args)
	if !ok {
		fmt.Fprintf(env.stderr, "usage: alerts [%s|%s]\n", testCommand, checkCommand)
		return exitUsage
	}
	if command == "" {
		return serve(ctx, env)
	}
	cfg, ok := loadConfig(env)
	if !ok {
		return exitUsage
	}
	if command == testCommand {
		return sendTest(ctx, cfg, env)
	}
	return check(ctx, cfg, env.stdout, env.stderr, env.sources)
}

// loadConfig is for the commands that cannot ask the person anything: they must say what is missing.
func loadConfig(env environment) (config.Config, bool) {
	loaded := config.Load(env.getenv, env.readFile)
	if loaded.Problems != nil {
		fmt.Fprintf(
			env.stderr,
			"invalid configuration:\n%v\nRun alerts with no command to set it up in your browser.\n",
			loaded.Problems,
		)
		return config.Config{}, false
	}
	return loaded.Config, true
}

// commandOf reads the command line: nothing means watch, and otherwise exactly one known command.
func commandOf(args []string) (string, bool) {
	switch {
	case len(args) == 0:
		return "", true
	case len(args) == 1 && (args[0] == testCommand || args[0] == checkCommand):
		return args[0], true
	default:
		return "", false
	}
}

// sendTest lets a user check each way of being told before relying on it.
func sendTest(ctx context.Context, cfg config.Config, env environment) int {
	code := 0
	if cfg.WebhookURL != nil {
		sender := notify.New(cfg.WebhookURL, notify.FormatFor(cfg.WebhookURL), notify.Options{})
		if err := sender.Test(ctx); err != nil {
			fmt.Fprintf(env.stderr, "test message failed: %v\n", err)
			code = 1
		} else {
			fmt.Fprintln(env.stdout, "test message sent: check your channel")
		}
	}
	if cfg.Popup {
		show := env.popupShow
		if show == nil {
			show = popup.ShowNow
		}
		title, text := popup.TestWindow()
		fmt.Fprintln(env.stdout, "showing a test pop-up: close it to finish")
		if err := show(ctx, title, text); err != nil {
			fmt.Fprintf(env.stderr, "test pop-up failed: %v\n", err)
			code = 1
		}
	}
	return code
}

// hooks let the program that runs the engine see into it: what became of each alert, and which sources were started.
type hooks struct {
	activity func(web.Activity)
	started  func(built []runner, sent *atomic.Int64)
	// popup is the window that shows alerts when the settings ask for pop-ups; nil makes a new one for this run.
	popup *popup.Window
}

// run is the engine without anything watching it, which is all most tests need.
func run(ctx context.Context, logger *slog.Logger, cfg config.Config, sources sourceFactory) error {
	return runWith(ctx, logger, cfg, sources, hooks{})
}

// runWith is the engine: it relays what its sources produce until ctx ends. It first restores what the previous run
// remembered, so a restart neither repeats an alert nor misses what happened while the program was off.
func runWith(ctx context.Context, logger *slog.Logger, cfg config.Config, sources sourceFactory, h hooks) error {
	logger.InfoContext(ctx, "started", "config", cfg)
	store, err := state.Open(cfg.StateFile)
	if err != nil {
		logger.WarnContext(ctx, "history could not be read; starting without it", "error", err)
	}
	flt := filter.New(cfg.Location, cfg.MinIntensity, clock.System{})
	for id, at := range store.Sent() {
		flt.Remember(id, at)
	}
	var tasks []func(context.Context) error
	var channels multiSender
	channels.logger = logger
	// The window goes first: it only queues, while a webhook that is down can take half a minute to give up.
	if cfg.Popup {
		window := h.popup
		if window == nil {
			window = popup.New(popup.Options{Logger: logger})
			tasks = append(tasks, window.Run)
		}
		channels.senders = append(channels.senders, window)
	}
	var webhook *notify.Sender
	if cfg.WebhookURL != nil {
		webhook = notify.New(cfg.WebhookURL, notify.FormatFor(cfg.WebhookURL), notify.Options{})
		channels.senders = append(channels.senders, webhook)
	}
	r := &relay{filter: flt, sender: channels, logger: logger, record: recordSent(store, logger), activity: h.activity}
	started := clock.System{}.Now()

	built := sources(sourceConfig{Handle: r.handle, Logger: logger, Since: store.Horizon()})
	if h.started != nil {
		h.started(built, &r.sent)
	}
	for _, source := range built {
		tasks = append(tasks, source.Run)
	}
	tasks = append(tasks, keepHistory(store, built, logger))
	// The heartbeat is a message to the webhook; a pop-up that nobody asked for every few hours would only be a nuisance.
	if cfg.HeartbeatEvery > 0 && webhook != nil {
		report := func() string { return statusText(cfg, started, r.sent.Load(), built) }
		tasks = append(tasks, heartbeat(cfg.HeartbeatEvery, webhook, report, logger))
	}

	err = async.Run(ctx, tasks...)
	logger.InfoContext(context.WithoutCancel(ctx), "stopping")
	return err
}
