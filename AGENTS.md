# Working in this repository

Masir is a Go workspace (`go.work`, Go 1.27.3). The desktop app is native MyGo, not a web
page. macOS arm64 is the primary development machine; Windows and Linux have their own
window implementation.

## Map

- `apps/masir` — the command-line app; `masir daemon run` is the daemon.
- `apps/masir-gui` — the tray app and its MyGo UI. This is where most UI work happens.
- `libs/masir-daemon` — daemon, routing, subscriptions, updates.
- `libs/masir-core` — client and shared core.
- `libs/masir-proto` — protobuf definitions; `make proto` regenerates.
- `patches/` — MyGo patches the GUI build applies.
- `scripts/build-gui.sh` — the desktop build for every OS and architecture.

## Fast loop

Use the smallest loop that proves the change; save the full matrix for before a commit or
a release.

```sh
cd apps/masir-gui && go test ./...                 # the module, cached
cd apps/masir-gui && go test -run TestName -v ./... # one test
make gui-local                                     # one app for this machine
make snapshots                                     # PNGs of each page in dist/snapshots
make test                                          # vet + tests for every module
make test-race                                     # the same with -race
```

- `make gui-local` builds only `go env GOOS`/`GOARCH`. Use it constantly.
- `make gui` builds all six targets and is slow; use it when touching build scripts,
  patches, platform code, or before a release.
- `make gui-dev` uses **stock** MyGo, so it does not include the patches in `patches/`.
  Test behavior that depends on a patch with `make gui-local`, not `make gui-dev`.
- Cross-compile cheaply inside the module:
  `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...`.
- Changing protobufs: `make proto`, then `make proto-lint` before committing.

## Looking at the UI without clicking

```sh
make snapshots
# or
cd apps/masir-gui
MASIR_SNAPSHOTS=/tmp/masir-snaps go test -count=1 -run TestSnapshots -v
```

`TestSnapshots` only writes images; it asserts nothing. Read the PNGs to check spacing,
centering, opacity, and theme colors. Snapshot images are also the fastest way to review a
layout change without launching the app.

For real frames and animations:

```sh
MYGO_FRAME_STATS=1   # log frames over 8 ms
MYGO_FRAME_STATS=all # log every frame
MASIR_DEMO=rules     # open the rules panel after launch
MASIR_DEMO=settings  # open settings
MASIR_DEMO=cycle     # alternate main and settings
```

Launch a built app with `MASIR_APP_NAME=MasirSmoke` so its single-instance lock, tray, and
cache do not collide with a running Masir. On macOS the executable is
`apps/masir-gui/dist/darwin-arm64/Masir.app/Contents/MacOS/Masir`. Run it for a few seconds,
capture stderr, and stop it. Demos only open pages; do not toggle daemon settings or change
subscriptions while smoke-testing unless that is the change under test.

## GUI invariants and sharp edges

- **Never do heavy work in a frame.** Parse rule JSON once (`ruleMeta`), cache searchable
  text, prefetch app icons one at a time, and keep RPC/CLI/OS checks asynchronous on
  `a.async` + `a.post`. Lists must virtualize with `ui.List`; do not build thousands of
  rows to show twenty.
- **Inputs belong inside their box.** Build a container, then create its input inside
  `box.Children(func() { ... })`. An input created just before `box.Children` is a sibling
  of the box, so its text is positioned somewhere else. This has bitten the port field and
  the add-subscription form.
- **Pinned list headers need an opaque background.** `p.subdued` is translucent; rows
  scrolling under a header use `p.listHeader`. The same applies to server group headers.
- **Warm a page in its own engine.** Do not call `ui.NewTester` in production to prewarm:
  it shapes text for another engine. A new page is built once in an unseen frame
  (`warmPage`), before the slide starts. Tests with `motion.instant = true` skip that hold
  frame.
- **Animate from the frame clock.** Springs step in `aimMotion` from `c.Now()`; use
  `c.AnimationFrame`, not timers. Height resizes are coalesced by the shell; do not resize
  from inside a frame.
- **Height follows the springs on every platform.** macOS needs
  `patches/mygo-darwin-popover-resize.patch` to stop AppKit animating its own resize. Keep
  `mygo-dxgi-no-stretch.patch` for Windows. If MyGo is updated, verify both patches still
  apply and run `make gui-local`.
- **Flags are 1:1** in tiles (`Fit(ui.Cover)`, rounded corners). Use `iconTile` for flags
  and app icons so both stay centered; `TestIconTileCentersWhatItHolds` guards it.
- **Strict list keys.** MyGo panics in tests when two rows under one parent share a key.
  When adding fixtures to the existing `c1`/`c2` configs, use distinct IDs such as `x%d`.

## Tests

- CI runs `go vet ./...` and `go test ./...` per module; the race detector runs everywhere
  except Windows. `make test` and `make test-race` mirror that locally.
- The GUI race suite takes about 15 seconds. Run it before committing GUI changes.
- The first headless frame includes engine and font initialization; do not benchmark from
  frame 1. Compare later frames, or run a second measurement after warmup.
- Use `newTestApp`, `tester`, `ukServers`, `manyRules`, and `manyConfigs`. `tester` is the
  headless `ui.NewTester`; `a.motion.instant = true` skips animation and is the default in
  those helpers.
- To test animation or height, set `a.motion.instant = false`, `tt.Frame()` to start, then
  `tt.Advance(...)`. Account for the unseen page-building frame before a page slide.
- Snapshots do not assert. If a visual rule matters, add a layout test (as with tiles) or
  state the snapshot expectation in the commit; do not rely on a golden pixel test.

## Commits

Before committing:

```sh
cd apps/masir-gui && gofmt -w . && go vet ./... && go test ./...
make test-race          # or at least the GUI module with -race
make gui-local          # when patches, platform code, or packaging changed
```

Keep `dist/`, `.mygo-patched`, generated binaries, and `.env` out of commits. Use an
imperative subject; explain the user-visible reason and any platform/patch consequence in
the body. Mention when `make gui` (all six targets) was checked for cross-platform changes.
