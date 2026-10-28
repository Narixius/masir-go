# Updates

Two signed channels update masir: the daemon and command line from the release's minisign
`checksums.txt`, and the desktop app from MyGo's ed25519 manifest. This is how they run,
what they download, and how a change stays compatible with copies already installed.

## What asks, and when

| Who | When | Costs |
|---|---|---|
| the daemon | a minute after it starts, then hourly while its cache is a day old | one GitHub request, none while the cache is fresh |
| the app | on a status, when the daemon's cache is a day old | one request, from the daemon's cache |
| the app | when the person opens the panel (macOS) or focuses the window, at most every 10 minutes | a conditional request: a 304 when nothing changed |
| the app | the signed app feed, once per release it hasn't answered for, and every 6 hours | the feed resolution |

`CheckUpdate.max_age_ms` is how a UI says how old an answer it will accept. The daemon
answers from its cache when it is younger, asks GitHub otherwise, and never trusts a
release more than a day old: a longer ask is capped. GitHub's `ETag` is kept with the
cached release, so a check that finds nothing new is a `304` with no body. That saves the
download, not the quota: an unauthenticated request counts even when it answers `304`
(measured, not assumed). Six an hour is a tenth of GitHub's 60-an-hour limit for an IP,
and the app feed is read from `github.com`, which is not the API and has no such limit.

A hidden prompt (`IgnoreUpdate`) is for one version. It stays hidden across checks — a UI
that asks every ten minutes must not bring it back — and clears when a newer release is
published, or when the daemon restarts.

## What an update downloads

Every download is `<app>-<role>-<os>-<version>-<arch>`.

| Download | Holds |
|---|---|
| `masir-cli-<os>-<v>-<arch>.tar.gz` | masir and the geo data: a whole install |
| `masir-cli-update-<os>-<v>-<arch>.tar.gz` | only masir: what an update installs |
| `masir-desktop-portable-<os>-<v>-<arch>.tar.gz` | the app, on macOS and Linux; also the archive its updater reads |
| `masir-desktop-update-windows-<v>-<arch>.tar.gz` | the app on Windows, where the portable download is a `.zip` |
| `masir-desktop-<from>-to-<v>-<os>-<arch>.delta` | the change from the previous app release |

The update job prefers the thin command line archive and falls back to the full one, so a
release published before the thin archive existed still updates. A delta that doesn't
apply to the installed app falls back to the whole archive; the manifest lists both.

## What an update installs

With the service installed, the app runs the daemon job first: it downloads, verifies,
unpacks, asks the new binary whether it can build the enabled rules, replaces the daemon,
the service's copy and (when privileged) the CLI's copy, restarts it, and waits for the
new version and the tunnel. Then the app installs its own signed archive and relaunches.
With no service, the daemon runs from the app bundle, which only the app updater writes:
the app update is the update, and the daemon restarts into the new bundle.

The app bundle has one writer (MyGo's updater). A daemon or CLI inside `*.app` refuses to
replace itself and says to update the app.

## Compatibility rules

A release must work with the copies already installed, in both directions:

- **Protocol**: additive fields only, never reuse a field number, and a new enum value
  only reachable when a client asks for it. `buf breaking` in `make proto-lint` enforces
  the field rules.
- **Hints, not requirements**: `max_age_ms` is a hint. A daemon that predates it ignores
  the field and answers from its daily cache, which is what the app did before the hint
  existed. `defer_restart` is only ever set by a GUI that handles a daemon which restarts
  anyway (an older daemon ignores the field and restarts at the end of the job).
- **Asset names have fallbacks**: a client asks for the exact name, then for the older
  name it knows. No name a shipped client knows is ever retired.
- **Every asset is signed**: new downloads join `checksums.txt` (minisign) or
  `checksums-desktop.txt` (minisign). Deltas are covered by both the signed manifest and
  the desktop list (they are named `masir-desktop-…`). The app's manifest is signed with
  the MyGo ed25519 key. A build without its key refuses updates rather than installing an
  unverified one.
- **One version number**: `mygo.json` and the release tag are the same number, so the app
  can key its feed check on the release the daemon reports. The 6-hour fallback keeps that
  from being load-bearing if the two ever diverge.

## Verifying a release by hand

```sh
minisign -V -P <MASIR_UPDATE_PUBKEY> -m checksums.txt -x checksums.txt.minisig
shasum -a 256 -c checksums.txt

minisign -V -P <MASIR_UPDATE_PUBKEY> -m checksums-desktop.txt -x checksums-desktop.txt.minisig
shasum -a 256 -c checksums-desktop.txt
```

## Changing this

- A new artifact: add it to the release, to a signed checksum list, and to the name table
  above; give it a role (`portable`, `update`, …) rather than a bare name.
- A new behaviour the GUI asks for: a new field, plus the behaviour without it. Test the
  old-daemon path with the fakes in `apps/masir-gui/updates_test.go`.
- A new signing key: `publicKeys` in the daemon accepts a comma-separated list, so add the
  new key beside the old one for a release before removing the old one.
