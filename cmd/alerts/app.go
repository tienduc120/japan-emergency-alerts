package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/tienduc120/japan-emergency-alerts/internal/async"
	"github.com/tienduc120/japan-emergency-alerts/internal/config"
	"github.com/tienduc120/japan-emergency-alerts/internal/notify"
	"github.com/tienduc120/japan-emergency-alerts/internal/popup"
	"github.com/tienduc120/japan-emergency-alerts/internal/web"
)

const maxActivity = 30

// activityLog keeps the last few things that happened, newest first, for the settings page.
type activityLog struct {
	mu    sync.Mutex
	items []web.Activity
}

func (l *activityLog) add(a web.Activity) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.items = append([]web.Activity{a}, l.items...)
	if len(l.items) <= maxActivity {
		return
	}
	// Alerts for elsewhere come several a day, so the oldest of them makes room first, or the person's own would be
	// pushed out within days. The newest is always kept.
	drop := len(l.items) - 1
	for i := drop; i > 0; i-- {
		if l.items[i].Elsewhere {
			drop = i
			break
		}
	}
	l.items = slices.Delete(l.items, drop, drop+1)
}

func (l *activityLog) list() []web.Activity {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]web.Activity(nil), l.items...)
}

// app is the program as the settings page sees it: it watches while the settings allow, and watches again with the new
// ones whenever the page saves them.
type app struct {
	env      environment
	logger   *slog.Logger
	activity *activityLog
	// window shows the pop-ups. It lives as long as the program, so the test button works before anything is saved.
	window *popup.Window
	// restart is told when new settings were saved. One pending message is enough, however many saves came.
	restart chan struct{}
	// stop ends the whole program, for the page's Stop button. It is nil until serve sets it.
	stop context.CancelFunc

	mu      sync.Mutex
	loaded  config.Loaded
	running bool
	cfg     config.Config
	built   []runner
}

func newApp(env environment, logger *slog.Logger) *app {
	return &app{
		env: env, logger: logger, activity: &activityLog{}, restart: make(chan struct{}, 1),
		window: popup.New(popup.Options{Show: env.popupShow, Logger: logger}),
	}
}

// reload reads the settings again and remembers them.
func (a *app) reload() config.Loaded {
	loaded := config.Load(a.env.getenv, a.env.readFile)
	a.mu.Lock()
	a.loaded = loaded
	a.mu.Unlock()
	return loaded
}

// supervise watches with the current settings, and starts again whenever new ones are saved, until ctx ends. With
// settings that cannot be used it waits for the page to supply good ones.
func (a *app) supervise(ctx context.Context) error {
	for ctx.Err() == nil {
		loaded := a.reload()
		if loaded.Problems != nil {
			select {
			case <-a.restart:
				continue
			case <-ctx.Done():
				return nil
			}
		}
		restarted, err := a.watch(ctx, loaded.Config)
		if err != nil {
			return err
		}
		if !restarted {
			return nil
		}
		a.logger.InfoContext(ctx, "settings changed; watching again")
	}
	return nil
}

// watch runs the engine until ctx ends or new settings arrive, and says which of the two it was.
func (a *app) watch(ctx context.Context, cfg config.Config) (bool, error) {
	engineCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	a.setRunning(cfg, true)
	defer a.setRunning(cfg, false)

	restarted := false
	err := async.Run(ctx,
		func(context.Context) error {
			defer cancel()
			return runWith(
				engineCtx,
				a.logger,
				cfg,
				a.env.sources,
				hooks{activity: a.activity.add, started: a.attach, popup: a.window},
			)
		},
		func(c context.Context) error {
			select {
			case <-a.restart:
				restarted = true
				cancel()
			case <-engineCtx.Done():
			case <-c.Done():
			}
			return nil
		},
	)
	return restarted && ctx.Err() == nil, err
}

func (a *app) setRunning(cfg config.Config, running bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.running = running
	a.cfg = cfg
	if !running {
		a.built = nil
	}
}

// attach is told which sources the engine started, so the page can show their health.
func (a *app) attach(built []runner, _ *atomic.Int64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.built = built
}

// Snapshot is what the settings page shows.
func (a *app) Snapshot() web.Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	snap := web.Snapshot{
		Saved:        a.loaded.File,
		Notes:        a.notes(),
		PopupDefault: runtime.GOOS == "windows" || runtime.GOOS == "darwin",
		Running:      a.running,
		Recent:       a.activity.list(),
	}
	if a.running {
		snap.Summary = summary(a.cfg)
		snap.Sources = sourceLines(a.built)
	}
	if a.env.startup != nil {
		snap.StartupOffered = true
		// An entry that cannot be read shows as off; saving with the box ticked writes it again.
		snap.StartupOn, _, _ = a.env.startup.State()
	}
	return snap
}

