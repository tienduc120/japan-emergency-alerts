# japan-emergency-alerts

Travelers in Japan on a data-only SIM or eSIM do not receive the emergency alerts that reach residents by cell broadcast. This program listens to free sources and forwards the major alerts to a webhook, such as a Discord channel, to a pop-up window on the computer it runs on, or to both.

Status: early development. It relays earthquake early warnings, earthquake reports, tsunami warnings, special weather warnings and volcano alerts, catches up after being offline, and has a settings page for people who do not use a terminal. It has not yet relayed a live alert, because none has occurred since it was built; it has been checked against real recorded data and live connections. No release has been published yet, so for now you build it yourself (see [Development](#development)).

## Not an official service

This is an unofficial relay and never a replacement for the real alerts. It is not affiliated with the Japan Meteorological Agency (JMA) or any government body, and it can be late, wrong, or down. Alerts are in JMA's own Japanese wording; for earthquake reports, early warnings and tsunami warnings, JMA's fields are arranged into lines, never reworded or added to.

Alerts that are not covered: J-Alert national alerts (such as missile warnings) and municipal evacuation orders have no free feed. The Japan Tourism Agency's "Safety tips" app covers the national alerts for visitors.

## Quick start (no terminal needed)

1. Download the file for your computer from the Releases page and unzip it: `alerts.exe` for Windows, `alerts` for macOS or Linux.
2. Double-click it. A window opens and stays open, and your browser opens a settings page. If it does not, copy the `Settings page:` address from the window into your browser.
3. On the page, choose where alerts go: paste a webhook address, switch on **Show a pop-up window on this computer**, or both. Press **Send test message** or **Show a test pop-up** to check.
4. Tick the prefectures you are in. The page is in English and Japanese. Pick the weakest earthquake you want to hear about (the default, intensity 5-lower, is when many people are frightened).
5. Press **Save and start**. Leave the window open: closing it stops the alerts. The next time, it starts watching at once and does not open the browser.

Only one copy runs at a time. Starting it again does not start a second one: it opens the settings page of the copy that is running, which is also the way back to the page later. The webhook box shows the address you saved; change it to replace it, or clear it and save to remove it.

The page can be reached only from your own computer. Its address carries a secret key so that other web pages you visit cannot change your settings, so use the address the program printed. It changes each time the program starts. Settings are kept in `config.json` in `japan-emergency-alerts` inside your user configuration folder (`%AppData%` on Windows, `~/Library/Application Support` on macOS, `~/.config` on Linux). The page's "Recent activity" shows what the program did with each alert, including the ones it did not send and why.

The program is not code-signed, so the first time:

- **Windows** may show "Windows protected your PC". Choose **More info**, then **Run anyway**.
- **macOS** may refuse an unidentified developer. Right-click the file, choose **Open**, and confirm. If it is still blocked, run `xattr -d com.apple.quarantine alerts` in a terminal.
- **Linux**: run `chmod +x alerts` once.

### Pop-up windows

With the pop-up on, each alert also opens a window on top of your other windows, with JMA's text, until you close it. It works beside the webhook, so a webhook that is down never costs you the pop-up, and the other way round; an alert counts as sent if either worked. At most three windows are open at once, and a few more wait their turn. The program has to be running, and your computer awake, for a window to appear. A webhook reaches your phone as well, which is why it is worth using even if you also use pop-ups.

- **Windows**: a warning window from the system, shown by a small helper (the program starts itself with a hidden `popup-window` command), so quitting the program is never held up by an open window.
- **macOS**: a critical alert dialog.
- **Linux**: `notify-send` or `zenity` must be installed. Without either, no window can be shown: the log says so, and the webhook, if you have one, still works.

## What it covers

The major alerts a resident's phone shows. The early warning is pushed to the program the moment it is issued; the rest come from JMA's free public feeds, checked once a minute:

| Alert | What is sent | Matched to your location by |
| --- | --- | --- |
| Earthquake early warning (緊急地震速報, warnings only) | The epicentre, magnitude, depth, and the forecast intensity for each warned region. Sent once per event, as soon as your prefecture is warned; a later update is not repeated, and a cancellation is sent only if the warning was | The prefectures of the warned regions |
| Earthquake report (震度速報 and 震源・震度に関する情報) | Epicentre, magnitude, the shaking in each prefecture, and JMA's tsunami note. Only if the shaking in your prefecture reaches `MIN_INTENSITY` | The prefectures JMA lists, each with its own intensity |
| Tsunami warning and major tsunami warning (not advisories) | JMA's headline and the warned coastal zones | Everyone. JMA's zones are coastal strips, not prefectures, so every location gets it; it is rare, and a miss costs far more than an extra |
| Special weather warning (気象特別警報) | JMA's own summary, unchanged | The prefecture of the warning |
| Volcano eruption flash report, and eruption warnings for residential areas | JMA's headline and activity text | The prefectures of the towns JMA names |

An earthquake report arrives one to two minutes after the shaking, so it tells you what happened. Only the early warning can come before it.

Drills and test messages that JMA or the early-warning service mark as such are never relayed.

### How current is it?

**Early warnings** come from [P2P Quake](https://www.p2pquake.net/) (P2P地震情報), a community service that receives JMA's warnings from a licensed distributor and republishes them. It is free, allows commercial use, and is fast, but it says plainly that its content and delivery are not guaranteed, that its servers are not redundant, that nothing is re-sent after an outage, and that it is not recommended as an early-warning service. This program keeps one connection open, pings it every 10 seconds (the service's network edge cut quieter connections within a minute or two), and reconnects within seconds when it drops. After every connect it also reads the service's list of its latest warnings once and relays any issued in the last two minutes, so a short drop does not cost you a warning. A longer outage still does. It is an extra layer, never a substitute for the alerts a Japanese phone receives. The service allows two connections per network address, so run one copy of the program.

**Everything else** comes from JMA's feeds, which are free and best-effort. JMA says delivery may be delayed or stop during maintenance, and it points anyone who needs guaranteed, fast delivery to licensed providers. This program polls each minute and stays well inside JMA's access rules: it asks only for what changed, never downloads a file twice, and uses a small fraction of the daily limit.

### After a restart, a sleep or an outage

The program keeps a small file (`state.json`, see `STATE_FILE`) with two things: when it last read JMA's feeds in full, and which alerts it has already sent. So:

- **Nothing is sent twice** across a restart.
- **Nothing published while it was off is lost.** When it starts, and whenever the feeds have gone unread for more than five minutes (a laptop that slept, a network outage), it reads JMA's multi-day feeds and relays what was published since, up to 12 hours back. These are marked "sent late", as is any alert that reaches you more than ten minutes after JMA issued it, so they never pass for live ones.
- **The very first run sends nothing from the past.**
- **Early warnings are not caught up.** A warning that arrives late is worthless; the only recovery is the two-minute read after a reconnect described above.

If the file cannot be read or written, the program says so in its log and keeps running without that memory.

### Is it still running? (optional)

It is off by default, so your alert channel stays quiet. If you want a sign of life, set the environment variable `HEARTBEAT_HOURS=24` and it posts a status message to the webhook once a day (never as a pop-up, and not at all without a webhook): since when it has been running, where it is watching, how many alerts it has sent, whether JMA's feeds are being read, and whether the early-warning connection is up. If the message stops arriving, the program or your computer has stopped; if it says a source is not working, that source is blind even though the program is alive.

### See it work on real data

```sh
alerts check
```

reads what JMA published over the last few days and prints, for each alert, whether it would have been sent for your settings and why not if not. It posts nothing, so it is safe to run at any time.

## Configuration

Most people only use the settings page. Everything it saves can also be set with environment variables, which win over the page: when one overrides a setting, the page says so. This suits servers, containers and scripts. A bad value is reported with every problem at once: the `test` and `check` commands refuse to run, and the program waits on the settings page for good ones.

| Variable | Meaning | Default |
| --- | --- | --- |
| `WEBHOOK_URL` | Where alerts are posted: an `http` or `https` URL, such as a Discord webhook. Required unless `POPUP` is on. Keep it secret: anyone with it can post to your channel | none |
| `POPUP` | `on` to show each alert in a window on this computer too (or instead of a webhook); `off` | the page's choice, else `off` |
| `LOCATION` | The prefecture or prefectures you are in: names in English or Japanese, or numbers from 1 to 47, separated by commas (`Tokyo,Kanagawa`, `13,14`, `大阪府`), or `all` for the whole country. See [Choosing your location](#choosing-your-location) | `Tokyo` |
| `MIN_INTENSITY` | Weakest earthquake report to send, on the JMA scale: `0` to `4`, `5-`, `5+`, `6-`, `6+`, `7` (`5弱`, `5強`, `6弱`, `6強` also work). Other alert types ignore it | `5-` |
| `HEARTBEAT_HOURS` | Hours between status messages, from `1` to `168`; `0` means none | `0` (off) |
| `CONFIG_FILE` | The settings file the page saves to. The webhook address is in it, so keep it private: it is written with owner-only permissions where the system supports them | `config.json` in `japan-emergency-alerts` inside your user configuration folder |
| `STATE_FILE` | Where the program keeps what it must remember across a restart. The folder is created if needed | `state.json` in `japan-emergency-alerts` inside your user configuration folder (`%AppData%` on Windows, `~/.config` on Linux). With no such folder, no history is kept |

The program writes a plain-text log to its window: when it started, each alert sent or held back, and the problems it met. The log never contains the webhook address.

## Checking that it works

Before you rely on it, send a test. On the settings page use **Send test message** and **Show a test pop-up**, or from a terminal:

```sh
alerts test        # or, from a clone of this repository: go run ./cmd/alerts test
```

It tests each way you set up: one message that says it is not an alert for the webhook, and a window for the pop-up (close it to finish). It tells you if the webhook refused the message. Errors never show the webhook URL.

How a message is delivered depends on the URL:

- **Discord** (`discord.com` and `discordapp.com` URLs are recognized): a normal message. JMA's text is passed on unchanged under a short English heading with the issue time in JST, followed by the credit and a note that this is an unofficial relay. Long texts are split at line breaks to fit Discord's size limit, and mentions such as `@everyone` in the text never ping anyone.
- **Any other URL**: one JSON object per alert with `id`, `kind`, `issued`, `areas` (prefecture numbers, with the intensity for earthquake reports), `text`, `source`, `notice`, and `message` (the same ready-to-read text Discord gets).

If the receiver is busy or briefly down, sending is retried up to three times; a wrong URL is not retried.

## Choosing your location

Alerts are matched by prefecture (都道府県), not by distance. On the settings page, tick the prefectures where you are staying (or **Whole Japan**). With the `LOCATION` variable, give the prefecture, or a comma-separated list of them, as described below, and you get the major alerts for those prefectures.

**1. Find your prefecture.** Most places are in the prefecture of the same name, but these catch people out:

| Place | Prefecture to use |
| --- | --- |
| Narita Airport, Tokyo Disney Resort | `Chiba` |
| Haneda Airport, Shinjuku, Shibuya, Asakusa | `Tokyo` |
| Yokohama, Kawasaki, Kamakura, Hakone | `Kanagawa` |
| Nikko | `Tochigi` |
| Kobe, Himeji | `Hyogo` |
| Kansai Airport, Universal Studios Japan | `Osaka` |
| Mount Fuji | `Shizuoka` or `Yamanashi`, depending on the side |
| Kanazawa | `Ishikawa` |
| Nagoya | `Aichi` |
| Hakata, Fukuoka city | `Fukuoka` |
| Naha, Ishigaki | `Okinawa` |

If your place is not here, search its name with "prefecture" (for example "Takayama prefecture" gives Gifu).

**2. Set it.** Use the English name in any capitalization, the full Japanese name, or the number from the table below. Several places are separated by commas, and spaces do not matter:

```sh
export LOCATION=Osaka                 # macOS or Linux, one prefecture
export LOCATION=Tokyo,Kanagawa        # a list
```

```powershell
$env:LOCATION = "大阪府"               # Windows PowerShell, one prefecture
$env:LOCATION = "13, 14"              # a list, by number
```

**3. Update it when you move.** Change `LOCATION` and restart the program when you change prefecture. Staying near a border (say in Kawasaki and visiting Tokyo), or touring several places? List them all, for example `Tokyo,Kanagawa,Kyoto`. `all` is the last resort: every major alert in Japan, which means more messages. A mistake in any entry stops the program at start and names the entry, so a place is never silently left unwatched.

The earthquake threshold is checked against the shaking reported in your listed prefectures, or against the strongest shaking anywhere if you use `all`.

**4. The numbers.** Names and numbers can be mixed (`Tokyo,14`). The number is the standard JIS prefecture code, and it saves you typing Japanese:

| No. | English | 日本語 |
| --- | --- | --- |
| 1 | Hokkaido | 北海道 |
| 2 | Aomori | 青森県 |
| 3 | Iwate | 岩手県 |
| 4 | Miyagi | 宮城県 |
| 5 | Akita | 秋田県 |
| 6 | Yamagata | 山形県 |
| 7 | Fukushima | 福島県 |
| 8 | Ibaraki | 茨城県 |
| 9 | Tochigi | 栃木県 |
| 10 | Gunma | 群馬県 |
| 11 | Saitama | 埼玉県 |
| 12 | Chiba | 千葉県 |
| 13 | Tokyo | 東京都 |
| 14 | Kanagawa | 神奈川県 |
| 15 | Niigata | 新潟県 |
| 16 | Toyama | 富山県 |
| 17 | Ishikawa | 石川県 |
| 18 | Fukui | 福井県 |
| 19 | Yamanashi | 山梨県 |
| 20 | Nagano | 長野県 |
| 21 | Gifu | 岐阜県 |
| 22 | Shizuoka | 静岡県 |
| 23 | Aichi | 愛知県 |
| 24 | Mie | 三重県 |
| 25 | Shiga | 滋賀県 |
| 26 | Kyoto | 京都府 |
| 27 | Osaka | 大阪府 |
| 28 | Hyogo | 兵庫県 |
| 29 | Nara | 奈良県 |
| 30 | Wakayama | 和歌山県 |
| 31 | Tottori | 鳥取県 |
| 32 | Shimane | 島根県 |
| 33 | Okayama | 岡山県 |
| 34 | Hiroshima | 広島県 |
| 35 | Yamaguchi | 山口県 |
| 36 | Tokushima | 徳島県 |
| 37 | Kagawa | 香川県 |
| 38 | Ehime | 愛媛県 |
| 39 | Kochi | 高知県 |
| 40 | Fukuoka | 福岡県 |
| 41 | Saga | 佐賀県 |
| 42 | Nagasaki | 長崎県 |
| 43 | Kumamoto | 熊本県 |
| 44 | Oita | 大分県 |
| 45 | Miyazaki | 宮崎県 |
| 46 | Kagoshima | 鹿児島県 |
| 47 | Okinawa | 沖縄県 |

There is no GPS or coordinate setting on purpose: a prefecture name is simpler, and it is close to how JMA labels its alerts.

## Development

You need Go, golangci-lint, govulncheck and lefthook.

```sh
lefthook install                          # run the checks on every commit and push
go test -race -timeout 120s -count=1 ./...
```

To build the program yourself: `go build -o alerts ./cmd/alerts` (add `.exe` on Windows). It needs no C compiler and runs on Windows, Linux and macOS.

Releases are built by GitHub Actions when a tag such as `v0.1.0` is pushed: the tests run, the program is built for Windows (amd64), Linux (amd64, arm64) and macOS (arm64, amd64), and the zip files and a `SHA256SUMS` file are attached to a draft release, which the maintainer publishes by hand.

Read [AGENTS.md](AGENTS.md) for the working rules.

## License and authors

MIT: free to use, modify, and use commercially. See [LICENSE](LICENSE) and [AUTHORS.md](AUTHORS.md).

## Credits

- Alert data: the Japan Meteorological Agency (JMA), from its public XML feeds and used under its [terms](https://www.jma.go.jp/jma/en/copyright.html). Source: Japan Meteorological Agency. The test fixtures in `internal/jma/testdata` are real or sample JMA telegrams, trimmed.
- Earthquake early warnings: [P2P地震情報 (P2P Quake)](https://www.p2pquake.net/), used under its [secondary use terms](https://www.p2pquake.net/secondary_use/). The warnings are JMA's, delivered through P2P Quake. The fixtures in `internal/eew/testdata` are modelled on its real messages.
- [gorilla/websocket](https://github.com/gorilla/websocket) (BSD-2-Clause): the WebSocket client behind the early-warning stream.
- [golangci-lint-config](https://github.com/maratori/golangci-lint-config) by maratori (MIT): `.golangci.yml` is adapted from it, with the license text kept in [LICENSES/](LICENSES/maratori-golangci-lint-config.txt).
- [goleak](https://github.com/uber-go/goleak) by Uber (MIT): the `TestMain` check that fails a package whose tests leave a goroutine running.
- [rsrc](https://github.com/akavel/rsrc) by akavel (MIT): the tool that turns the icon into the Windows resource file; it is run, not copied.
- `internal/async` and `internal/clock` come from the author's own go-base template.

Third-party licenses are listed in [NOTICE.md](NOTICE.md).
