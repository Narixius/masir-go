package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"google.golang.org/protobuf/proto"

	"github.com/narixius/masir-go/libs/masir-core/service"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// The GUI checks for a new release at most as often as the daemon does, and an ignored
// prompt comes back when a fresh check runs (or when the daemon restarts).
const (
	updateCheckEvery = 24 * time.Hour
	updateRetryWait  = updateCheckEvery
)

// updatePhase is what the footer's update control shows.
type updatePhase int

const (
	updateIdle updatePhase = iota
	updateAvailable
	updateStarting
	updateDownloading
	updatePaused
	updateVerifying
	updateInstalling
	updateRestarting
	updateAppDownloading
	updateAppPaused
	updateAppInstalling
	updateFailed
)

// updateState is the daemon's release check, the desktop app's signed update, and the
// job in progress. It is read and written on the main thread only.
type updateState struct {
	phase    updatePhase
	rel      *pb.ReleaseInfo
	app      *mygo.Update
	job      *pb.UpdateJob
	err      string
	checking bool
	// appChecked says the signed app update was asked for in this check cycle.
	appChecked bool
	// lastAttempt keeps a failed release check from being repeated on every status.
	lastAttempt time.Time
	lastStart   int64
	// download progress for the app update; the daemon job has its own bytes.
	progress, total int64
	appPaused       bool
	appCancelled    bool
	appCancel       context.CancelFunc
	appRun          int
	watchCancel     context.CancelFunc
	daemonDone      bool
	ignoreBusy      bool
	attached        bool
	attaching       bool
	deferRestart    bool
	pendingVersion  string
	pendingAtLaunch bool
	restarting      bool
	catchUp         bool // this app launch already checked a daemon left behind by an app-only update
}

// The app's updater is behind variables so tests do not need a signed release or a
// running application.
var (
	appUpdatesEnabled = func() bool { return mygo.Updater.Enabled() }
	checkAppUpdate    = func(ctx context.Context) (*mygo.Update, error) { return mygo.Updater.Check(ctx) }
	installAppUpdate  = func(ctx context.Context, u *mygo.Update, progress func(int64, int64)) error {
		return u.Install(ctx, progress)
	}
	relaunchApp = func() { mygo.App.Relaunch() }
)

// observeUpdates takes the daemon's latest release and starts a check when the cached
// one is missing or older than the check interval.
func (a *app) observeUpdates(st *pb.Status) {
	start := st.GetStartedAtMs()
	if start != 0 && a.upd.lastStart != 0 && start != a.upd.lastStart && !a.busyUpdate() {
		// A new daemon: its ignored version is gone, and its release cache with it.
		a.upd.rel = nil
		a.upd.app = nil
		a.upd.appChecked = false
		a.upd.lastAttempt = time.Time{}
		a.upd.attached = false
		a.upd.deferRestart = false
		if a.upd.phase == updateAvailable || a.upd.phase == updateFailed {
			a.upd.phase = updateIdle
		}
	}
	if start != 0 {
		a.upd.lastStart = start
	}
	if rel := st.GetLatestRelease(); rel != nil {
		a.applyRelease(rel)
	} else if !a.busyUpdate() {
		a.upd.rel = nil
	}
	a.maybeFinishPending(st)
	a.catchUpDaemon(st)
	a.maybeCheckUpdates()
	if !a.upd.attached {
		// A job started by the CLI or a previous GUI is still the one to show.
		a.upd.attached = true
		a.upd.attaching = true
		a.watchDaemonUpdate()
	}
}

// applyRelease takes a release check and updates what the control offers.
func (a *app) applyRelease(rel *pb.ReleaseInfo) {
	old := a.upd.rel
	a.upd.rel = rel
	if old == nil || old.GetCheckedAtMs() != rel.GetCheckedAtMs() {
		// A fresh daemon check starts a new check cycle for the signed app update too.
		a.upd.appChecked = false
	}
	if a.upd.phase == updateAvailable && !a.hasUpdate() {
		a.upd.phase = updateIdle
	}
	if a.upd.phase == updateIdle && a.hasUpdate() {
		a.upd.phase = updateAvailable
	}
}

