package main

import (
	"context"
	"strings"
	"testing"
	"time"

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
	if got := a.updateLabel(); got != "Downloading 42%" {
		t.Fatalf("label = %q", got)
	}
	a.upd.phase = updatePaused
	if got := a.updateLabel(); got != "Paused 42%" {
		t.Fatalf("paused label = %q", got)
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

// A version gap asks for a fresh release instead of installing either half on its own.
func TestAVersionGapAsksForAFreshRelease(t *testing.T) {
	f := ukServers()
	a := newTestApp(f)
	f.status.Version = "0.4.0"
	f.release = releaseInfo("0.4.0", "v0.4.1")
	a.version = "0.4.1"
	calls := len(f.calls)
	a.applyStatus(f.status)
	if len(f.calls) == calls {
		t.Fatal("a version gap did not ask for a fresh release")
	}
	if f.called("start update") {
		t.Fatalf("the gap installed something on its own: %v", f.calls)
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
