# Masir

**The Xray client that routes the way you think.**

Masir provides the full power of Xray's routing without requiring the core to restart whenever you make a change. Subscriptions, server switching, and updates are managed automatically. Route traffic by app or site, sending it through the proxy or directly to the internet according to your rules.

**[Download the latest release](https://github.com/Narixius/masir-go/releases/latest)**

```text
$ masir status

Masir Daemon 0.1.10 ● Active (privileged, service)
├─ Serving: you (uid 501)
└─ Uptime:  3h 12m (since 2026-10-25 09:14:02)

Xray Core v26.10.21 ● Active
├─ Mode:             TUN (system-wide routing)
├─ Routing Fallback: Proxy (unmatched traffic goes through the active proxy)
├─ Always-on Proxy:  OFF
├─ Proxy Port:       127.0.0.1:7890
├─ DoH DNS:          https://1.1.1.1/dns-query
├─ DNS Servers:      1.1.1.1, 8.8.8.8
├─ Xray Log:         info
└─ Geo Data:         2026-10-25 (3h ago, /usr/local/lib/masir)

Connectivity
├─ Internet: ✔ 92ms
└─ Proxy:    Frankfurt 2  ✔ 168ms
✔ Online, and the active proxy works.
```

## Why Masir

- **Route by app.** One rule sends a whole app through the proxy or keeps it direct, helper processes included. Pick it from a list of your installed apps.

- **Full Xray routing.** Every rule is a real Xray routing rule: domains (+geosite), IPs (+geoip), ports, protocols, processes, or raw JSON. Xray checks each rule before it's saved.

- **Live changes, no restarts.** Rules, server switches and pins are applied to the running core through Xray's own gRPC API. Xray keeps running while you tweak, so nothing else drops.

- **Pin rules to any server.** Send one service through one server, from any subscription, whatever the active server is. If that server disappears, the rule falls back instead of breaking.

- **Subscriptions on autopilot.** JSON configs and share links (`vless://`, `vmess://`, `trojan://`, `ss://`, `hysteria2://`, ...), refreshed on schedule, with traffic and expiry shown, fetched through the proxy when the provider is blocked.

- **Live latency.** The server picker tests what's on screen as you scroll. `l` retests one, `a` retests all, or let `masir use --fastest` pick.

- **TUN or proxy, your choice.** TUN routes the whole system without touching your DNS settings (app-based routings work in TUN mode only); proxy mode gives you Mixed protocol (SOCKS5 + HTTP) on a port, which you can share over your LAN if you choose to.

- **Keeps itself current.** Receive the verified latest updates of the Masir and GEO files using `masir update` and `masir update geo`.

- **Scriptable, and built to be built on.** Plain output when piped, `--json` where it matters, smart shell completions, and a gRPC API for your own tools or a GUI.

## Philosophy

Routing is what Xray does best. Its rules can send each connection exactly where it should go: this app through the proxy, that site direct, ads nowhere, one service through the one server that works for it. Most Xray clients barely expose any of that. They start Xray system-wide, push all traffic through the tunnel, and hide routing behind a screen that's hard to find and harder to configure. Doing anything beyond the defaults means hand-editing JSON, and switching servers often restarts the core and drops every connection.

Masir started from the opposite end. Routing comes first: rules are easy to write and easy to read, and nothing about them is watered down, so anything Xray can match, Masir can route. The core stays up while you change things, because Xray's own API can apply changes live. Servers from several subscriptions sit side by side, and any rule can point at any of them. And the tool should feel good to use every day: fast to pick a server, clear about what's running, quiet when everything works.

It's the client I wanted for myself, for living with Xray configs and moving between many servers. If you've felt the same friction, it's probably the client you wanted too.

## Install

1. Download the archive for your system from the **[releases page](https://github.com/Narixius/masir-go/releases/latest)**:

   | System | Archive |
   |---|---|
   | macOS, Apple Silicon | `masir-cli-darwin-<version>-arm64.tar.gz` |
   | macOS, Intel | `masir-cli-darwin-<version>-amd64.tar.gz` |
   | Linux, x86-64 | `masir-cli-linux-<version>-amd64.tar.gz` |
   | Linux, ARM64 | `masir-cli-linux-<version>-arm64.tar.gz` |
   | Windows, x86-64 | `masir-cli-windows-<version>-amd64.zip` |
   | Windows, ARM64 | `masir-cli-windows-<version>-arm64.zip` |

   Each archive holds the `masir` executable plus `geosite.dat` and `geoip.dat`. Keep the three files together. (On Windows, the [Wintun](https://www.wintun.net) driver TUN mode needs is built into `masir.exe`.)

2. Put them in a folder on your `PATH`, for example:

   ```sh
   mkdir -p ~/.local/masir
   tar -xzf masir-cli-darwin-<version>-arm64.tar.gz -C ~/.local/masir
   ln -s ~/.local/masir/masir ~/.local/bin/masir
   ```

   On Windows, extract the zip into a folder such as `%LOCALAPPDATA%\masir` and add that folder to your `PATH`.

3. On macOS, if the archive came through a browser, clear the download quarantine once:

   ```sh
   xattr -d com.apple.quarantine ~/.local/masir/masir
   ```

4. Optional: enable shell completions.

    ```sh
    # zsh
    masir completion zsh > "${fpath[1]}/_masir"
    exec zsh

    # bash (user-local; the exact directory depends on your distro)
    mkdir -p ~/.local/share/bash-completion/completions
    masir completion bash > ~/.local/share/bash-completion/completions/masir

    # fish
    masir completion fish > ~/.config/fish/completions/masir.fish
    ```

    PowerShell:

    ```powershell
    # current session
    masir completion powershell | Out-String | Invoke-Expression

    # permanent
    Add-Content $PROFILE 'masir completion powershell | Out-String | Invoke-Expression'
    ```

    Create `$PROFILE` first with `New-Item -ItemType File -Force $PROFILE` if it doesn't
    exist.

That's it. There's nothing separate to install for the daemon: masir starts it in the background when a command needs it.

### Desktop app

Masir also comes as a desktop app for people who would rather click than type. There are
two ways to install it, and the app ships the `masir` command-line tool inside it, so it
needs no separate download:

| System | Download |
|---|---|
| macOS, Apple Silicon | `masir-desktop-darwin-<version>-arm64.dmg` |
| macOS, Intel | `masir-desktop-darwin-<version>-amd64.dmg` |
| Windows, x86-64 | `masir-desktop-windows-<version>-amd64.zip` |
| Windows, ARM64 | `masir-desktop-windows-<version>-arm64.zip` |
| Linux, x86-64 | `masir-desktop-linux-<version>-amd64.deb`, or the portable `.tar.gz` |
| Linux, ARM64 | `masir-desktop-linux-<version>-arm64.deb`, or the portable `.tar.gz` |

It lives in the tray: a native popover on macOS, an app window on Windows and Linux. It
starts the daemon when none is running, and asks for root through the system's own prompt
when tunnel mode needs it. Settings can install the daemon as a background service and
install the `masir` command for the terminal. The release builds are not yet signed with a
Developer ID or a code-signing certificate; macOS may need a right-click → Open, and
Windows SmartScreen may ask first.

## Quick start

```sh
masir sub add https://example.com/your-subscription    # add a subscription
masir use                                              # pick a server, with live latency
masir connect                                          # turn masir on (TUN mode)
masir status                                           # see what's running
```

`masir connect` uses TUN mode, which needs root (Administrator rights on Windows). If its daemon isn't running with those rights, Masir starts it that way for the session: sudo asks for your password on macOS and Linux (unless it remembers you), Windows shows its permission prompt. To never be asked again, run `masir daemon enable` once: it installs the daemon as a system service that starts at boot. Prefer not to? `masir connect --proxy` runs proxy mode only, as your normal user.

Then make it yours:

```sh
masir rules add                                        # the guided rule wizard
masir mode direct                                      # (optional) send unmatched traffic direct
```

Run `masir` with no arguments for the full command list, or `masir menu` for an interactive menu of everything.

## Routing rules

Rules are checked top to bottom; the first match wins. Traffic no rule matches goes to the **routing mode**: `proxy` (the default) or `direct`, switched with `masir mode`. With no server selected yet, Masir has to send it direct until you pick one. Local network traffic and DNS are handled by built-in rules, so you never lock yourself out.

Each rule sends its traffic to one of:

- `proxy`: the active server
- `direct`: straight out, skipping the proxy
- `block`: dropped
- a specific server, from any subscription (`--via <server id>`)
- a custom Xray outbound (`outbound:<tag>`)

```sh
# open a wizard to define a rule step by step (Recommended)
masir rules add

# Iranian websites and addresses go direct
masir rules add --domain geosite:category-ir --ip geoip:ir -o direct

# Telegram, with all its helper processes, through the proxy
masir rules add --app /Applications/Telegram.app/ -o proxy

# Google always through one specific server, whatever the active one is
masir rules add --domain geosite:google --via 737e3ea15f56

# Block ads and trackers
masir rules add --domain geosite:category-ads-all -o block

# SSH always direct
masir rules add --port 22 -o direct

# Anything Xray understands, as raw rule JSON
masir rules add --name lab --json '{"source":["192.168.1.0/24"],"network":"udp"}' -o direct
```

Matchers combine within a rule (all must match), and each accepts a comma-separated list:

| Flag | Matches | Examples |
|---|---|---|
| `--domain` | websites | `example.com`, `geosite:google`, `domain:x.com`, `keyword:cdn`, `regexp:...` |
| `--ip` | destination addresses | `1.2.3.4`, `10.0.0.0/8`, `geoip:ir`, `geoip:private` |
| `--port` | destination ports | `443`, `1000-2000` |
| `--network` | transport | `tcp`, `udp` |
| `--protocol` | sniffed protocol | `http`, `tls`, `quic`, `bittorrent` |
| `--process`, `--app` | the app sending the traffic | `Telegram`, `/Applications/Spotify.app/` (a path ending in `/` matches everything inside) |
| `--source` | the sending device, when sharing over LAN | `192.168.1.20` |
| `--source-port` | the sending port | `5000-6000` |

Manage rules with `masir rules` (list), `masir rules edit <name>` (an interactive editor, or flags such as `--rename`, `--append`, `--unset`), `masir rules rm`, and `masir rules test <domain|ip>` to see exactly where a destination would go right now. `masir rules disable <name>` and `masir rules enable <name>` turn a rule off and on without removing it.

Geo data comes from [Loyalsoldier/v2ray-rules-dat](https://github.com/Loyalsoldier/v2ray-rules-dat), refreshed daily upstream: `geosite:` is the v2fly domain list with hundreds of thousands of extra domains (a far larger ad and tracker list, for one), and `geoip:` covers every country by its two-letter code (`ir`, `de`, `us`, ...) plus lists such as `private`, `google`, `telegram`, `netflix`, `twitter` and `cloudflare`.

The rule wizard's website and address pickers include a country shortlist (Iran, Russia, China, Turkmenistan, Pakistan, and other restricted networks) and search every country in the installed data. Type a country name, adjective or code — `russia`, `russian`, `ru`, `turkmenistan`, `uae` — and the flags follow.

## Settings

```sh
masir settings                       # interactive
masir settings set mixedPort 7891
masir settings reset mixedPort
```

| Key | Default | What it does |
|---|---|---|
| `routingMode` | `proxy` | Where traffic no rule matches goes: `proxy` or `direct` |
| `mixedPort` | `7890` | The local proxy port, SOCKS5 and HTTP on one port |
| `allowLan` | `off` | Let other devices on your network use the proxy |
| `alwaysOnProxy` | `off` | Keep the proxy port up even while masir is off |
| `dohDns` | `https://1.1.1.1/dns-query` | DNS over HTTPS, reached through the proxy |
| `dnsServers` | `1.1.1.1, 8.8.8.8` | Plain DNS, sent direct, used to resolve proxy server names |
| `tunDns` | empty | DNS servers the TUN interface advertises; empty uses `dnsServers` |
| `gateway` | `10.10.20.1/24` | IPv4 range for the TUN interface |
| `subInterval` | `720` | Minutes between background subscription refreshes (5 to 43200) |
| `logLevel` | `warning` | Xray's own log level: `debug`, `info`, `warning`, `error`, or `none`; `masir logs` shows these |
| `analytics` | `on` | Send anonymous usage and error events to PostHog, once the first-run notice has been shown |

**The proxy has no authentication.** The mixed inbound (SOCKS5 + HTTP) on `mixedPort` is `noauth`: anyone who can reach the port can use your proxy. By default it listens on `127.0.0.1` only. With `allowLan on` it listens on every interface, so every device on your network can use it; keep it off on networks you don't trust.


## Commands

| Command | What it does |
|---|---|
| `masir connect [--proxy]` | Turn masir on: TUN mode, or proxy mode only with `--proxy` |
| `masir disconnect` | Turn masir off; the daemon keeps running |
| `masir status [--no-check] [--json]` | Daemon, Xray and live connectivity |
| `masir use [id] [--fastest]` | Switch the active server |
| `masir mode [proxy\|direct]` | Where traffic no rule matches goes |
| `masir sub` | List subscriptions with traffic and expiry; `add`, `update`, `rm`, `list --nodes` |
| `masir rules` | List rules; `add`, `edit`, `rm`, `test`, `check`, `enable`, `disable` |
| `masir settings` | An interactive settings menu; `list`, `get`, `set`, `reset` |
| `masir update [--check]` | Install the latest masir release |
| `masir update geo [--disable-broken-rules]` | Refresh geosite.dat and geoip.dat, unless that would break a rule |
| `masir update pause\|resume\|stop\|status` | Control an update in progress |
| `masir logs [--no-follow] [--level debug\|info\|warn\|error]` | Show recent daemon and Xray activity, then follow it |
| `masir get-config` | Print the exact Xray config masir runs |
| `masir daemon start\|stop\|restart\|enable\|disable` | Manage the daemon and the system service |
| `masir menu` | Every command, one pick away |

Every command has `--help` with examples.

## Right-to-left text

Masir automatically shapes and reorders Persian/Arabic text on terminals that don’t
do bidi themselves. On Windows it leaves the text to the terminal by default, because
the current Windows terminal handles the shaping and ordering itself.

Override the guess with `MASIR_BIDI`:

```powershell
# powershell
$env:MASIR_BIDI = "on"    # force masir's shaping/reordering
$env:MASIR_BIDI = "off"   # leave the text to the terminal
```

```sh
# unix
MASIR_BIDI=on masir use
MASIR_BIDI=off masir use
```

Use `on` if your Windows terminal shows reversed or disconnected Persian text. Use
`off` if your terminal already renders RTL correctly and Masir’s extra shaping is
reversing it.

## Privacy

Masir collects anonymous usage statistics and error reports to help improve it. Nothing is collected before a one-time notice on your first run. To turn it off, run `masir settings set analytics off` (or set `DO_NOT_TRACK=1`). The details are in [docs/telemetry.md](docs/telemetry.md).

## Scripting and the API

Output is plain text whenever it isn't going to a terminal, and status and rules speak JSON:

```sh
masir status --json | jq '.connectivity.proxy'
masir rules --json
masir use --fastest && masir connect --proxy
```

The daemon's gRPC API (`masir.daemon.v1.DaemonService`, defined in [`libs/masir-proto`](libs/masir-proto/proto/masir/daemon/v1/daemon.proto)) listens on a Unix socket (a named pipe on Windows), with server reflection enabled, so tools like [grpcurl](https://github.com/fullstorydev/grpcurl) work out of the box. Where the socket is depends on how the daemon runs, and neither needs root to connect:

- A daemon running as you (proxy mode, or before you've asked for TUN) listens on `daemon.sock` in the config folder.
- The root daemon (the system service, or a TUN session) listens on `/var/run/masir/<your uid>.sock`.

Only you and root may connect; the kernel checks who is on the other end.

```sh
SOCK="unix:///var/run/masir/$(id -u).sock"                              # root daemon
SOCK="unix://$HOME/Library/Application Support/masir/daemon.sock"      # daemon running as you (macOS)
grpcurl -plaintext "$SOCK" list
grpcurl -plaintext "$SOCK" masir.daemon.v1.DaemonService/GetStatus
```

The API covers everything the CLI does and more: custom inbounds and outbounds, live status streaming, log streaming, per-server latency tests, and update jobs with progress, pause, resume and stop.

## Security

Masir's daemon can run as root (for TUN), so it is built not to be a way for other software on your machine to become root. [SECURITY.md](SECURITY.md) explains what it protects, what it doesn't, and how to report a problem.

## Platform support

| | macOS | Linux | Windows |
|---|---|---|---|
| Proxy mode | yes | yes | yes |
| TUN mode | yes | yes | yes |
| System service | launchd | systemd | Windows service |
| Updates | yes | yes | yes |

On Windows, TUN runs on the [Wintun](https://www.wintun.net) driver, built into `masir.exe` and written beside the service's copy when it starts, and Masir keeps DNS queries inside the tunnel with Xray's Windows Filtering Platform rules, so lookups can't leak through other network adapters. Windows TUN support is new: if something doesn't work on your setup, please open an issue with the output of `masir logs`.

## Building from source

You need Go 1.27 or newer.

```sh
git clone https://github.com/Narixius/masir-go.git
cd masir-go
make build                  # dist/masir
```

For geo rules, put `geosite.dat` and `geoip.dat` next to the binary, or let `masir update geo` fetch them.
