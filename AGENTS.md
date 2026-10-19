# japan-emergency-alerts

A small Go program that listens to free Japan disaster-alert sources and forwards the major alerts to a webhook, for travelers who do not receive Japan's cell-broadcast alerts. It must stay very small in memory and CPU, and easy for anyone to run.

## Status

Steps 1 to 7 of the build order are done: the module and gate, `internal/async`, `internal/clock`, the configuration (`internal/config`, `internal/region`, `internal/intensity`), the alert type and the filter (`internal/alert`, `internal/filter`), the webhook sender (`internal/notify`), the JMA feed poller with catch-up (`internal/jma`), the earthquake early-warning stream (`internal/eew`), the history file (`internal/state`), and a `main` that wires them together with an optional heartbeat (off by default) and has `alerts test` and `alerts check` commands. A release is made by pushing a version tag. The release workflow takes the version from the tag; `version` in `cmd/alerts/main.go` is the default for a build made by hand, so raise it when you tag a new release. Step 8 (easy for non-technical people) is done: a settings file and a local settings page (`internal/web`), a desktop pop-up as a second way to be told (`internal/popup`), the browser opening by itself, and the README quick start. Do not report a rule as enforced unless a tool in this repository checks it. The working plan is in `.local/PLAN.md` (gitignored), which is where scope, sources, build order, and open items live.

## How to work

- **Test first, and run the tests.** Write the failing test, run that package's tests and see it fail for the right reason, make it pass, run them again. That is the only checking you do while working.
- **Do not run lint, format, vet, or a review between steps.** The pre-commit hook runs them and CI runs them again; a rejected commit tells you what to fix. Open the details only when a gate reports a failure.
- Follow the `writing-go-tests` and `handling-errors` skills in `.agents/skills/`. Claude Code reads them through a git-ignored link: `New-Item -ItemType Junction -Path .claude\skills -Target .agents\skills` (PowerShell, once per clone).

## Commands

| Need | Command |
| --- | --- |
| Tests | `go test -race -timeout 120s -count=1 ./...` |
| Lint and format check | `golangci-lint run ./...` (the pre-commit hook does it) |
| Install the git hooks | `lefthook install` (once per clone) |
| Vulnerabilities | `govulncheck ./...` (the pre-push hook and CI do it) |

## Hard rules

