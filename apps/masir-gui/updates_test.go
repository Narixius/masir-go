package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

func releaseInfo(current, latest string) *pb.ReleaseInfo {
	return &pb.ReleaseInfo{
		CurrentVersion: current,
		LatestVersion:  latest,
		Available:      latest != current,
		CheckedAtMs:    time.Now().UnixMilli(),
	}
}

func TestUpdateButtonAppearsForANewerRelease(t *testing.T) {
	f := ukServers()
	f.release = releaseInfo("0.1.0", "v0.2.0")
	a := newTestApp(f)
	tt := tester(a)
	if !tt.HasText("Update Available") {
		t.Fatalf("no update button: %q", tt.Texts())
	}
	if !tt.HasText("Ignore this update") {
		t.Fatal("the ignore action is missing")
	}
}

func TestIgnoringAnUpdateHidesTheButton(t *testing.T) {
	f := ukServers()
	f.release = releaseInfo("0.1.0", "v0.2.0")
	a := newTestApp(f)
	tt := tester(a)
	if err := tt.Click("Ignore this update"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if !tt.HasText("Masir v0.1.0") || tt.HasText("Update Available") {
		t.Fatalf("after ignoring: %q", tt.Texts())
	}
	if !f.called("ignore v0.2.0") {
		t.Fatalf("calls = %v", f.calls)
	}
}

func TestUpdatePercentAndLabels(t *testing.T) {
	f := ukServers()
	f.release = releaseInfo("0.1.0", "v0.2.0")
	a := newTestApp(f)
	a.upd.phase = updateDownloading
	a.upd.job = &pb.UpdateJob{State: pb.UpdateState_UPDATE_STATE_DOWNLOADING, BytesDone: 42, BytesTotal: 100}
	if got := a.updatePercent(); got != 42 {
		t.Fatalf("percent = %d", got)
	}
	// One download (a bundled daemon, or an app that is current): no part marker.
	if got := a.updateLabel(); got != "Downloading 42%" {
		t.Fatalf("label = %q", got)
	}
	a.upd.phase = updatePaused
	if got := a.updateLabel(); got != "Paused 42%" {
		t.Fatalf("paused label = %q", got)
	}
}

// An update with a service installed downloads twice: the daemon and command line, then
// the app. The label says which part, since the footer is too narrow for the names.
func TestTwoPartUpdateLabels(t *testing.T) {
	f := ukServers()
	f.release = releaseInfo("0.1.0", "v0.2.0")
	a := newTestApp(f)
	a.upd.parts = 2
	a.upd.phase = updateDownloading
	a.upd.job = &pb.UpdateJob{State: pb.UpdateState_UPDATE_STATE_DOWNLOADING, BytesDone: 42, BytesTotal: 100}
	if got := a.updateLabel(); got != "Part 1/2 · 42%" {
		t.Fatalf("part 1 label = %q", got)
	}
	if got := a.updateTooltip(); got != "Part 1 of 2: the daemon and the masir command" {
		t.Fatalf("part 1 tooltip = %q", got)
	}
	a.upd.phase = updatePaused
	if got := a.updateLabel(); got != "Paused 1/2 · 42%" {
		t.Fatalf("part 1 paused = %q", got)
	}
	// The app's share is its own progress, reported by the app updater.
	a.upd.phase = updateAppDownloading
	a.upd.progress, a.upd.total = 74, 100
	a.upd.job = nil
	if got := a.updateLabel(); got != "Part 2/2 · 74%" {
		t.Fatalf("part 2 label = %q", got)
	}
	if got := a.updateTooltip(); got != "Part 2 of 2: the Masir app" {
		t.Fatalf("part 2 tooltip = %q", got)
	}
	a.upd.phase = updateAppPaused
	if got := a.updateLabel(); got != "Paused 2/2 · 74%" {
		t.Fatalf("part 2 paused = %q", got)
	}
	// The other phases name themselves; only the downloads are numbered.
	for phase, want := range map[updatePhase]string{
		updateAvailable:     "Update Available",
		updateVerifying:     "Verifying update",
		updateInstalling:    "Installing update",
		updateRestarting:    "Restarting masir",
		updateAppInstalling: "Installing Masir",
	} {
		a.upd.phase = phase
		if got := a.updateLabel(); got != want {
			t.Errorf("phase %v label = %q, want %q", phase, got, want)
		}
		if tip := a.updateTooltip(); tip != "" {
			t.Errorf("phase %v has tooltip %q", phase, tip)
		}
	}
}

// The number of downloads decides the label: a daemon running from the app bundle is
// updated by the app alone, and an app that is current leaves only the daemon.
func TestTheUpdateCountsItsDownloads(t *testing.T) {
	f := ukServers()
	f.release = releaseInfo("0.1.0", "v0.2.0")
	a := newTestApp(f)
	a.upd.app = &mygo.Update{Version: "0.2.0"}
	if got := a.updatePartCount(); got != 2 {
		t.Fatalf("service daemon and app: %d parts", got)
	}
	a.upd.app = nil
	if got := a.updatePartCount(); got != 1 {
		t.Fatalf("daemon only: %d parts", got)
	}
	// A bundled daemon belongs to the app updater.
	a.upd.app = &mygo.Update{Version: "0.2.0"}
	a.binary = "/Applications/Masir.app/Contents/MacOS/Masir"
	if got := a.updatePartCount(); got != 1 {
		t.Fatalf("bundled daemon: %d parts", got)
	}
}

func TestDaemonUpdatePauseResumeAndStop(t *testing.T) {
	f := ukServers()
	f.release = releaseInfo("0.1.0", "v0.2.0")
	f.updateJob = &pb.UpdateJob{Target: pb.UpdateTarget_UPDATE_TARGET_MASIR, State: pb.UpdateState_UPDATE_STATE_PAUSED, BytesDone: 30, BytesTotal: 100}
	a := newTestApp(f)
	a.upd.phase = updateDownloading
	a.upd.job = &pb.UpdateJob{Target: pb.UpdateTarget_UPDATE_TARGET_MASIR, State: pb.UpdateState_UPDATE_STATE_DOWNLOADING, BytesDone: 30, BytesTotal: 100}

	a.pauseUpdate()
	if a.upd.phase != updatePaused || !f.called("pause update") {
		t.Fatalf("phase=%v calls=%v", a.upd.phase, f.calls)
	}
	f.updateJob = &pb.UpdateJob{Target: pb.UpdateTarget_UPDATE_TARGET_MASIR, State: pb.UpdateState_UPDATE_STATE_DOWNLOADING, BytesDone: 30, BytesTotal: 100}
	a.upd.watchCancel = func() {} // the watch from the start of the job is still open
	a.resumeUpdate()
	if a.upd.phase != updateDownloading || !f.called("resume update") {
		t.Fatalf("phase=%v calls=%v", a.upd.phase, f.calls)
	}
	f.updateJob = &pb.UpdateJob{Target: pb.UpdateTarget_UPDATE_TARGET_MASIR, State: pb.UpdateState_UPDATE_STATE_STOPPED}
	a.cancelUpdate()
	if a.upd.phase != updateAvailable || !f.called("stop update") {
		t.Fatalf("phase=%v calls=%v", a.upd.phase, f.calls)
	}
}

func TestDaemonUpdateThenAppUpdateRelaunches(t *testing.T) {
	f := ukServers()
	f.status.Version = "0.2.0" // the RESTARTING job's daemon has come back
	f.release = releaseInfo("0.1.0", "v0.2.0")
	a := newTestApp(f)
	f.updateJob = &pb.UpdateJob{Target: pb.UpdateTarget_UPDATE_TARGET_MASIR, State: pb.UpdateState_UPDATE_STATE_RESTARTING, Version: "v0.2.0"}
	f.updateStream = &fakeUpdateStream{jobs: []*pb.UpdateJob{f.updateJob}}
	a.upd.rel = releaseInfo("0.1.0", "v0.2.0")
	a.upd.phase = updateAvailable
	app := &mygo.Update{Version: "0.2.0"}
	a.upd.app = app
	installed, relaunched := false, false
	oldInstall, oldRelaunch := installAppUpdate, relaunchApp
	installAppUpdate = func(_ context.Context, u *mygo.Update, progress func(int64, int64)) error {
		if u != app {
			t.Fatalf("installed %v, want the checked app update", u.Version)
		}
		installed = true
		progress(50, 100)
		return nil
	}
	relaunchApp = func() { relaunched = true }
	t.Cleanup(func() { installAppUpdate, relaunchApp = oldInstall, oldRelaunch })

	a.startUpdate()
	if !f.called("start update") || !f.called("watch update") {
		t.Fatalf("calls = %v", f.calls)
	}
	if !installed || !relaunched {
		t.Fatalf("installed=%v relaunched=%v", installed, relaunched)
	}
	if a.upd.phase != updateIdle {
		t.Fatalf("phase = %v", a.upd.phase)
	}
}

func TestAppUpdatePauseAndResume(t *testing.T) {
	f := ukServers()
	f.release = releaseInfo("0.1.0", "0.1.0") // the daemon is current; only the app is newer
	a := newTestApp(f)
	app := &mygo.Update{Version: "0.2.0"}
	a.upd.app = app
	a.upd.phase = updateAvailable
	installs := 0
	oldInstall := installAppUpdate
	installAppUpdate = func(context.Context, *mygo.Update, func(int64, int64)) error {
		installs++
		return context.Canceled
	}
	oldRelaunch := relaunchApp
	relaunchApp = func() { t.Fatal("a cancelled app download relaunched") }
	t.Cleanup(func() { installAppUpdate, relaunchApp = oldInstall, oldRelaunch })

	a.startAppUpdate()
	// The inline async install returned context.Canceled, but it was not paused: a real
	// error becomes the failed state.
	if a.upd.phase != updateFailed {
		t.Fatalf("phase = %v", a.upd.phase)
	}
	a.upd.phase = updateAppDownloading
	a.upd.progress, a.upd.total = 40, 100
	a.upd.appPaused = false
	cancelled := false
	a.upd.appCancel = func() { cancelled = true }
	a.pauseUpdate()
	if !cancelled || a.upd.phase != updateAppPaused {
		t.Fatalf("paused: cancelled=%v phase=%v", cancelled, a.upd.phase)
	}
	a.resumeUpdate()
	if installs < 2 {
		t.Fatalf("resume did not start the installer again: %d", installs)
	}
}

func TestCheckUsesTheDaemonsOneDayCache(t *testing.T) {
	f := ukServers()
	f.release = releaseInfo("0.1.0", "v0.2.0")
	a := newTestApp(f)
	if !f.called("check update") {
		t.Fatal("the first status did not check")
	}
	calls := len(f.calls)
	a.applyStatus(f.status)
	if len(f.calls) != calls {
		t.Fatalf("a cached release checked again: %v", f.calls)
	}
}

func TestDaemonStartForgetsAnIgnoredPrompt(t *testing.T) {
	f := ukServers()
	f.release = releaseInfo("0.1.0", "v0.2.0")
	a := newTestApp(f)
	a.upd.rel = &pb.ReleaseInfo{CurrentVersion: "0.1.0", LatestVersion: "v0.2.0", Available: true, IgnoredVersion: "v0.2.0", CheckedAtMs: time.Now().UnixMilli()}
	a.upd.phase = updateIdle
	a.upd.lastStart = 123
	a.upd.lastAttempt = time.Now()
	calls := len(f.calls)

	a.applyStatus(&pb.Status{
		Version: "0.1.0", StartedAtMs: 456, Revision: f.status.Revision,
		Settings: f.status.Settings,
	})
	if len(f.calls) == calls {
		t.Fatal("a new daemon did not trigger a fresh release check")
	}
	if a.upd.phase != updateAvailable || a.ignored("v0.2.0") {
		t.Fatalf("after the daemon restarted: phase=%v ignored=%q", a.upd.phase, a.upd.rel.GetIgnoredVersion())
	}
}

// A version gap must force a fresh daemon check: the daemon's one-day cache can still
// claim the old version is current. When the app is already past the daemon, finish the
// app-only update by updating the service too.
func TestAVersionGapCatchesTheDaemonUp(t *testing.T) {
	f := ukServers()
	a := newTestApp(f)
	f.status.Version = "0.4.0"
	f.release = releaseInfo("0.4.0", "v0.4.1")
	a.version = "0.4.1"
	calls := len(f.calls)
	a.applyStatus(f.status)
	if !f.called("check update force") {
		t.Fatalf("the cached release was used instead of a forced check: %v", f.calls)
	}
	if !f.called("start update") {
		t.Fatalf("the daemon behind the app was not updated: %v", f.calls)
	}
	if len(f.calls) == calls {
		t.Fatal("a version gap did not ask for a fresh release")
	}
	// The same gap is not asked for again on every status.
	calls = len(f.calls)
	a.applyStatus(f.status)
	if len(f.calls) != calls {
		t.Fatalf("the same gap checked again: %v", f.calls)
	}
}

func TestAWatchedRestartWaitsForTheNewVersion(t *testing.T) {
	f := ukServers()
	f.status.Version = "0.4.3"
	a := newTestApp(f)
	a.upd.phase = updateRestarting
	a.upd.job = &pb.UpdateJob{
		Target:  pb.UpdateTarget_UPDATE_TARGET_MASIR,
		State:   pb.UpdateState_UPDATE_STATE_RESTARTING,
		Version: "v0.4.3",
	}

	a.daemonWatchEnded()
	if a.upd.phase != updateIdle {
		t.Fatalf("phase=%v", a.upd.phase)
	}
}

func TestADaemonUpdateWithoutAnAppUpdateFinishes(t *testing.T) {
	f := ukServers()
	f.status.Version = "0.2.0"
	f.release = releaseInfo("0.1.0", "v0.2.0")
	a := newTestApp(f)
	f.updateJob = &pb.UpdateJob{Target: pb.UpdateTarget_UPDATE_TARGET_MASIR, State: pb.UpdateState_UPDATE_STATE_RESTARTING, Version: "v0.2.0"}
	f.updateStream = &fakeUpdateStream{jobs: []*pb.UpdateJob{f.updateJob}}
	a.upd.rel = releaseInfo("0.1.0", "v0.2.0")
	a.upd.phase = updateAvailable

	a.startUpdate()
	if !f.called("start update") {
		t.Fatalf("a daemon update did not start: %v", f.calls)
	}
	if a.upd.phase != updateIdle {
		t.Fatalf("phase=%v", a.upd.phase)
	}
}

// A daemon that runs from the app bundle is replaced by the app update, never by the
// daemon's own updater.
func TestABundledDaemonIsUpdatedByTheAppOnly(t *testing.T) {
	f := ukServers()
	f.status.Bundled = true
	f.restartVersion = "0.2.0"
	f.release = releaseInfo("0.1.0", "v0.2.0")
	a := newTestApp(f)
	if a.daemonUpdateAvailable() {
		t.Fatal("the bundled daemon offered a daemon update")
	}
	a.upd.app = &mygo.Update{Version: "0.2.0"}
	a.upd.phase = updateAvailable
	installed, relaunched := false, false
	oldInstall, oldRelaunch := installAppUpdate, relaunchApp
	installAppUpdate = func(context.Context, *mygo.Update, func(int64, int64)) error { installed = true; return nil }
	relaunchApp = func() { relaunched = true }
	t.Cleanup(func() { installAppUpdate, relaunchApp = oldInstall, oldRelaunch })

	a.startUpdate()
	if f.called("start update") {
		t.Fatalf("the bundled daemon started a daemon update: %v", f.calls)
	}
	if !f.called("restart") {
		t.Fatalf("the daemon was not restarted into the new bundle: %v", f.calls)
	}
	if !installed || !relaunched {
		t.Fatalf("installed=%v relaunched=%v", installed, relaunched)
	}
}

// A daemon that comes back without the tunnel is a failed update, not a success.
func TestADaemonWithoutTheTunnelIsAFailedUpdate(t *testing.T) {
	f := ukServers()
	f.status.TunRunning = false
	f.status.Version = "0.2.0"
	f.restartVersion = "0.2.0"
	a := newTestApp(f)
	a.upd.tunWasOn = true
	old := daemonWaitTimeout
	daemonWaitTimeout = 60 * time.Millisecond
	t.Cleanup(func() { daemonWaitTimeout = old })

	a.restartDaemon("0.2.0")
	if a.upd.phase != updateFailed {
		t.Fatalf("phase=%v err=%q", a.upd.phase, a.upd.err)
	}
	if !strings.Contains(a.upd.err, "tunnel") {
		t.Fatalf("err = %q", a.upd.err)
	}
}

// An older daemon doesn't report that it is bundled; the path still protects it.
func TestABundledDaemonPathIsNotOfferedADaemonUpdate(t *testing.T) {
	f := ukServers()
	f.release = releaseInfo("0.1.0", "v0.2.0")
	a := newTestApp(f)
	a.status.Bundled = false // a daemon built before the field existed
	a.binary = "/Applications/Masir.app/Contents/Resources/bin/masir"
	if a.daemonUpdateAvailable() {
		t.Fatal("a daemon inside the app bundle was offered a daemon update")
	}
	if !a.appUpdateCoversDaemon() {
		t.Fatal("the app update should carry a bundled daemon")
	}
}

// waitCalls waits for n calls like s: checks run off the main thread.
func waitCalls(t *testing.T, f *fakeDaemon, s string, n int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if f.count(s) >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("wanted %d %q calls, have %d", n, s, f.count(s))
}

// Opening the panel is the moment a new release can be seen, so it asks for an answer
// newer than ten minutes, and never more often than that.
func TestOpeningThePanelAsksForAReleaseEveryTenMinutes(t *testing.T) {
	f := ukServers()
	f.release = releaseInfo("0.1.0", "v0.1.0")
	a := newTestApp(f)
	if got := f.ageAsked(); got != updateCheckEvery {
		t.Fatalf("the status check asked for %v, want the daily one", got)
	}
	// The release is a day old: the next open must ask again.
	a.upd.rel.CheckedAtMs = time.Now().Add(-11 * time.Minute).UnixMilli()
	a.checkForUpdateOnForeground()
	waitCalls(t, f, "check update", 2)
	if got := f.ageAsked(); got != updateForegroundEvery {
		t.Fatalf("an open asked for %v, want %v", got, updateForegroundEvery)
	}
	// The same open twice, and a second one a moment later: nothing new is asked.
	for _, age := range []time.Duration{0, 5 * time.Minute} {
		a.upd.lastForeground = time.Now().Add(-age)
		a.upd.rel.CheckedAtMs = time.Now().Add(-11 * time.Minute).UnixMilli()
		a.checkForUpdateOnForeground()
	}
	time.Sleep(50 * time.Millisecond)
	if n := f.count("check update"); n != 2 {
		t.Fatalf("opens inside ten minutes brought the checks to %d, want 2", n)
	}
}

// The app feed answers for one release: a UI checking every few minutes must not ask it
// again until that release changes, or six hours pass.
func TestTheAppFeedIsAskedOncePerRelease(t *testing.T) {
	f := ukServers()
	f.release = releaseInfo("0.1.0", "v0.1.0")
	enabled, check := appUpdatesEnabled, checkAppUpdate
	t.Cleanup(func() { appUpdatesEnabled, checkAppUpdate = enabled, check })
	appUpdatesEnabled = func() bool { return true }
	asked := 0
	var mu sync.Mutex
	checkAppUpdate = func(context.Context) (*mygo.Update, error) {
		mu.Lock()
		asked++
		mu.Unlock()
		return nil, nil
	}
	a := newTestApp(f)
	waitFeed := func(n int) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			got := asked
			mu.Unlock()
			if got >= n {
				return
			}
			time.Sleep(time.Millisecond)
		}
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("wanted %d app feed checks, have %d", n, asked)
	}
	waitFeed(1)
	// A release the feed has answered for: a new foreground check doesn't ask again.
	a.upd.rel.CheckedAtMs = time.Now().Add(-11 * time.Minute).UnixMilli()
	a.upd.lastForeground = time.Time{}
	a.checkForUpdateOnForeground()
	a.upd.lastForeground = time.Time{}
	a.checkForUpdateOnForeground()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if asked != 1 {
		mu.Unlock()
		t.Fatalf("the feed was asked %d times for one release", asked)
	}
	mu.Unlock()
	// Six hours on, it is asked again: the app may be released apart from the daemon.
	a.upd.appCheckedAt = time.Now().Add(-updateFeedEvery - time.Minute)
	a.upd.appAttempt = time.Time{}
	a.upd.lastForeground = time.Time{}
	a.checkForUpdateOnForeground()
	waitFeed(2)
}