// catchUpDaemon handles the one case the app update can leave behind: the desktop app
// reached a release before the daemon's cached release check knew about it, so the update
// only replaced the app. The daemon and the app should run the same release; ask GitHub
// directly, then install the daemon if it is behind.
func (a *app) catchUpDaemon(st *pb.Status) {
	if a.upd.catchUp || a.busyUpdate() || a.upd.checking || a.upd.restarting {
		return
	}
	if a.version == "" || st.GetVersion() == "" || sameVersion(a.version, st.GetVersion()) {
		return
	}
	a.upd.catchUp = true
	a.checkDaemonReleaseForced()
}

// checkDaemonReleaseForced asks GitHub through the daemon, ignoring its one-day cache,
// and starts the daemon update when the app is newer than what runs.
func (a *app) checkDaemonReleaseForced() {
	a.upd.checking = true
	a.async(func() {
		ctx, cancel := rpc()
		res, err := a.d.CheckUpdate(ctx, &pb.CheckUpdateRequest{Force: true})
		cancel()
		a.post(func() {
			a.upd.checking = false
			if err == nil && res.GetRelease() != nil {
				a.applyRelease(res.GetRelease())
				if a.daemonUpdateAvailable() && !a.busyUpdate() {
					a.startDaemonUpdate()
					return
				}
			}
			a.maybeCheckUpdates()
		})
	})
}

// maybeCheckUpdates asks the daemon for a release when its cached one is missing or old,
// then asks the signed app feed once in that check cycle. It never polls.
func (a *app) maybeCheckUpdates() {
	if a.busyUpdate() || a.upd.checking {
		return
	}
	rel := a.upd.rel
	if rel == nil || time.Since(time.UnixMilli(rel.GetCheckedAtMs())) >= updateCheckEvery {
		if !a.upd.lastAttempt.IsZero() && time.Since(a.upd.lastAttempt) < updateRetryWait {
			return
		}
		a.upd.lastAttempt = time.Now()
		a.checkDaemonRelease()
		return
	}
	if a.ignored(rel.GetLatestVersion()) {
		return
	}
	if !a.upd.appChecked {
		a.checkAppRelease()
	}
}

// checkDaemonRelease asks the daemon for the latest release. The daemon caches it for
// one day, so this is cheap when another UI already checked.
func (a *app) checkDaemonRelease() {
	a.upd.checking = true
	a.async(func() {
		ctx, cancel := rpc()
		res, err := a.d.CheckUpdate(ctx, &pb.CheckUpdateRequest{})
		cancel()
		a.post(func() {
			a.upd.checking = false
			if err == nil && res.GetRelease() != nil {
				a.applyRelease(res.GetRelease())
			}
			a.maybeCheckUpdates()
		})
	})
}

// checkAppRelease asks MyGo for a signed update of the desktop app itself.
func (a *app) checkAppRelease() {
	a.upd.appChecked = true
	if !appUpdatesEnabled() {
		return
	}
	a.async(func() {
		ctx, cancel := rpc()
		u, err := checkAppUpdate(ctx)
		cancel()
		a.post(func() {
			if err == nil {
				a.upd.app = u
			}
			if a.upd.phase == updateAvailable && !a.hasUpdate() {
				a.upd.phase = updateIdle
			}
			if a.upd.phase == updateIdle && a.hasUpdate() {
				a.upd.phase = updateAvailable
			}
		})
	})
}

// ignored reports whether this version is hidden by the daemon's ignore.
func (a *app) ignored(version string) bool {
	hidden := a.upd.rel.GetIgnoredVersion()
	return hidden != "" && sameVersion(hidden, version)
}

