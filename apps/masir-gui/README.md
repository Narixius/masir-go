# Masir for macOS

The menu bar app for the masir daemon. It is a native app (MyGo): a tray icon whose panel
turns masir on and off, picks the server, and shows the rules. All the logic lives in the
daemon; the app only talks to it.

## Build and install

```sh
make gui
```

builds `dist/darwin-arm64/Masir.app` (and a `.dmg`) for this machine, with the `masir`
executable and the geo data inside it (in `Contents/Resources/bin`). Drag it to Applications, or:

```sh
cp -R apps/masir-gui/dist/darwin-arm64/Masir.app /Applications/
```

The app is signed ad hoc. If macOS refuses to open it the first time, right-click it and
choose Open. To open it at login, turn on Open at Login in its settings.

The app uses the daemon that is already running (the service, or one it started before);
if none answers it starts the `masir` inside the app.

## Other platforms

Cross-building needs no extra tools; run these from the repository root on any machine.

| Command | Output (in `apps/masir-gui/dist/`) |
|---|---|
| `make gui-windows` | `windows-amd64/`: `Masir.exe` with `bin/masir.exe`. The installer is made on Windows. |
| `make gui-linux` | `linux-amd64/`: the executable, a `.deb`, a tarball and `install.sh` (`GUI_LINUX_ARCH=arm64` for ARM). |

The tunnel needs administrator rights. On Windows the app asks through the system prompt. On
Linux it cannot ask yet: install the service once with `sudo masir daemon enable`, or start the
daemon as root yourself.

## Run from source

```sh
make gui-dev
```

builds `masir` into `dist/` and runs the app against it, rebuilding as you edit. Set
`MASIR_DEMO=servers|rules|settings|add` to open that part of the panel a moment after launch.

## Tests

```sh
cd apps/masir-gui && go test ./...
MASIR_SNAPSHOTS=/tmp/masir-snaps go test -run Snapshots ./...   # pictures of each page
```
