# Japan Emergency Alerts

Travelers in Japan on a data-only SIM or eSIM do not get the emergency alerts that reach residents by cell broadcast. This free program listens to Japan's public alert sources and forwards the major ones to a pop-up window on your computer, to your phone through Discord, or both.

> **Not an official service.** It can be late, wrong or down, so never rely on it alone. It is not connected to the Japan Meteorological Agency (JMA) or any government body.
>
> - Alerts are JMA's own Japanese text under a short English heading. Use a translation app if you cannot read Japanese.
> - National J-Alert alerts (such as missile warnings) and city evacuation orders have no free source, so they are not covered. The Japan Tourism Agency's "Safety tips" app covers the national ones.
> - It has been checked against real recorded data and live connections, but has not yet relayed a live alert: none has occurred since it was built.

## Quick start (Windows)

Nothing to install, no terminal, no unzipping.

1. **Download** the file ending `_windows_amd64.exe` from the [latest release](https://github.com/tienduc120/japan-emergency-alerts/releases/latest), under **Assets**. Ignore the zips. If your browser asks, choose **Keep**.
2. **Double-click it.** If Windows says "Windows protected your PC" (the program is free and not code-signed), choose **More info**, then **Run anyway**.
3. Your browser opens the settings page (English and Japanese). In part 1, choose where alerts go and press **Test**: tick **Show a pop-up window on this computer**, and/or paste a [Discord webhook](#discord-webhook-for-your-phone).
4. In part 2, tick the prefecture you are in (a region of Japan, like a state). Not sure? Narita Airport and Tokyo Disney Resort are in Chiba. See [Choosing your location](#choosing-your-location).
5. In part 3, pick the weakest earthquake you want to hear about. The default, **5-lower** (5弱), is strong shaking. Tsunami, volcano and weather warnings are always sent.
6. Press **Save and start**. Nothing is watched until you do. Then close the tab: the program keeps running in the background.

## Mac and Linux

Download the zip for your system from the [latest release](https://github.com/tienduc120/japan-emergency-alerts/releases/latest), unzip it, run `alerts`, then follow steps 3 to 6. The zip name ends in `_darwin_arm64` (Apple silicon Mac), `_darwin_amd64` (Intel Mac), `_linux_amd64` or `_linux_arm64`.

- **macOS** may refuse an unidentified developer: right-click the file, choose **Open**. If still blocked, run `xattr -d com.apple.quarantine alerts`.
- **Linux**: run `chmod +x alerts` once. Pop-ups need `notify-send` or `zenity`; without either, only the webhook works.
- It runs in a terminal window: leave it open, or press **Stop running** on the settings page.

## Discord webhook for your phone

A pop-up only works while your computer is on and awake; a webhook also reaches your phone. A webhook is an address that lets the program post in one Discord channel.

1. Install Discord on your phone and allow notifications.
2. In a server you manage (or a new private one): channel settings, **Integrations**, **Webhooks**, **New Webhook**, **Copy Webhook URL**.
3. Paste it into the settings page and press **Test**.

Keep the address secret: anyone with it can post in your channel.

## Day to day

- **It does not start by itself after a restart.** Double-click the file again. It then watches silently, so it can look as if nothing happened.
- **To check it, change a setting or stop it, double-click the file again.** It never starts a second copy: it opens the settings page of the running one, where **Stop running** stops it.
- **A sleeping computer cannot warn you.** When it wakes, the program sends what JMA published in the meantime (up to 12 hours), marked "sent late". Early warnings are never caught up.
- **Open the page by double-clicking, not by typing its address.** The address holds a secret key that changes each start, and works only on your computer.
- **To replace the webhook, paste a new one. To remove it, clear the box and save.**
- Settings are in `config.json`, in the `japan-emergency-alerts` folder under `%AppData%` (Windows), `~/Library/Application Support` (macOS) or `~/.config` (Linux). It holds the webhook, so keep it private. On Windows, `log.txt` is beside it.

## What you get

| Alert | Sent when |
| --- | --- |
| Earthquake early warning (緊急地震速報) | Your prefecture is warned, as soon as it is issued. Once per event: updates are not repeated, and a cancellation is sent only if the warning was. Only this one can arrive before the shaking |
| Earthquake report (震度速報, 震源・震度に関する情報) | One to two minutes after the shaking, if your prefecture reached the strength you chose |
| Tsunami warning, major tsunami warning (not advisories) | Always, for everyone. JMA's zones are coastal strips, not prefectures, and a miss costs far more than an extra |
| Special weather warning (気象特別警報) | Your prefecture |
| Volcano eruption flash report, eruption warning for residential areas | The prefectures of the towns JMA names |

JMA's text is passed on unchanged, never reworded or added to. Drills and tests that JMA or the early-warning service mark as such are never relayed.

## If something goes wrong

| Problem | What to do |
| --- | --- |
| Nothing happened when I double-clicked | If settings are saved, it is already watching silently. Double-click again to open its settings page |
| The test message was refused | The webhook address is wrong or cut short. Copy it again; it starts `https://discord.com/api/webhooks/` |
| No pop-up | Check the box is ticked and saved. The program must be running and the computer awake |
| What did it do with an alert? | **Recent activity**, at the bottom of the settings page, lists each alert, whether it was sent, and why not |
| It would not start or stopped | On Windows a message box says why. See also `log.txt`: press Windows key + R, type `%AppData%\japan-emergency-alerts`, press Enter |

---

Below is reference for people who want to know how it works or to skip the settings page.

## How current is it?

**Early warnings** come from [P2P Quake](https://www.p2pquake.net/) (P2P地震情報), a community service that republishes JMA's warnings. It is free and fast, but says its delivery is not guaranteed, its servers are not redundant, and nothing is re-sent after an outage. The program keeps one connection open (the service allows two per address, so run one copy), pings it every 10 seconds, and reconnects within seconds. After each connect it reads the last two minutes of warnings, so a short drop costs nothing; a longer outage does.

**Everything else** comes from JMA's free, best-effort feeds, read once a minute within JMA's access rules: it asks only for what changed and never downloads a file twice. JMA says delivery may be delayed or stop during maintenance.

## After a restart, a sleep or an outage

The program keeps `state.json`: when it last read JMA's feeds in full, and which alerts it has sent.

- Nothing is sent twice across a restart.
- At start, and whenever the feeds went unread for over five minutes, it relays what JMA published since, up to 12 hours back. These are marked "sent late", as is any alert reaching you over ten minutes after JMA issued it.
- The very first run sends nothing from the past.
- If the file cannot be read or written, the program says so in its log and keeps running without that memory.

## Pop-ups

Each alert opens a window on top of your other windows, with JMA's text, until you close it. At most three are open at once; more wait their turn. Pop-up and webhook work independently, and an alert counts as sent if either worked.

- **Windows**: a system warning window, shown by a small helper (the program starts itself with a hidden `popup-window` command), so quitting is never held up by an open window.
- **macOS**: a critical alert dialog.
- **Linux**: `notify-send` or `zenity`.

## Configuration

The settings page saves everything below. Environment variables win over the page, and the page says when one overrides a setting. A bad value is reported with every problem at once: `test` and `check` refuse to run, and the program waits on the settings page for good ones.

| Variable | Meaning | Default |
| --- | --- | --- |
| `WEBHOOK_URL` | An `http` or `https` URL, such as a Discord webhook. Required unless `POPUP` is on. Keep it secret | none |
| `POPUP` | `on` to show each alert in a window on this computer | the page's choice, else `off` |
| `LOCATION` | Prefecture names (English or Japanese) or numbers 1 to 47, comma-separated, or `all`. See [Choosing your location](#choosing-your-location) | `Tokyo` |
| `MIN_INTENSITY` | Weakest earthquake report to send: `0` to `4`, `5-`, `5+`, `6-`, `6+`, `7` (`5弱`, `5強`, `6弱`, `6強` also work). Other alert types ignore it | `5-` |
| `HEARTBEAT_HOURS` | Hours between status messages to the webhook, `1` to `168`; `0` is none | `0` |
| `CONFIG_FILE` | The settings file. Written with owner-only permissions where the system supports them | `config.json` in the settings folder (see [Day to day](#day-to-day)) |
| `STATE_FILE` | The history file. With no user configuration folder, no history is kept | `state.json` in the settings folder |

The log is plain text and never contains the webhook address.

**Heartbeat** (off by default, so your channel stays quiet): with `HEARTBEAT_HOURS=24`, the program posts a status message to the webhook once a day, never as a pop-up. It says since when it has been running, where it is watching, how many alerts it has sent, whether JMA's feeds are being read, and whether the early-warning connection is up. If the message stops, the program or your computer has stopped.

## Checking that it works

```sh
alerts test        # or, from a clone of this repository: go run ./cmd/alerts test
alerts check
```

`test` tests each way you set up: a message that says it is not an alert for the webhook, and a window for the pop-up (close it to finish). It tells you if the webhook refused the message. It is the same as the **Test** buttons.

`check` reads what JMA published over the last few days and prints, for each alert, whether it would have been sent for your settings and why not. It posts nothing.

How a message is delivered depends on the URL:

- **Discord** (`discord.com` and `discordapp.com`): a normal message with a short English heading and the issue time in JST, JMA's text unchanged, the credit and a note that this is an unofficial relay. Long texts are split at line breaks, and mentions such as `@everyone` never ping anyone.
- **Any other URL**: one JSON object per alert with `id`, `kind`, `issued`, `areas` (prefecture numbers, with the intensity for earthquake reports), `text`, `source`, `notice`, and `message` (the text Discord gets).

If the receiver is busy or briefly down, sending is retried up to three times; a wrong URL is not retried. Errors never show the webhook URL.

## Choosing your location

Alerts are matched by prefecture (都道府県), not by distance. There is no GPS or coordinate setting on purpose: a prefecture name is simpler, and close to how JMA labels its alerts.

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

**2. Set it.** On the settings page, tick it in part 2 and press **Save and start**. With `LOCATION`, use the English name in any capitalization, the Japanese name, or the number from the table below, separated by commas (`Tokyo,Kanagawa`, `13,14`, `大阪府`); names and numbers can be mixed.

```sh
export LOCATION=Tokyo,Kanagawa        # macOS or Linux
```

```powershell
$env:LOCATION = "大阪府"               # Windows PowerShell
```

**3. Update it when you move.** Change the ticks on the page and save, or change `LOCATION` and restart. Staying near a border, or touring? List every place, such as `Tokyo,Kanagawa,Kyoto`. `all` is the last resort: every major alert in Japan, so more messages. A mistake in any entry is reported and names the entry, so a place is never silently left unwatched. The earthquake threshold is checked against the shaking in your listed prefectures, or the strongest anywhere with `all`.

<details>
<summary>Prefecture numbers</summary>

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

</details>

## Development

You need Go, golangci-lint, govulncheck and lefthook.

```sh
lefthook install                          # run the checks on every commit and push
go test -race -timeout 120s -count=1 ./...
```

Build with `go build -o alerts ./cmd/alerts` (add `.exe` on Windows); no C compiler is needed. The released Windows file uses `-ldflags "-H=windowsgui"` so it opens no console window. From a terminal it still prints, but the terminal does not wait for it, so run `.\alerts.exe test | Out-Host` in PowerShell to see the output in order.

Pushing a tag such as `v0.1.0` makes GitHub Actions test, build for Windows, Linux and macOS, and attach the zips, the Windows `.exe` on its own and `SHA256SUMS` to a draft release, which the maintainer publishes by hand.

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