func sameVersion(a, b string) bool {
	return strings.TrimPrefix(strings.TrimSpace(a), "v") == strings.TrimPrefix(strings.TrimSpace(b), "v")
}

// daemonUpdateAvailable reports whether the daemon and CLI have a newer release.
func (a *app) daemonUpdateAvailable() bool {
	rel := a.upd.rel
	return rel != nil && rel.GetAvailable() && !a.ignored(rel.GetLatestVersion())
}

// appUpdateAvailable reports whether the signed desktop app has a newer release.
func (a *app) appUpdateAvailable() bool {
	return a.upd.app != nil && !a.ignored(a.upd.app.Version)
}

// hasUpdate reports whether either side has an update the person has not ignored.
func (a *app) hasUpdate() bool {
	return a.daemonUpdateAvailable() || a.appUpdateAvailable()
}

// busy reports whether an update is doing something, so a check must not interrupt it.
func (a *app) busyUpdate() bool {
	switch a.upd.phase {
	case updateStarting, updateDownloading, updatePaused, updateVerifying,
		updateInstalling, updateRestarting, updateAppDownloading, updateAppPaused,
		updateAppInstalling:
		return true
	}
	return false
}

// showUpdateControl reports whether the footer and Settings show the control rather than
// the version text.
func (a *app) showUpdateControl() bool {
	if a.upd.phase == updateAvailable || a.upd.phase == updateFailed {
		return true
	}
	return a.busyUpdate()
}

// startUpdate starts the daemon update, then the desktop app update, in one flow.
func (a *app) startUpdate() {
	if a.upd.phase != updateAvailable && a.upd.phase != updateFailed {
		return
	}
	a.upd.err = ""
	if a.daemonUpdateAvailable() {
		a.startDaemonUpdate()
		return
	}
	if a.appUpdateAvailable() {
		a.startAppUpdate()
	}
}

func (a *app) startDaemonUpdate() {
	a.upd.phase = updateStarting
	a.upd.deferRestart = true
	client := ""
	if a.cli.installed {
		client = a.cli.path
	}
	root := a.privileged()
	a.async(func() {
		if service.Installed() && !root {
			if err := a.becomePrivileged(); err != nil {
				a.post(func() {
					a.upd.phase = updateFailed
					a.upd.err = describe(err)
					a.notify("The update failed: " + a.upd.err)
				})
				return
			}
		}
		ctx, cancel := rpc()
		res, err := a.d.StartUpdate(ctx, &pb.StartUpdateRequest{
			Target: pb.UpdateTarget_UPDATE_TARGET_MASIR, Trigger: "user", ClientBinary: client,
			DeferRestart: true,
		})
		cancel()
		a.post(func() {
			if err != nil {
				a.upd.phase = updateFailed
				a.upd.err = describe(err)
				a.notify("The update failed: " + a.upd.err)
				return
			}
			a.upd.job = res.GetJob()
			a.applyJob(a.upd.job)
			a.watchDaemonUpdate()
		})
	})
}

// watchDaemonUpdate follows the daemon's job until it ends or the daemon restarts.
func (a *app) watchDaemonUpdate() {
	ctx, cancel := context.WithCancel(context.Background())
	a.upd.watchCancel = cancel
	a.async(func() {
		stream, err := a.d.WatchUpdate(ctx, &pb.WatchUpdateRequest{Target: pb.UpdateTarget_UPDATE_TARGET_MASIR})
		if err != nil {
			a.post(func() {
				attaching := a.upd.attaching
				a.upd.attaching = false
				if attaching {
					return // no job to attach to is not a failed update
				}
				a.upd.phase = updateFailed
				a.upd.err = describe(err)
				a.notify("The update failed: " + a.upd.err)
			})
			return
		}
		for {
			res, err := stream.Recv()
			if err != nil {
				break
			}
			job := res.GetJob()
			a.post(func() { a.applyJob(job) })
		}
		a.post(func() {
			a.upd.watchCancel = nil
			a.daemonWatchEnded()
		})
	})
}