// A daemon that isn't running can't be updated, but the app carries one: the app feed is
// still asked, so a broken daemon doesn't hide the app that fixes it.
func TestTheAppFeedIsAskedWithoutADaemon(t *testing.T) {
	f := ukServers()
	f.release, f.releaseErr = nil, errors.New("no daemon")
	enabled, check := appUpdatesEnabled, checkAppUpdate
	t.Cleanup(func() { appUpdatesEnabled, checkAppUpdate = enabled, check })
	appUpdatesEnabled = func() bool { return true }
	asked := 0
	var mu sync.Mutex
	checkAppUpdate = func(context.Context) (*mygo.Update, error) {
		mu.Lock()
		asked++
		mu.Unlock()
		return nil, nil
	}
	a := newTestApp(f)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := asked
		mu.Unlock()
		if got > 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	first := asked
	mu.Unlock()
	if first == 0 {
		t.Fatal("no release and no app feed check: a stopped daemon hides the app update")
	}
	// And not again on the next open, since the feed answered moments ago.
	a.upd.lastForeground = time.Time{}
	a.upd.lastAttempt = time.Time{}
	a.checkForUpdateOnForeground()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if asked > 1 {
		t.Fatalf("the feed was asked %d times inside six hours", asked)
	}
}

// A hidden prompt stays hidden while the release doesn't change, however often the GUI
// asks.
func TestAnIgnoredUpdateStaysHiddenAcrossChecks(t *testing.T) {
	f := ukServers()
	rel := releaseInfo("0.1.0", "v0.2.0")
	rel.IgnoredVersion = "v0.2.0"
	f.release = rel
	a := newTestApp(f)
	if a.upd.phase == updateAvailable {
		t.Fatal("an ignored update was offered")
	}
	a.upd.rel.CheckedAtMs = time.Now().Add(-11 * time.Minute).UnixMilli()
	a.checkForUpdateOnForeground()
	time.Sleep(20 * time.Millisecond)
	if !a.ignored("v0.2.0") || a.upd.phase == updateAvailable {
		t.Fatalf("a check brought an ignored update back: phase=%v", a.upd.phase)
	}
}