- **Start goroutines only through `internal/async`** (`async.Run`): it cancels, waits, and turns a panic into an error.
- **A package that starts goroutines checks for leaks.** Its `TestMain` calls `goleak.VerifyTestMain(m)`. Never `time.Sleep` in code or tests; wait on a timer, a channel, or the context, and use `testing/synctest` for time.
- **Time comes from `internal/clock`**, so a test can fix it.
- **Every network call has a context and an explicit timeout.** No `http.Get`, `http.DefaultClient`, or `context.Background` outside `main`.
- **Log through `log/slog`**, never `log`. `fmt.Fprint*` is for the lines a person must read at the console (start-up lines, a problem to fix) and goes to the injected stdout or stderr. The log is plain text, not JSON, because a person reads it. Never log a webhook URL.
- **The settings page is only for this computer.** Keep the loopback listener, the secret key and cookie, the Host allow-list, the Origin check on POST and the CSP with a nonce together; weakening one makes a web page the person visits able to change their settings.
- **Only one copy runs.** The settings port is the lock: when it is taken, `listenPreferring` asks the owner for its `Server` header (`web.ServerName`) and a copy of this program makes the new one open the first one's settings page and exit. It finds the address in `settings-address.txt` beside the settings file (written at start, removed at exit, and only a `http://127.0.0.1` address with a key is ever opened). That file holds the page's key, so it lives in the owner-only folder and is never logged. If another program owns the port the new copy takes a free one, so the guard is best-effort then. Keep `Server` on every answer of the page.
- **The page's `Referrer-Policy` is `same-origin`, not `no-referrer`.** With `no-referrer` a browser sends `Origin: null` on the page's own form, and the Origin check on POST refuses every save. Tests that write the `Origin` header by hand cannot see this; `TestSavingWorksFromTheOriginABrowserReallySends` follows the header the page sends.
- **Tell the pop-up before the webhook.** A webhook that is down takes up to half a minute to give up, and the early-warning stream waits for it on its reading goroutine; the window only queues. Keep the window first in `multiSender`.
- **The Windows icon is built from the page's icon.** `cmd/alerts/rsrc_windows_amd64.syso` holds `internal/web/favicon.ico` for `alerts.exe`; a test fails when they differ. After changing the icon, run `go run github.com/akavel/rsrc@v0.10.2 -ico internal/web/favicon.ico -arch amd64 -o cmd/alerts/rsrc_windows_amd64.syso`.
- **The Windows file has no console window.** It is built with `-ldflags "-H=windowsgui"` (the release workflow does; add it to a local build to see what a person sees). `attachTerminal` joins the terminal that started it, so `alerts test` and `alerts check` still print; with no terminal, output goes to `log.txt` beside the settings (replaced each run, the page's key redacted) and a failure is shown in a message box. The pop-up helper never opens the log, or it would empty the log of the program that started it. People stop the program with Stop running on the settings page, which is disabled while nothing is watched.
- **A pop-up must never block the program.** Showing a window waits until it is closed, so it happens in `popup.Window` workers (a helper process on Windows), never in an alert source or in shutdown.
- **Errors are returned values**, wrapped with `%w` and a message that names the operation. Compare with `errors.Is` and `errors.As`.
- **Never weaken a gate to make a change pass.** Changing `.golangci.yml`, `lefthook.yml`, or the CI workflow needs the maintainer's approval.
- **Never bypass git hooks** (`--no-verify`).
- **Be a good guest of JMA.** Poll no faster than once a minute (the feeds only change that often), send `If-None-Match`, and never download a telegram twice: JMA blocks addresses that download 10 GB or more a day. Only download from under the data address, never from a URL a feed names elsewhere.
- **Be a good guest of P2P Quake too.** One WebSocket connection (its limit is two per address), pinged every 10 seconds, reconnecting with backoff. Never poll its history API in a loop; it allows 60 requests a minute and adds about a second of delay. It is read once after each connect, at most once every ten seconds, for warnings from the last two minutes.
- **Do not lengthen the ping.** Measured against the live service: pings every 10 seconds kept a connection for over nine minutes, while pings every 30 seconds were cut at 60 or 90 seconds. Re-measure before changing it.
- **Failing to save history never stops the program.** Log it once and carry on.
- **Real webhook URLs live in the environment**, never in the repository. `.env` is ignored.
- **Every release carries the Windows `.exe` on its own**, named `japan-emergency-alerts_<tag>_windows_amd64.exe`, beside the zips and listed in `SHA256SUMS`. The users are travelers who download one file and double-click it. `release.yml` builds it; keep it when changing the workflow, and look for it in the draft release before it is published.
- **Credit what you reference.** When a design, config, or code comes from a public source, add it to the README "Credits" in the same change. If any text or code is copied or adapted, keep the upstream license text and record it in `NOTICE.md`. Only MIT, BSD, and Apache-2.0 material comes in.
- **Do not commit `.local/`.**
- Conventional Commits: `type(scope): subject`, imperative, lowercase.
- **Comments say why**, never what, and stay short.
- **Markdown: one paragraph or list item per line.** Never hard-wrap prose.

## Alert content rules

These follow the JMA terms (https://www.jma.go.jp/jma/en/copyright.html) and the Meteorological Service Act. They are our reading, not legal advice.

- **Relay JMA text unchanged.** Select by region and threshold only. Never compute our own forecast, such as an expected intensity at a point or a tsunami arrival time.
- **Every message names the source** ("Source: Japan Meteorological Agency") and says it is an unofficial relay that has been processed.
- **No JMA logos or marks.**
- The early-warning source is a third party relaying JMA data, so the same rules apply to it.

## Layout

```
cmd/alerts/         composition root: config, logging, signals, shutdown, the settings page's app (app.go, serve.go, browser.go)
internal/async      the only place goroutines start
internal/clock      time behind an interface
internal/config     environment variables over the settings file (file.go) over defaults, to a checked Config; never logs the webhook path
internal/fileutil   atomic file writes with owner-only permissions
internal/web        the settings page: bilingual HTML, the local-only security headers, saving and the test buttons
internal/popup      pop-up windows as a second way to be told: a queue, a few workers, and one adapter per operating system
internal/region     the 47 prefectures (the README's number table is copied from it) and a Location (a set of prefectures, or all Japan); no GPS on purpose
internal/intensity  the JMA seismic intensity scale, ordered for threshold comparisons
internal/alert      the one shape every source turns its messages into
internal/filter     place, intensity threshold, and duplicates; pure logic, safe for several sources at once
internal/notify     posts to a Discord or generic webhook; retries; never prints or returns the webhook URL
internal/jma        JMA's Atom feeds and XML telegrams: Wanted (what to download), Decode (telegram to alert), Poller
internal/eew        P2P Quake's WebSocket of early warnings: Decode (message to frame), Stream (connection, pings, reconnects, cancellations, two-minute history read after each connect)
internal/state      the history file: when the feeds were last read in full, and which alerts were sent; atomic writes
```

`cmd/alerts` also holds `relay` (filter, then send through `multiSender`, then log why not; an alert counts as sent when any one channel worked), `check` (the dry run), `history` (saves the horizon every minute and at shutdown, records sent alerts) and `heartbeat` (the optional status message, webhook only). `app.supervise` runs the engine and starts it again in the same process when the page saves new settings. Sources, the browser, the listener and the pop-up window are injected through `environment`, so tests never touch the network or the screen.

How catching up works: `jma.Poller` keeps a horizon, the last time every feed was read in full. At start it is the saved one. Whenever it is more than five minutes old the poller reads the long feeds once and relays what was published after it (at most 12 hours back, marked `Late`). `Late` is also set for any alert delivered more than ten minutes after JMA issued it. The long feeds are large, so a gap is caught up once, and a failing telegram is retried at most five times.

Each source converts its own area codes to prefecture codes and sets `alert.Alert.ID`; the filter relies on both. An early warning uses the event id as its ID, so only the first message that reaches a person's prefecture is sent and later updates are dropped as duplicates; a cancellation has its own ID and is sent only to the prefectures the warning covered.

How each JMA telegram becomes an alert is in `internal/jma/telegram.go`: the type code in the file name picks the decoder, a drill or test status is never relayed, a tsunami warning goes to every prefecture, and an unreadable telegram of any kind except an earthquake still goes out from JMA's own feed summary.

## Things that will surprise you

Grow this by correction: when any tool gets something wrong, add the line.

- **Never name a package `httpapi`.** On the maintainer's Windows machine Bitdefender quarantines a test binary called `httpapi.test.exe`, and `go test` fails with `Access is denied`. Pick another name rather than rebuilding in a loop.
- **JMA's sample telegrams include drills.** `Control/Status` is `通常` (normal), `訓練` (drill) or `試験` (test). Only the last two are refused, on purpose, so a change in how the normal status is written can never silence a real alert.
- **P2P Quake names regions, not prefectures.** A warned region is `東京`, `神奈川`, `北海道道東`, `伊豆諸島` or `宮古島`: the prefecture without its suffix, Hokkaido in four, and some island groups. `eew.prefectureOf` maps them, and a region it does not know warns everyone with a logged error. A test checks that all 47 prefectures map. Its normal messages have no `test` key, and its history endpoint and the WebSocket carry the same message format, so saved history is real test data.
- **Intensity values carry stray whitespace** in real telegrams (`"4 "`), so always parse them through `intensity.Parse`, which trims.
- **To check the decoder against real data**, run `alerts check`. For new telegram types, JMA's official sample set is `jmaxml_*_Samples.zip` on https://xml.kishou.go.jp/tec_material.html; download it into its own empty folder and decode every file of the type once before trusting a decoder.
- **Windows editors and PowerShell add a UTF-8 byte-order mark** to files they write. `state.Open` strips it. When a script writes a file this program reads, use `[System.IO.File]::WriteAllText(path, text, (New-Object System.Text.UTF8Encoding($false)))`.
- **A long shell command with several heredocs can be rejected whole** by the Bash tool. Use the editor tools to write files and keep shell commands short.
- **On Windows, write files with LF endings.** `.gitattributes` forces LF in the repository.
- **Go's `url.Error` quotes the whole URL**, webhook secret included. Anything that can carry one is scrubbed before it is returned or logged; test that new error paths do the same.
- **The page's templates escape `+` as `&#43;`** inside attributes, so a test that matches HTML must accept both.
- **Memory is tuned in `tuneMemory` (one processor, `GOGC=20`), and the environment wins.** Set from `main`, `GOMAXPROCS` comes too late to drop the threads the runtime started for every core, so the program reaches about 7 MB, not the 6.5 MB it reaches when `GOMAXPROCS=1` is already in the environment at launch. An `init()` or a package variable could set it earlier, but `gochecknoinits` and `gochecknoglobals` forbid both. Measure with a build on another port (the settings port is the lock) and run each setup twice, since one run varies by about 0.5 MB.
- **The Windows pop-up is a second process.** `alerts.exe popup-window` reads the title and text from stdin and shows a `MessageBoxW`. Do not call `MessageBoxW` from the main process: it would hold up shutdown until someone closed the window.
- **To see a pop-up for real**, run `alerts test` with `POPUP=on`, check the window with `Get-Process | ? MainWindowTitle -like '*Japan Emergency*'`, and kill the process. Keep `CONFIG_FILE` and `STATE_FILE` pointed at temporary files so your own settings are not touched.