// applyJob turns the daemon's job state into the control's phase.
func (a *app) applyJob(job *pb.UpdateJob) {
	if job == nil {
		return
	}
	if a.upd.attaching {
		a.upd.attaching = false
		if !updateActive(job.GetState()) {
			return // a finished job from an earlier update is not ours to show
		}
	}
	a.upd.job = job
	switch job.GetState() {
	case pb.UpdateState_UPDATE_STATE_CHECKING:
		a.upd.phase = updateStarting
	case pb.UpdateState_UPDATE_STATE_DOWNLOADING:
		a.upd.phase = updateDownloading
	case pb.UpdateState_UPDATE_STATE_PAUSED:
		a.upd.phase = updatePaused
	case pb.UpdateState_UPDATE_STATE_VERIFYING:
		a.upd.phase = updateVerifying
	case pb.UpdateState_UPDATE_STATE_INSTALLING:
		a.upd.phase = updateInstalling
	case pb.UpdateState_UPDATE_STATE_RESTARTING:
		a.upd.phase = updateRestarting
	case pb.UpdateState_UPDATE_STATE_DONE:
		a.daemonInstalled()
	case pb.UpdateState_UPDATE_STATE_UP_TO_DATE:
		a.daemonFinished()
	case pb.UpdateState_UPDATE_STATE_STOPPED:
		a.upd.phase = updateAvailable
		a.upd.job = nil
	case pb.UpdateState_UPDATE_STATE_FAILED, pb.UpdateState_UPDATE_STATE_BLOCKED:
		a.upd.phase = updateFailed
		a.upd.err = job.GetError()
		if a.upd.err == "" && len(job.GetBlockedRules()) > 0 {
			a.upd.err = "the new version can't build some of your rules"
		}
		if a.upd.err == "" {
			a.upd.err = "the update couldn't be installed"
		}
		a.notify("The update failed: " + a.upd.err)
	}
}

// daemonWatchEnded handles a watch stream that ended, including the daemon going away
// to restart into the new version.
func (a *app) daemonWatchEnded() {
	a.upd.attaching = false
	if a.upd.phase == updateRestarting && a.upd.restarting {
		return // the deferred restart is driven by restartDaemon
	}
	if a.upd.phase == updateRestarting {
		if a.upd.deferRestart {
			// An older daemon finishes a deferred install as RESTARTING. It means the
			// binary is installed and this process is intentionally still running; the
			// GUI records the install and does the restart itself.
			a.daemonInstalled()
			return
		}
		a.awaitDaemon(a.upd.job.GetVersion())
		return
	}
	switch a.upd.phase {
	case updateFailed, updateIdle, updateAppDownloading, updateAppPaused, updateAppInstalling:
		return
	case updateAvailable:
		return
	}
	a.upd.phase = updateFailed
	a.upd.err = "lost the update while it was running"
	a.notify("The update failed: " + a.upd.err)
}

