# Third-party material

Only MIT, BSD, and Apache-2.0 material is brought in. For each item keep the upstream license text in the repository and record the upstream below.

## Adapted

| Item                        | Upstream                                         | License                                | Where                                                                    |
| --------------------------- | ------------------------------------------------ | -------------------------------------- | ------------------------------------------------------------------------ |
| golangci-lint configuration | https://github.com/maratori/golangci-lint-config | MIT, Copyright (c) 2025 Marat Reimers  | `.golangci.yml`; license text in `LICENSES/maratori-golangci-lint-config.txt` |

## Data

- Japan Meteorological Agency (JMA) public XML telegrams and feeds, https://www.data.jma.go.jp/developer/xml/feed/, used under the [JMA terms of use](https://www.jma.go.jp/jma/en/copyright.html) (Public Data License 1.0, compatible with CC BY 4.0). Source: Japan Meteorological Agency website.
- P2P Quake (P2P地震情報) WebSocket stream, wss://api.p2pquake.net/v2/ws, used under its [secondary use terms](https://www.p2pquake.net/secondary_use/): free for commercial and non-commercial use, no registration, with a limit of two WebSocket connections per address. It republishes JMA data received through DM-D.S.S.
- `internal/eew/testdata/*.json` are modelled on real P2P Quake messages, with invented event numbers and trimmed regions.
- `internal/jma/testdata/*.xml` are real telegrams taken from those feeds, or sample telegrams from JMA's technical material (https://xml.kishou.go.jp/tec_material.html), trimmed to what the tests need and in some cases edited. They are fixtures, not JMA's current output.

## Own material

- `internal/web/favicon.ico` is the author's own icon, published here under the MIT license of this repository. It is the icon of the settings page and, through `cmd/alerts/rsrc_windows_amd64.syso`, of `alerts.exe`.

## Go dependencies (used as modules, not copied)

- `go.uber.org/goleak` v1.3.0 (MIT, Copyright (c) 2018 Uber Technologies, Inc.), the goroutine-leak check in `TestMain`.
- `github.com/gorilla/websocket` v1.5.3 (BSD-2-Clause, Copyright (c) 2013 The Gorilla WebSocket Authors), the WebSocket client behind the early-warning stream.

## Developer tools (installed, not copied)

golangci-lint, govulncheck, lefthook, and `github.com/akavel/rsrc` v0.10.2 (MIT), which turns the icon into the Windows resource file with `go run`. Each keeps its own license.
