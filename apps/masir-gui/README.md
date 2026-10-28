# Masir desktop app

The tray app for the masir daemon. It is native UI (MyGo): one tray icon on every OS, whose
panel turns masir on and off, picks the server with live latency, shows the rules, adds
subscriptions, and has settings. All the logic lives in the daemon; the app only talks to
it.

- **macOS**: the icon opens the system's popover, anchored to the menu bar.
- **Windows and Linux**: the icon opens the app's own window, with a title bar and a
  taskbar entry. Closing it puts it back in the tray. The user cannot resize it; the
  window follows the height of its page and its collapsible lists.

The app ships the `masir` executable and the geo data inside it (in a `bin` folder of its
resources), so it can start the daemon when none is running. Country flags are drawn from
bundled SVGs rather than emoji, so they look the same on every OS, including Windows, whose
emoji font has no flags. The daemon runs as the user, which is all proxy mode needs; tunnel
mode asks for root through the OS prompt (the system password dialog on macOS and Linux,
the UAC prompt on Windows). Settings can install the daemon as a background service. The
`masir` command-line tool installs itself on first launch: a link in `~/.local/bin` on
macOS and Linux, with a marked PATH block added to the user's shell config when needed, or
the app's `bin` folder on the user's PATH on Windows. Removing it in Settings records an
opt-out, so a later launch does not put it back. `MASIR_CLI_INSTALL=1` forces the install
from a development build, and `MASIR_NO_CLI_INSTALL=1` turns it off.

## Build

```sh
make gui-local                             # this machine only: the fast build while developing
# MASIR_GEO_SKIP=1 skips the geo checksum check for an offline build with good files
make gui                                   # darwin, linux and windows; amd64 and arm64
make gui GUI_OS=darwin                     # one system, both architectures
make gui GUI_ARCHES=arm64                  # all systems, one architecture
make gui GUI_OS=windows GUI_ARCHES=arm64   # one build
```

`GUI_OS` is `darwin`, `linux` and/or `windows`; `GUI_ARCHES` is `amd64` and/or `arm64`.
Space or comma separated; the default is all six builds. Each goes to
`apps/masir-gui/dist/<os>-<arch>`, with the `masir` executable and the geo data in the
`bin` folder of the resources it ships.

Every download is named `<app>-<role>-<os>-<version>-<arch>`:

- `masir-desktop-<os>-<version>-<arch>.dmg` (macOS, drag to Applications),
  `masir-desktop-setup-windows-<version>-<arch>.exe` and
  `masir-desktop-<os>-<version>-<arch>.deb` are the installers;
- `masir-desktop-portable-<os>-<version>-<arch>.<ext>` is the archive a person unpacks and
  runs: `.tar.gz` on macOS and Linux, `.zip` on Windows (the release workflow packs it);
- `masir-desktop-update-windows-<version>-<arch>.tar.gz` is the Windows archive MyGo's
  updater reads. On macOS and Linux the portable tarball is that archive too: the same
  bytes serve both, so nobody downloads the app twice;
- `masir-cli-<os>-<version>-<arch>.tar.gz` (masir and the geo data, a whole install) and
  `masir-cli-update-<os>-<version>-<arch>.tar.gz` (masir only, what `masir update` asks
  for).

`update-<os>-<arch>.json` is the signed manifest the app reads, and it points at the
archive above, so the names are ours to choose. `deltas: 1` in `mygo.json` makes the build
publish a `masir-desktop-<from>-to-<version>-<os>-<arch>.delta` from the previous release,
which is what an update downloads when it applies; a delta that doesn't apply falls back to
the whole archive.