// The control is 42 tw wide and the footer has no room to grow it, so a numbered label
// must stay within what it already draws: "Installing update" is the longest label the
// shipped control holds, at 100% of a download. The numbers are narrow, the words are
// not, which is why the part is "1/2" and not "part 1 of 2".
func TestThePartLabelsFitTheUpdateControl(t *testing.T) {
	f := ukServers()
	f.release = releaseInfo("0.1.0", "v0.2.0")
	a := newTestApp(f)
	a.upd.parts = 2
	a.upd.job = &pb.UpdateJob{State: pb.UpdateState_UPDATE_STATE_DOWNLOADING, BytesDone: 100, BytesTotal: 100}
	room := utf8.RuneCountInString("Installing update")
	for phase, part := range map[updatePhase]int{
		updateDownloading: 1, updatePaused: 1, updateAppDownloading: 2, updateAppPaused: 2,
	} {
		a.upd.phase = phase
		a.upd.progress, a.upd.total = 100, 100
		got := a.updateLabel()
		if n := utf8.RuneCountInString(got); n > room {
			t.Errorf("phase %v label %q is %d characters, more than %q (%d)", phase, got, n, "Installing update", room)
		}
		if part == 2 && !strings.Contains(got, "2/2") && strings.Contains(got, "/") {
			t.Errorf("phase %v label %q names the wrong part", phase, got)
		}
	}
}
