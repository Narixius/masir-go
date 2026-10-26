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
resources), so it can start the daemon when none is running. The daemon runs as the user,
which is all proxy mode needs; tunnel mode asks for root through the OS prompt (the
system password dialog on macOS and Linux, the UAC prompt on Windows). Settings can install
the daemon as a background service, and install the `masir` command-line tool for the
terminal (a link in `~/.local/bin`, or the app's `bin` folder on the user's `PATH` on
Windows).

## Build

```sh
make gui           # this machine's architecture, in apps/masir-gui/dist/darwin-<arch>
make gui-windows   # windows-amd64 (GUI_WIN_ARCH=arm64 for ARM)
make gui-linux     # linux-amd64 (GUI_LINUX_ARCH=arm64 for ARM)
```

`make gui` builds `dist/darwin-<arch>/Masir.app` (and a `.dmg`) with `masir` and the geo
data in `Contents/Resources/bin`. Drag it to Applications, or:

```sh
cp -R apps/masir-gui/dist/darwin-arm64/Masir.app /Applications/
```

The app is signed ad hoc. If macOS refuses to open it the first time, right-click it and
choose Open.

On Windows, `dist/windows-<arch>` holds `Masir.exe` and `bin/masir.exe`; the installer is
made on Windows. On Linux, `dist/linux-<arch>` holds the executable, a Debian package, a
tarball with `install.sh`, and the desktop entry; the app installs for one user, without
root.

## Run from source

```sh
make gui-dev
```

builds `masir` into `dist/` and runs the app against it, rebuilding as you edit. Set
`MASIR_DEMO=servers|rules|settings|add` to open that part of the panel a moment after
launch.

## Tests

```sh
cd apps/masir-gui && go test ./...
MASIR_SNAPSHOTS=/tmp/masir-snaps go test -run Snapshots ./...   # pictures of each page
```
