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
the UAC prompt on Windows). Settings can install the daemon as a background service, and
install the `masir` command-line tool for the terminal (a link in `~/.local/bin`, or the
app's `bin` folder on the user's `PATH` on Windows).

## Build

```sh
make gui                                   # darwin, linux and windows; amd64 and arm64
make gui GUI_OS=darwin                     # one system, both architectures
make gui GUI_ARCHES=arm64                  # all systems, one architecture
make gui GUI_OS=windows GUI_ARCHES=arm64   # one build
```

`GUI_OS` is `darwin`, `linux` and/or `windows`; `GUI_ARCHES` is `amd64` and/or `arm64`.
Space or comma separated; the default is all six builds. Each goes to
`apps/masir-gui/dist/<os>-<arch>`, with the `masir` executable and the geo data in the
`bin` folder of the resources it ships.

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

The macOS build is `dist/darwin-<arch>/Masir.app` (and a `.dmg`); drag it to Applications,
or:

```sh
cp -R apps/masir-gui/dist/darwin-arm64/Masir.app /Applications/
```

The app is signed ad hoc. If macOS refuses to open it the first time, right-click it and
choose Open.

The Windows build is `dist/windows-<arch>` with `Masir.exe` and `bin/masir.exe`; the
installer is made on Windows. The Linux build is `dist/linux-<arch>` with the executable,
a Debian package, a tarball with `install.sh`, and the desktop entry; the app installs for
one user, without root.

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
cd apps/masir-gui && go test ./...
MASIR_SNAPSHOTS=/tmp/masir-snaps go test -run Snapshots ./...   # pictures of each page
```
