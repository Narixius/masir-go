# masir

**The Xray client that routes the way you think.**

Decide, app by app and site by site, what goes through the proxy and what doesn't, with the full power of Xray's routing, and without restarting the core every time you change something. Subscriptions, server switching and updates take care of themselves.

**[Download the latest release](https://github.com/Narixius/masir-go/releases/latest)**

```text
$ masir status

Masir Daemon 0.1.0 ● Active (privileged, service)
├─ Serving: you (uid 501)
└─ Uptime:  3h 12m (since 2026-10-25 09:14:02)

Xray Core v26.10.21 ● Active
├─ Mode:         TUN (system-wide routing)
├─ Routing Mode: direct (unmatched traffic skips the proxy)
├─ Proxy Port:   0.0.0.0:7890 (LAN allowed)
├─ DoH DNS:      https://1.1.1.1/dns-query
├─ DNS Servers:  1.1.1.1, 8.8.8.8
└─ Geo Data:     2026-10-24 (1d ago)

Connectivity
├─ Internet: ✔ 92ms
└─ Proxy:    Frankfurt 2  ✔ 168ms
✔ Online, and the active proxy works.
```

## Why masir

**Route by app.** One rule sends a whole app through the proxy or keeps it direct, helper processes included. Pick it from a list of your installed apps.

**Full Xray routing.** Every rule is a real Xray routing rule: domains, `geosite:`, IPs, `geoip:`, ports, protocols, processes, or raw JSON. Xray checks each rule before it's saved.

**Live changes, no restarts.** Rules, server switches and pins are applied to the running core through Xray's own gRPC API. Xray keeps running while you tweak, so nothing else drops.

**Pin rules to any server.** Send one service through one server, from any subscription, whatever the active server is. If that server disappears, the rule falls back instead of breaking.

**Subscriptions on autopilot.** JSON configs and share links (`vless://`, `vmess://`, `trojan://`, `ss://`, `hysteria2://`, ...), refreshed on schedule, with traffic and expiry shown, fetched through the proxy when the provider is blocked.

**Live latency.** The server picker tests what's on screen as you scroll. `l` retests one, `a` retests all, or let `masir use --fastest` pick.

**TUN or proxy, your choice.** TUN routes the whole system without touching your DNS settings; proxy mode gives you SOCKS5 and HTTP on one port, shareable over your LAN.

**Keeps itself current.** `masir update` and `masir update geo` install verified releases and fresh geo data, with a compact progress bar, pause, resume and stop. If your copy of masir is root-owned, the daemon updates it when it has the rights; otherwise masir asks for sudo once, and leaves a copyable command if you decline.

**Scriptable, and built to be built on.** Plain output when piped, `--json` where it matters, smart shell completions, and a gRPC API for your own tools or a GUI.

## Philosophy

Routing is what Xray does best. Its rules can send each connection exactly where it should go: this app through the proxy, that site direct, ads nowhere, one service through the one server that works for it. Most Xray clients barely expose any of that. They start Xray system-wide, push all traffic through the tunnel, and hide routing behind a screen that's hard to find and harder to configure. Doing anything beyond the defaults means hand-editing JSON, and switching servers often restarts the core and drops every connection.

masir started from the opposite end. Routing comes first: rules are easy to write and easy to read, and nothing about them is watered down, so anything Xray can match, masir can route. The core stays up while you change things, because Xray's own API can apply changes live. Servers from several subscriptions sit side by side, and any rule can point at any of them. And the tool should feel good to use every day: fast to pick a server, clear about what's running, quiet when everything works.

It's the client I wanted for myself, for living with Xray configs and moving between many servers. If you've felt the same friction, it's probably the client you wanted too.

## Install

1. Download the archive for your system from the **[releases page](https://github.com/Narixius/masir-go/releases/latest)**:

   | System | Archive |
   |---|---|
   | macOS, Apple Silicon | `masir_<version>_darwin_arm64.tar.gz` |
   | macOS, Intel | `masir_<version>_darwin_amd64.tar.gz` |
   | Linux, x86-64 | `masir_<version>_linux_amd64.tar.gz` |
   | Linux, ARM64 | `masir_<version>_linux_arm64.tar.gz` |
   | Windows, x86-64 | `masir_<version>_windows_amd64.zip` |
   | Windows, ARM64 | `masir_<version>_windows_arm64.zip` |

   Each archive holds the `masir` executable plus `geosite.dat` and `geoip.dat`. Keep the three files together. (On Windows, the [Wintun](https://www.wintun.net) driver TUN mode needs is built into `masir.exe`.)

2. Put them in a folder on your `PATH`, for example:

   ```sh
   mkdir -p ~/.local/masir
   tar -xzf masir_*_darwin_arm64.tar.gz -C ~/.local/masir
   ln -s ~/.local/masir/masir ~/.local/bin/masir
   ```

   On Windows, extract the zip into a folder such as `%LOCALAPPDATA%\masir` and add that folder to your `PATH`.

3. On macOS, if the archive came through a browser, clear the download quarantine once:

   ```sh
   xattr -d com.apple.quarantine ~/.local/masir/masir
   ```

4. Optional: enable shell completions.

   ```sh
   masir completion zsh > "${fpath[1]}/_masir"    # or bash, fish, powershell
   ```

That's it. There's nothing separate to install for the daemon: masir starts it in the background when a command needs it.

## Quick start

```sh
masir sub add https://example.com/your-subscription    # add a subscription
masir use                                              # pick a server, with live latency
masir connect                                          # turn masir on (TUN mode)
masir status                                           # see what's running
```

`masir connect` uses TUN mode, which needs root (Administrator rights on Windows). If its daemon isn't running with those rights, masir starts it that way for the session: sudo asks for your password on macOS and Linux (unless it remembers you), Windows shows its permission prompt. To never be asked again, run `masir daemon enable` once: it installs the daemon as a system service that starts at boot. Prefer not to? `masir connect --proxy` runs proxy mode only, as your normal user.

Then make it yours:

```sh
masir rules add                                        # the guided rule wizard
masir mode proxy                                       # send everything no rule matches through the proxy
```

Run `masir` with no arguments for the full command list, or `masir menu` for an interactive menu of everything.

## Routing rules

Rules are checked top to bottom; the first match wins. Traffic no rule matches goes to the **routing mode**: `direct` (the default) or `proxy`, switched with `masir mode`. Local network traffic and DNS are handled by built-in rules, so you never lock yourself out.

Each rule sends its traffic to one of:

- `proxy`: the active server
- `direct`: straight out, skipping the proxy
- `block`: dropped
- a specific server, from any subscription (`--via <server id>`)
- a custom Xray outbound (`outbound:<tag>`)

```sh
# Iranian websites and addresses go direct
masir rules add --domain geosite:category-ir --ip geoip:ir -o direct

# Russian websites go through the proxy
masir rules add --domain geosite:category-ru -o proxy

# Telegram, with all its helper processes, through the proxy
masir rules add --app /Applications/Telegram.app/ -o proxy

# Netflix always through one specific server, whatever the active one is
masir rules add --domain geosite:netflix --via 737e3ea15f56

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

Manage them with `masir rules` (list), `masir rules edit <name>` (an interactive editor, or flags such as `--rename`, `--append`, `--unset`), `masir rules rm`, and `masir rules test <domain|ip>` to see exactly where a destination would go right now.

Geo data comes from [Loyalsoldier/v2ray-rules-dat](https://github.com/Loyalsoldier/v2ray-rules-dat), refreshed daily upstream: `geosite:` is the v2fly domain list with hundreds of thousands of extra domains (a far larger ad and tracker list, for one), and `geoip:` covers every country by its two-letter code (`ir`, `de`, `us`, ...) plus lists such as `private`, `google`, `telegram`, `netflix`, `twitter` and `cloudflare`.

The rule wizard's website and address pickers include a country shortlist (Iran, Russia, China, Turkmenistan, Pakistan, and other restricted networks) and search every country in the installed data. Type a country name, adjective or code — `russia`, `russian`, `ru`, `turkmenistan`, `uae` — and the flags follow.

## Commands

| Command | What it does |
|---|---|
| `masir connect [--proxy]` | Turn masir on: TUN mode, or proxy mode only with `--proxy` |
| `masir disconnect` | Turn masir off; the daemon keeps running |
| `masir status [--no-check] [--json]` | Daemon, Xray and live connectivity |
| `masir use [id] [--fastest]` | Switch the active server |
| `masir mode [proxy\|direct]` | Where traffic no rule matches goes |
| `masir sub` | List subscriptions with traffic and expiry; `add`, `update`, `rm`, `list --nodes` |
| `masir rules` | List rules; `add`, `edit`, `rm`, `test` |
| `masir settings` | An interactive settings menu; `list`, `get`, `set`, `reset` |
| `masir update [--check]` | Install the latest masir release |
| `masir update geo` | Refresh geosite.dat and geoip.dat |
| `masir update pause\|resume\|stop\|status` | Control an update in progress |
| `masir logs [--no-follow] [--level debug\|info\|warn\|error]` | Show recent daemon and Xray activity, then follow it |
| `masir get-config` | Print the exact Xray config masir runs |
| `masir daemon start\|stop\|restart\|enable\|disable` | Manage the daemon and the system service |
| `masir menu` | Every command, one pick away |

Every command has `--help` with examples.

## Settings

```sh
masir settings                       # interactive
masir settings set mixedPort 7891
masir settings reset mixedPort
```

| Key | Default | What it does |
|---|---|---|
| `routingMode` | `direct` | Where traffic no rule matches goes: `proxy` or `direct` |
| `mixedPort` | `7890` | The local proxy port, SOCKS5 and HTTP on one port |
| `allowLan` | `on` | Let other devices on your network use the proxy |
| `alwaysOnProxy` | `off` | Keep the proxy port up even while masir is off |
| `dohDns` | `https://1.1.1.1/dns-query` | DNS over HTTPS, reached through the proxy |
| `dnsServers` | `1.1.1.1, 8.8.8.8` | Plain DNS, sent direct, used to resolve proxy server names |
| `tunDns` | empty | DNS servers the TUN interface advertises; empty uses `dnsServers` |
| `gateway` | `10.10.20.1/24` | IPv4 range for the TUN interface |
| `subInterval` | `720` | Minutes between background subscription refreshes (5 to 43200) |
| `logLevel` | `warning` | Xray's own log level: `debug`, `info`, `warning`, `error`, or `none`; `masir logs` shows these |
| `analytics` | `on` | Send anonymous usage and error events to PostHog |

## Privacy and analytics

masir sends anonymous usage and error events to PostHog (EU region) to make the defaults and failure handling better. It is on by default, and a one-time notice appears on first interactive use. Turn it off with:

```sh
masir settings set analytics off
```

`DO_NOT_TRACK=1` and `MASIR_ANALYTICS=off` also disable it. Development builds don't send unless `MASIR_ANALYTICS=on` and a key is configured.

**What is sent**

- Anonymous install ID (random, not a machine or account ID)
- Command names, success/failure, duration, and flag names — never values like domains, URLs or rule contents
- Error kinds, fingerprints, and scrubbed messages for panics/unexpected daemon errors
- App, Xray, OS, architecture, and Go versions (OS/version/library context is added by PostHog's Go SDK)
- Local timezone and UTC offset (for regional grouping; no IP geolocation)
- Active config country parsed from its flag emoji, plus protocol/transport/security
- Safe settings: routing fallback, TUN/LAN/always-on, proxy port and whether it's default, Xray log level, subscription interval, DNS/DoH provider categories and counts
- Counts of rules, inbounds, outbounds, subscriptions, and configs
- Update, subscription refresh, latency, Xray start/stop/apply-failure, and daemon start/stop events

**What is never sent**

- Subscription URLs or names
- Config names, server addresses, ports, UUIDs, passwords
- Rule domains, IPs, process names or app paths
- Raw DNS/DoH addresses or URLs
- Local file paths, usernames, hostnames, or raw logs
- IP-based geolocation (PostHog GeoIP is disabled)

Analytics tries a direct connection first and falls back to the running proxy when the direct route is blocked. Events captured while the daemon is unavailable wait in a local spool and are sent on the next daemon start. Person profiles are deliberately disabled (`$process_person_profile: false`), so events are anonymous and don't create PostHog person profiles.

For local builds, put the public PostHog project token in `.env`:

```sh
MASIR_POSTHOG_KEY=
MASIR_POSTHOG_HOST=https://eu.i.posthog.com
```

`make build` and `make snapshot` read `.env`. Release builds read `MASIR_POSTHOG_KEY` and `MASIR_POSTHOG_HOST` from repository Actions variables; without a key, analytics is a no-op.

## How it works

masir is one executable with two roles:

- **The CLI**, which you use.
- **The daemon** (`masir daemon run`), which owns everything else: Xray, your settings, rules and subscriptions, and the gRPC API every client talks to.

When a command needs the daemon and none is running, masir starts it in the background. A daemon started this way stays idle, managing subscriptions, rules and updates without starting Xray, until you connect. For TUN mode, `masir connect` starts the daemon as root (or Administrator) for the session, and `masir daemon enable` installs a copy only root or Administrators can change (`/usr/local/lib/masir`, or `Program Files\masir` on Windows) as a launchd, systemd or Windows service. It serves only you: the daemon's socket (a named pipe on Windows) accepts connections from root or Administrators and from the user it serves, nobody else.

Xray runs embedded in the daemon. masir composes its configuration from your state, writes the result to `xray.json` for you to inspect, and applies changes live through Xray's gRPC API whenever possible.

Everything lives in one folder:

| System | Location |
|---|---|
| macOS | `~/Library/Application Support/masir` |
| Linux | `~/.config/masir` |
| Windows | `%AppData%\masir` |

## Scripting and the API

Output is plain text whenever it isn't going to a terminal, and status and rules speak JSON:

```sh
masir status --json | jq '.connectivity.proxy'
masir rules --json
masir use --fastest && masir connect --proxy
```

The daemon's gRPC API (`masir.daemon.v1.DaemonService`, defined in [`libs/masir-proto`](libs/masir-proto/proto/masir/daemon/v1/daemon.proto)) listens on `daemon.sock` in the config folder, with server reflection enabled, so tools like [grpcurl](https://github.com/fullstorydev/grpcurl) work out of the box:

```sh
SOCK="unix://$HOME/Library/Application Support/masir/daemon.sock"
grpcurl -plaintext "$SOCK" list
grpcurl -plaintext "$SOCK" masir.daemon.v1.DaemonService/GetStatus
```

The API covers everything the CLI does and more: custom inbounds and outbounds, live status streaming, log streaming, per-server latency tests, and update jobs with progress, pause, resume and stop.

## Platform support

| | macOS | Linux | Windows |
|---|---|---|---|
| Proxy mode | yes | yes | yes |
| TUN mode | yes | yes | yes (new in 0.1.1) |
| System service | launchd | systemd | Windows service |
| Updates | yes | yes | yes |

On Windows, TUN runs on the [Wintun](https://www.wintun.net) driver, built into `masir.exe` and written beside the service's copy when it starts, and masir keeps DNS queries inside the tunnel with Xray's Windows Filtering Platform rules, so lookups can't leak through other network adapters. Windows TUN support is new: if something doesn't work on your setup, please open an issue with the output of `masir logs`.

## Building from source

You need Go 1.27 or newer.

```sh
git clone https://github.com/Narixius/masir-go.git
cd masir-go
make build                  # dist/masir
make snapshot               # release archives for every platform, into build/release
```

For geo rules, put `geosite.dat` and `geoip.dat` next to the binary, or let `masir update geo` fetch them.

Releases are built by GitHub Actions: pushing a `v*` tag publishes archives for every platform, and every published release is what `masir update` installs.