// sourceLines are the sources' health lines, each with the colour of its light when the source can tell.
func sourceLines(built []runner) []web.Source {
	var lines []web.Source
	for _, source := range built {
		s, ok := source.(statuser)
		if !ok {
			continue
		}
		line := web.Source{Text: s.Status()}
		if line.Text == "" {
			continue
		}
		if h, known := source.(healther); known {
			line.State = h.Health()
		}
		lines = append(lines, line)
	}
	return lines
}

// notes are things the person should know before they change anything. The caller holds the lock.
func (a *app) notes() []string {
	names := map[string]string{
		config.EnvWebhookURL:   "The webhook address",
		config.EnvLocation:     "The location",
		config.EnvMinIntensity: "The minimum earthquake strength",
	}
	var notes []string
	for _, key := range a.loaded.Overridden {
		notes = append(notes, fmt.Sprintf(
			"%s is set by the environment variable %s, which overrides this page. / 環境変数 %s が優先されるため、このページでは変更できません。",
			names[key], key, key))
	}
	if a.loaded.FileErr != nil {
		notes = append(notes, a.loaded.FileErr.Error())
	}
	if !a.running {
		for _, p := range config.Problems(a.loaded.Problems) {
			// A missing webhook is simply a first run, which the page already explains.
			if p.Field == config.EnvWebhookURL && strings.HasPrefix(p.Problem, "is required") {
				continue
			}
			notes = append(notes, p.Error())
		}
	}
	return notes
}

// Save keeps the settings and starts watching with them. Nothing is kept unless every setting passes.
func (a *app) Save(ctx context.Context, f config.File, startWithWindows bool) error {
	if err := config.Check(a.env.getenv, f); err != nil {
		return err
	}
	if err := a.applyStartup(startWithWindows); err != nil {
		return err
	}
	a.mu.Lock()
	path := a.loaded.Path
	a.mu.Unlock()
	if err := config.Save(path, f); err != nil {
		return err
	}
	select {
	case a.restart <- struct{}{}:
	default:
	}
	a.logger.InfoContext(ctx, "settings saved", "file", path)
	return nil
}

// applyStartup turns the start-with-Windows entry on or off. On Windows ticking it always rewrites it, which also
// clears Task Manager's mark when someone had turned it off there.
func (a *app) applyStartup(on bool) error {
	if a.env.startup == nil {
		return nil
	}
	var err error
	if on {
		var exe string
		if exe, err = a.env.executable(); err == nil {
			err = a.env.startup.Enable(exe)
		}
	} else {
		err = a.env.startup.Disable()
	}
	if err != nil {
		return fmt.Errorf("start with Windows could not be set: %w", err)
	}
	return nil
}

// Quit stops the program.
func (a *app) Quit() {
	if a.stop != nil {
		a.stop()
	}
}

// Test posts a test message to the webhook in the box. The box shows the saved address, so an empty one has none, except
// that an address from the environment is never in the box and is still the one in use.
func (a *app) Test(ctx context.Context, webhook string) error {
	address := strings.TrimSpace(webhook)
	if address == "" {
		address = strings.TrimSpace(a.env.getenv(config.EnvWebhookURL))
	}
	if address == "" {
		return errors.New("no webhook address yet: paste one first / 先に Webhook のアドレスを貼り付けてください")
	}
	u, err := config.ParseWebhook(address)
	if err != nil {
		return errors.New("that is not a usable address: it must start with https:// / https:// で始まるアドレスを入力してください")
	}
	return notify.New(u, notify.FormatFor(u), notify.Options{}).Test(ctx)
}

// TestPopup shows a test window on this computer.
func (a *app) TestPopup(ctx context.Context) error {
	if err := a.window.Test(ctx); err != nil {
		if errors.Is(err, popup.ErrBusy) {
			return errors.New(
				"too many pop-ups are still open: close some and try again / 開いているポップアップを閉じてからもう一度お試しください",
			)
		}
		return err
	}
	return nil
}

func summary(cfg config.Config) string {
	return fmt.Sprintf("Watching %s from intensity %s\n監視中：%s・震度%s以上",
		cfg.Location, cfg.MinIntensity.Japanese(), cfg.Location, cfg.MinIntensity.Japanese())
}