The build applies the patches in `patches/` to MyGo for its duration. Windows stretched a
frame drawn for one client height into a window that has changed height, which made the
content magnify during the animated height changes; `mygo-dxgi-no-stretch.patch` makes the
swap chain show the stale frame unscaled and fills the rest with the window's background.
The macOS popover animated a content size change on AppKit's own schedule, so the panel's
height ran ahead of the lists it holds; `mygo-darwin-popover-resize.patch` turns that
animation off so the shell can follow the springs every frame, as the other platforms do.
The patched copy is made in `.mygo-patched` and removed, and `go.mod`/`go.sum` are put
back, after the build. `make gui-dev` uses stock MyGo; if MyGo is updated, the patches may
need updating.

The app updates itself from MyGo's signed release archives. The public key is
`updates.publicKey` in `mygo.json`; a release build also needs the matching private key
in `MYGO_UPDATER_PRIVATE_KEY` (or `.env`). Without it the app builds, but cannot update
itself. Generate a pair once with `cd apps/masir-gui && go tool mygo keygen`, put the
public key in `mygo.json`, keep the private key secret, and back it up: installed apps
accept only updates signed with it.

The app is ad hoc code signed (macOS runs it; Gatekeeper still asks on first launch). The
background service is installed as `Masir` — Login Items falls back to a legacy daemon's
executable name — and the app's icon is attached to that binary as a Finder resource fork
(`AppIcon.rsrc` and `FinderInfo.bin`), so the service shows as Masir with the Masir icon
without a Developer ID. `AssociatedBundleIdentifiers` stays in the plist for a signed
build. Updates re-apply the icon to the new binary. `resources/AppIcon.rsrc` and
`resources/FinderInfo.bin` are generated from `AppIcon.icns` with `sips -i`, `DeRez`,
`Rez` and `SetFile`; regenerate them if the icon changes. The security of an update does
not come from that signature: the app archive (or its delta) is verified with
the MyGo ed25519 key, and the command line/daemon archives with the release minisign key
(`MASIR_UPDATE_PUBKEY`, signed `checksums.txt` and `checksums-desktop.txt`). A build
without a key refuses updates instead of installing an unverified one;
`MASIR_UPDATE_ALLOW_UNSIGNED=1` is only for development. The app bundle is the one copy
the app updater writes: the daemon and CLI refuse to replace a file inside `*.app`. When
the app updates, a daemon running from the old bundle restarts into the new one before the
app relaunches, and a daemon running as the service updates itself from the signed
release. The previous download stays verifiable with:

```sh
minisign -V -P <MASIR_UPDATE_PUBKEY> -m checksums-desktop.txt -x checksums-desktop.txt.minisig
shasum -a 256 -c checksums-desktop.txt
```

`docs/updates.md` has the whole flow: what the app asks and when, what each job installs,
and which combinations of app and daemon versions work together.

`docs/updates.md` has the whole flow: what the app checks and when, what each job
installs, and which combinations of app and daemon versions work together.

The macOS build is `dist/darwin-<arch>/Masir.app` (and a `.dmg`); drag it to Applications,
or:

```sh
cp -R apps/masir-gui/dist/darwin-arm64/Masir.app /Applications/
```

The app is signed ad hoc. If macOS refuses to open it the first time, right-click it and
choose Open.

The Windows build is `dist/windows-<arch>` with `Masir.exe` and `bin/masir.exe`; the
installer is made on Windows. The Linux build is `dist/linux-<arch>` with the executable,
a Debian package, a portable tarball, and the desktop entry (`install.sh` is written beside
them); the app installs for one user, without root.

## Run from source

```sh
make gui-dev
```

builds `masir` into `dist/` and runs the app against it, rebuilding as you edit. Set
`MASIR_DEMO=servers|rules|settings|add` to open that part of the panel a moment after
launch, or `MASIR_DEMO=cycle` to go between the main page and settings on repeat (useful
for watching the transition and its window resizing), and `MYGO_FRAME_STATS=1` to log
frames that take longer than 8 ms.

## Tests

```sh
cd apps/masir-gui && go test ./...          # the module's tests
make test                                   # vet and test every module, as CI does
make test-race                              # the same with the race detector
make screenshots                            # pictures of each page, in dist/snapshots
```