// awaitDaemon waits for the restarted daemon to report the version the update installed.
func (a *app) awaitDaemon(version string) {
	a.async(func() {
		deadline := time.Now().Add(2 * time.Minute)
		for time.Now().Before(deadline) {
			ctx, cancel := rpc()
			res, err := a.d.GetStatus(ctx, &pb.GetStatusRequest{})
			cancel()
			if err == nil && (version == "" || sameVersion(res.GetStatus().GetVersion(), version)) {
				a.post(a.daemonFinished)
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		a.post(func() {
			a.upd.phase = updateFailed
			a.upd.err = "the daemon didn't come back after the update"
			a.notify("The update failed: " + a.upd.err)
		})
	})
}

// daemonInstalled records a deferred daemon install and moves on to the app update, or
// restarts the daemon when there is no app update to follow.
func (a *app) daemonInstalled() {
	if a.upd.phase == updateAppDownloading || a.upd.phase == updateAppPaused || a.upd.phase == updateAppInstalling {
		return
	}
	version := a.upd.job.GetVersion()
	a.upd.daemonDone = true
	a.upd.job = nil
	a.upd.deferRestart = false
	if a.upd.rel != nil {
		rel := proto.Clone(a.upd.rel).(*pb.ReleaseInfo)
		rel.Available = false
		rel.IgnoredVersion = ""
		a.upd.rel = rel
	}
	if version != "" {
		a.upd.pendingVersion = version
		a.upd.pendingAtLaunch = false
		a.savePending(version)
	}
	if a.appUpdateAvailable() {
		a.startAppUpdate()
		return
	}
	if version != "" {
		a.restartDaemon(version)
		return
	}
	a.upd.phase = updateIdle
}

// daemonFinished moves on after an immediate daemon restart, or back to the version text.
func (a *app) daemonFinished() {
	if a.upd.phase == updateAppDownloading || a.upd.phase == updateAppPaused || a.upd.phase == updateAppInstalling {
		return // the app update already started from the daemon's final job
	}
	a.upd.daemonDone = true
	a.upd.job = nil
	if a.upd.rel != nil {
		// Keep the release for the app-only ignore; it is no longer daemon-available.
		rel := proto.Clone(a.upd.rel).(*pb.ReleaseInfo)
		rel.Available = false
		rel.IgnoredVersion = ""
		a.upd.rel = rel
	}
	if a.appUpdateAvailable() {
		a.startAppUpdate()
		return
	}
	a.upd.phase = updateIdle
}

// startAppUpdate installs the signed desktop app update with MyGo. A pause cancels the
// download; a resume starts it again, since MyGo's installer cannot continue a partial
// app download.
func (a *app) startAppUpdate() {
	u := a.upd.app
	if u == nil {
		return
	}
	a.upd.phase = updateAppDownloading
	a.upd.progress, a.upd.total = 0, 0
	a.upd.appPaused = false
	a.upd.appCancelled = false
	a.upd.appRun++
	run := a.upd.appRun
	ctx, cancel := context.WithCancel(context.Background())
	a.upd.appCancel = cancel
	a.async(func() {
		lastPct, lastAt := -1, time.Now()
		progress := func(done, total int64) {
			pct := -1
			if total > 0 {
				pct = int(done * 100 / total)
			}
			if pct == lastPct && time.Since(lastAt) < 100*time.Millisecond {
				return
			}
			lastPct, lastAt = pct, time.Now()
			a.post(func() {
				if run == a.upd.appRun && a.upd.phase == updateAppDownloading {
					a.upd.progress, a.upd.total = done, total
					if total > 0 && done >= total {
						a.upd.phase = updateAppInstalling // past the pause point
					}
				}
			})
		}
		err := installAppUpdate(ctx, u, progress)
		a.post(func() {
			if run != a.upd.appRun {
				return // a pause and resume started a newer attempt
			}
			a.upd.appCancel = nil
			switch {
			case a.upd.appCancelled:
				a.upd.phase = updateAvailable
				a.upd.progress, a.upd.total = 0, 0
			case a.upd.appPaused:
				a.upd.phase = updateAppPaused
			case err != nil:
				a.upd.phase = updateFailed
				a.upd.err = err.Error()
				a.notify("The app update failed: " + a.upd.err)
			default:
				a.upd.phase = updateAppInstalling
				relaunchApp()
			}
		})
	})
}

// pauseUpdate pauses a daemon download, or cancels the app download until it is resumed.
func (a *app) pauseUpdate() {
	switch a.upd.phase {
	case updateDownloading:
		a.async(func() {
			ctx, cancel := rpc()
			res, err := a.d.PauseUpdate(ctx, &pb.PauseUpdateRequest{Target: pb.UpdateTarget_UPDATE_TARGET_MASIR})
			cancel()
			a.post(func() {
				if err != nil {
					a.upd.phase = updateFailed
					a.upd.err = describe(err)
					return
				}
				a.applyJob(res.GetJob())
			})
		})
	case updateAppDownloading:
		a.upd.appPaused = true
		if a.upd.appCancel != nil {
			a.upd.appCancel()
			a.upd.appCancel = nil
		}
		a.upd.phase = updateAppPaused
	}
}

// resumeUpdate continues a paused daemon download, or starts the app download again.
func (a *app) resumeUpdate() {
	switch a.upd.phase {
	case updatePaused:
		a.async(func() {
			ctx, cancel := rpc()
			res, err := a.d.ResumeUpdate(ctx, &pb.ResumeUpdateRequest{Target: pb.UpdateTarget_UPDATE_TARGET_MASIR})
			cancel()
			a.post(func() {
				if err != nil {
					a.upd.phase = updateFailed
					a.upd.err = describe(err)
					return
				}
				a.applyJob(res.GetJob())
				if a.upd.watchCancel == nil {
					a.watchDaemonUpdate()
				}
			})
		})
	case updateAppPaused:
		a.startAppUpdate()
	}
}

// cancelUpdate stops a daemon download and deletes its partial file, or abandons the app
// download and returns to the available button.
func (a *app) cancelUpdate() {
	switch a.upd.phase {
	case updateDownloading, updatePaused:
		a.async(func() {
			ctx, cancel := rpc()
			res, err := a.d.StopUpdate(ctx, &pb.StopUpdateRequest{Target: pb.UpdateTarget_UPDATE_TARGET_MASIR})
			cancel()
			a.post(func() {
				if err != nil {
					a.upd.phase = updateFailed
					a.upd.err = describe(err)
					return
				}
				a.applyJob(res.GetJob())
			})
		})
	case updateAppDownloading, updateAppPaused:
		a.upd.appCancelled = true
		if a.upd.appCancel != nil {
			a.upd.appCancel()
			a.upd.appCancel = nil
		}
		a.upd.phase = updateAvailable
		a.upd.progress, a.upd.total = 0, 0
	}
}

// retryUpdate starts the failed side again.
func (a *app) retryUpdate() {
	if a.upd.phase != updateFailed {
		return
	}
	a.upd.phase = updateAvailable
	a.startUpdate()
}

// ignoreUpdate hides this version in the daemon's memory until the next fresh check or
// the next daemon start.
func (a *app) ignoreUpdate() {
	if a.upd.ignoreBusy {
		return
	}
	version := ""
	switch {
	case a.daemonUpdateAvailable():
		version = a.upd.rel.GetLatestVersion()
	case a.appUpdateAvailable():
		version = a.upd.app.Version
	}
	if version == "" {
		return
	}
	a.upd.ignoreBusy = true
	a.async(func() {
		ctx, cancel := rpc()
		res, err := a.d.IgnoreUpdate(ctx, &pb.IgnoreUpdateRequest{Version: version})
		cancel()
		a.post(func() {
			a.upd.ignoreBusy = false
			if err != nil {
				a.fail("Couldn't ignore the update: ", err)
				return
			}
			if res.GetRelease() != nil {
				a.applyRelease(res.GetRelease())
			}
			a.upd.phase = updateIdle
		})
	})
}

// updateActive reports whether a job is doing something a UI can show.
func updateActive(s pb.UpdateState) bool {
	switch s {
	case pb.UpdateState_UPDATE_STATE_CHECKING, pb.UpdateState_UPDATE_STATE_DOWNLOADING,
		pb.UpdateState_UPDATE_STATE_PAUSED, pb.UpdateState_UPDATE_STATE_VERIFYING,
		pb.UpdateState_UPDATE_STATE_INSTALLING, pb.UpdateState_UPDATE_STATE_RESTARTING:
		return true
	}
	return false
}

// updatePercent is the download progress, 0 when it isn't known yet.
func (a *app) updatePercent() int {
	var done, total int64
	if a.upd.phase == updateAppDownloading || a.upd.phase == updateAppPaused {
		done, total = a.upd.progress, a.upd.total
	} else if a.upd.job != nil {
		done, total = a.upd.job.GetBytesDone(), a.upd.job.GetBytesTotal()
	}
	if total <= 0 {
		return 0
	}
	return int(max(0, min(100, done*100/total)))
}

// updateLabel is the text inside the control.
func (a *app) updateLabel() string {
	switch a.upd.phase {
	case updateAvailable:
		return "Update Available"
	case updateStarting:
		return "Starting update"
	case updateDownloading:
		return fmt.Sprintf("Downloading %d%%", a.updatePercent())
	case updatePaused:
		return fmt.Sprintf("Paused %d%%", a.updatePercent())
	case updateVerifying:
		return "Verifying update"
	case updateInstalling:
		return "Installing update"
	case updateRestarting:
		return "Restarting masir"
	case updateAppDownloading:
		return fmt.Sprintf("Downloading %d%%", a.updatePercent())
	case updateAppPaused:
		return fmt.Sprintf("Paused %d%%", a.updatePercent())
	case updateAppInstalling:
		return "Installing Masir"
	case updateFailed:
		if a.upd.err != "" {
			return "Update failed"
		}
		return "Update failed"
	}
	return ""
}

// pendingUpdateFile is where the GUI remembers a daemon binary it installed but did not
// restart yet. It is separate from the daemon's data, so an app update keeps it.
func pendingUpdateFile() string {
	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "pending-update")
}

func readPendingUpdate(path string) string {
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (a *app) savePending(version string) {
	if a.pendingFile == "" || version == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(a.pendingFile), 0o700)
	_ = os.WriteFile(a.pendingFile, []byte(version+"\n"), 0o600)
}

func (a *app) clearPending() {
	if a.pendingFile != "" {
		_ = os.Remove(a.pendingFile)
	}
}

// maybeFinishPending restarts a daemon whose new binary was installed by the previous
// app session, once the app has been updated and started again.
func (a *app) maybeFinishPending(st *pb.Status) {
	version := a.upd.pendingVersion
	if version == "" {
		return
	}
	if st.GetVersion() != "" && sameVersion(st.GetVersion(), version) {
		a.upd.pendingVersion, a.upd.pendingAtLaunch = "", false
		a.clearPending()
		if a.upd.phase == updateRestarting {
			a.upd.phase = updateIdle
		}
		return
	}
	if a.upd.pendingAtLaunch && !a.busyUpdate() && !a.upd.restarting {
		a.restartDaemon(version)
	}
}

// restartDaemon stops the running daemon so the service (or the app's watch) starts the
// binary installed by the deferred daemon update.
func (a *app) restartDaemon(version string) {
	if a.upd.restarting {
		return
	}
	a.upd.restarting = true
	a.upd.phase = updateRestarting
	a.upd.err = ""
	a.async(func() {
		ctx, cancel := rpc()
		_, err := a.d.Restart(ctx, &pb.RestartRequest{})
		cancel()
		if err != nil { // an older daemon without Restart, or no longer reachable
			ctx, cancel := rpc()
			_, _ = a.d.Shutdown(ctx, &pb.ShutdownRequest{})
			cancel()
		}
		deadline := time.Now().Add(2 * time.Minute)
		for time.Now().Before(deadline) {
			ctx, cancel := rpc()
			res, err := a.d.GetStatus(ctx, &pb.GetStatusRequest{})
			cancel()
			if err == nil && (version == "" || sameVersion(res.GetStatus().GetVersion(), version)) {
				a.post(func() {
					a.upd.restarting = false
					a.upd.pendingVersion, a.upd.pendingAtLaunch = "", false
					a.clearPending()
					a.upd.phase = updateIdle
				})
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		a.post(func() {
			a.upd.restarting = false
			a.upd.phase = updateFailed
			a.upd.err = "the daemon didn't restart into the installed version"
			a.notify("The update failed: " + a.upd.err)
		})
	})
}
