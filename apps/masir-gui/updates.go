package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"google.golang.org/protobuf/proto"

	"github.com/narixius/masir-go/libs/masir-core/service"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// The GUI asks the daemon for a release when the person opens the panel or focuses the
// window, at most every updateForegroundEvery: a daily cache would leave a security
// release unnoticed for a day. It asks the signed app feed when that release is one the
// feed hasn't answered for, and every updateFeedEvery regardless, since the app is
// released from the same tag but may one day not be.
const (
	// updateCheckEvery is how old the daemon's release check may be before a status
	// makes the GUI ask for a newer one.
	updateCheckEvery = 24 * time.Hour
	// updateForegroundEvery is what opening the panel, or focusing the window, asks for.
	updateForegroundEvery = 10 * time.Minute
	// updateFeedEvery is the fallback cadence for the signed desktop app feed.
	updateFeedEvery = 6 * time.Hour
	// updateRetryWait is how soon a failed check is tried again. A lost network at launch
	// must not hide a security update for a day, and a failing GitHub must not be asked
	// on every open.
	updateRetryWait = 15 * time.Minute
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
	// appCheckedVersion is the release the signed app feed last answered for,
	// appCheckedAt is when it answered, and appAttempt is when it was last asked, so a
	// failed check is retried later rather than on every status.
	appCheckedVersion string
	appCheckedAt      time.Time
	appAttempt        time.Time
	// lastAttempt keeps a failed release check from being repeated on every status, and
	// lastForeground is the last check an open panel asked for.
	lastAttempt    time.Time
	lastForeground time.Time
	lastStart      int64
	// download progress for the app update; the daemon job has its own bytes.
	progress, total int64
	appPaused       bool
	appCancelled    bool
	appCancel       context.CancelFunc
	appRun          int
	watchCancel     context.CancelFunc
	ignoreBusy      bool
	attached        bool
	attaching       bool
	sawJob          bool // the current watch stream saw a job that was doing something
	restarting      bool
	// tunWasOn remembers the tunnel before a daemon update, so the update can report
	// that it didn't come back instead of calling itself installed.
	tunWasOn bool
	// gapSeen is the daemon/app version pair already checked, so a persistent gap asks
	// for a fresh release once instead of on every status.
	gapSeen string
	// wantApp says the person asked for the update and the app feed hasn't answered yet.
	// A bundled daemon is updated by the app, so the answer decides what runs.
	wantApp bool
	// relaunchAfterRestart relaunches the app once a bundled daemon has restarted into
	// the app update's copy.
	relaunchAfterRestart bool
}

// daemonWaitTimeout bounds how long a restarted daemon may take to answer with the new
// version (and the tunnel). Tests shorten it.
var daemonWaitTimeout = 2 * time.Minute

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
		a.upd.appCheckedVersion = ""
		a.upd.appCheckedAt = time.Time{}
		a.upd.lastAttempt = time.Time{}
		a.upd.attached = false
		if a.upd.phase == updateAvailable || a.upd.phase == updateFailed {
			a.upd.phase = updateIdle
		}
	}
	if start != 0 {
		a.upd.lastStart = start
	}
	if rel := st.GetLatestRelease(); rel != nil {
		a.applyRelease(rel)
	}
	// A status without a release (a daemon that restarted, or one that hasn't checked
	// yet) is not news that the release is gone: it keeps the answer the last check
	// brought, and a daemon restart clears it above.
	a.noteVersionGap(st)
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
	a.upd.rel = rel
	if a.upd.phase == updateAvailable && !a.hasUpdate() {
		a.upd.phase = updateIdle
	}
	if a.upd.phase == updateIdle && a.hasUpdate() {
		a.upd.phase = updateAvailable
	}
}

// noteVersionGap notices that the app and the daemon run different releases. The
// daemon's release check is cached for a day, so it can still say "up to date" for the
// old version; ask GitHub through it with Force. When the app is already past the
// daemon (an app-only update), finish the same transaction by updating the service
// automatically. A gap that is still there is not checked again.
func (a *app) noteVersionGap(st *pb.Status) {
	if a.busyUpdate() || a.upd.checking || a.upd.restarting {
		return
	}
	if a.version == "" || st.GetVersion() == "" {
		return
	}
	if sameVersion(a.version, st.GetVersion()) {
		a.upd.gapSeen = ""
		return
	}
	gap := a.version + "|" + st.GetVersion()
	if a.upd.gapSeen == gap {
		return
	}
	a.upd.gapSeen = gap
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

// maybeCheckUpdates is the status-driven check: the daemon's daily cache is what it
// trusts, so a status only asks when that cache is a day old.
func (a *app) maybeCheckUpdates() {
	a.refreshUpdateCheck(updateCheckEvery)
}

// checkForUpdateOnForeground is what opening the panel, or focusing the window, asks for:
// a release reflected while the person is looking, at most every updateForegroundEvery.
// It is cheap when nothing changed: the daemon answers a conditional check with a 304
// that carries no body.
func (a *app) checkForUpdateOnForeground() {
	if !a.upd.lastForeground.IsZero() && time.Since(a.upd.lastForeground) < updateForegroundEvery {
		return
	}
	a.upd.lastForeground = time.Now()
	a.refreshUpdateCheck(updateForegroundEvery)
}

// refreshUpdateCheck asks the daemon for a release younger than maxAge, then the signed
// app feed for a release it hasn't answered for. Neither call polls.
func (a *app) refreshUpdateCheck(maxAge time.Duration) {
	if a.busyUpdate() || a.upd.checking || a.upd.restarting {
		return
	}
	rel := a.upd.rel
	if rel == nil || time.Since(time.UnixMilli(rel.GetCheckedAtMs())) >= maxAge {
		if a.upd.lastAttempt.IsZero() || time.Since(a.upd.lastAttempt) >= updateRetryWait {
			a.upd.lastAttempt = time.Now()
			a.checkDaemonRelease(maxAge)
			return // its answer asks the app feed, with the release it brought
		}
		// A recent attempt failed, or a daemon too old to accept max age keeps answering
		// from its own cache. The feed is still worth asking: it is the app's own.
	}
	a.checkAppFeed()
}

// appFeedDue reports whether the signed app feed should be asked: a release version it
// hasn't answered for, updateFeedEvery since it last answered, or a check that never
// succeeded. With no daemon release at all the feed is still asked on its own schedule,
// because the app carries a daemon and can fix one that won't start.
func (a *app) appFeedDue() bool {
	rel := a.upd.rel
	if rel != nil && a.ignored(rel.GetLatestVersion()) {
		return false
	}
	if !a.appFeedAnswered() {
		return true
	}
	return time.Since(a.upd.appCheckedAt) >= updateFeedEvery
}

// appFeedAnswered reports whether the signed app feed has answered for the release the
// daemon last reported.
func (a *app) appFeedAnswered() bool {
	rel := a.upd.rel
	return rel != nil && !a.upd.appCheckedAt.IsZero() && a.upd.appCheckedVersion == rel.GetLatestVersion()
}

// checkAppFeed asks the app feed when it is due, and not again until a failed check may
// be retried.
func (a *app) checkAppFeed() {
	if !a.appFeedDue() {
		return
	}
	if !a.upd.appAttempt.IsZero() && time.Since(a.upd.appAttempt) < updateRetryWait {
		return
	}
	a.checkAppRelease()
}

// checkDaemonRelease asks the daemon for the latest release, saying how old an answer it
// may give. A daemon that predates the hint ignores it and answers from its own cache.
func (a *app) checkDaemonRelease(maxAge time.Duration) {
	a.upd.checking = true
	a.async(func() {
		ctx, cancel := rpc()
		res, err := a.d.CheckUpdate(ctx, &pb.CheckUpdateRequest{MaxAgeMs: maxAge.Milliseconds()})
		cancel()
		a.post(func() {
			a.upd.checking = false
			if err == nil && res.GetRelease() != nil {
				// The attempt worked, so a later open may ask again: the retry wait is
				// for failures, and an open should not inherit the launch check's.
				a.upd.lastAttempt = time.Time{}
				a.applyRelease(res.GetRelease())
			}
			a.maybeCheckUpdates()
		})
	})
}

// checkAppRelease asks MyGo for a signed update of the desktop app itself.
func (a *app) checkAppRelease() {
	key := ""
	if a.upd.rel != nil {
		key = a.upd.rel.GetLatestVersion()
	}
	a.upd.appAttempt = time.Now()
	if !appUpdatesEnabled() {
		a.upd.appCheckedVersion, a.upd.appCheckedAt = key, time.Now()
		if a.upd.wantApp {
			a.upd.wantApp = false
			a.upd.phase = updateIdle
		}
		return
	}
	a.async(func() {
		ctx, cancel := rpc()
		u, err := checkAppUpdate(ctx)
		cancel()
		a.post(func() {
			if err == nil {
				a.upd.app = u
				// Keyed by the release the answer belongs to, so a release that appeared
				// while this was in flight asks the feed again rather than trusting it.
				a.upd.appCheckedVersion, a.upd.appCheckedAt = key, time.Now()
			}
			if a.upd.wantApp {
				a.upd.wantApp = false
				if u != nil {
					a.startAppUpdate()
					return
				}
				a.upd.phase = updateIdle
				a.upd.err = ""
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

// daemonUpdateAvailable reports whether the daemon and CLI have a newer release that
// this copy may install. A daemon running from the desktop app's bundle is the app's
// copy: only the app updater may replace it. A service or standalone copy is the
// daemon's own to update from the signed release.
func (a *app) daemonUpdateAvailable() bool {
	if a.daemonIsBundled() {
		return false
	}
	return a.daemonReleaseAvailable()
}

// daemonIsBundled reports whether the daemon the app starts or talks to is the app's
// own copy. The path says so even when the daemon predates the status field, which is
// the first update after this change.
func (a *app) daemonIsBundled() bool {
	if a.status != nil && a.status.GetBundled() {
		return true
	}
	p := filepath.ToSlash(a.binary)
	return strings.Contains(p, ".app/Contents/") ||
		strings.Contains(p, "/resources/bin/") || strings.Contains(p, "/Resources/bin/")
}

// daemonReleaseAvailable is the release check alone, before deciding which copy (the
// app's bundle or the service) is the one that may install it.
func (a *app) daemonReleaseAvailable() bool {
	rel := a.upd.rel
	return rel != nil && rel.GetAvailable() && !a.ignored(rel.GetLatestVersion())
}

// appUpdateCoversDaemon reports whether the only way to update this daemon is the app
// update, because it runs from the app bundle.
func (a *app) appUpdateCoversDaemon() bool {
	return a.daemonIsBundled()
}

// appUpdateAvailable reports whether the signed desktop app has a newer release.
func (a *app) appUpdateAvailable() bool {
	return a.upd.app != nil && !a.ignored(a.upd.app.Version)
}

// hasUpdate reports whether either side has an update the person has not ignored. A
// bundled daemon's release is shown too: the app update is how it is installed.
func (a *app) hasUpdate() bool {
	if a.daemonUpdateAvailable() || a.appUpdateAvailable() {
		return true
	}
	// A bundled daemon is updated by the app. Until the app feed has answered for this
	// release, it is still worth showing; once it says the app is current, it isn't.
	return a.appUpdateCoversDaemon() && a.daemonReleaseAvailable() && !a.appFeedAnswered()
}

// updateInFlight answers the daemon watch from another goroutine. The update state is
// main-thread only, so the question is asked there and the answer carried back.
func (a *app) updateInFlight() bool {
	var busy bool
	a.post(func() { busy = a.busyUpdate() || a.upd.restarting })
	return busy
}

// servicePresent answers whether a service is installed, from the main thread. It also
// asks the filesystem, so a status that hasn't arrived yet can't make the app start a
// second daemon beside a service.
func (a *app) servicePresent() bool {
	var present bool
	a.post(func() {
		present = (a.status != nil && a.status.GetServiceInstalled()) || service.Installed()
	})
	return present
}

// daemonPath is a.binary, read on the main thread.
func (a *app) daemonPath() string {
	var p string
	a.post(func() { p = a.binary })
	return p
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
		return
	}
	if a.appUpdateCoversDaemon() {
		// The daemon runs from the app bundle, so the app update is the update. Its
		// feed may still be answering; start it as soon as it does.
		a.upd.wantApp = true
		a.upd.phase = updateStarting
		a.checkAppRelease() // asks now, whatever the cadence says
	}
}

func (a *app) startDaemonUpdate() {
	a.upd.phase = updateStarting
	a.upd.tunWasOn = a.status != nil && a.status.GetTunRunning()
	client := ""
	if a.cli.installed {
		client = a.cli.path
	}
	root := a.privileged()
	a.async(func() {
		if service.Installed() && !root {
			// The service is the supervisor. Starting a session root daemon here would
			// replace it (launchd doesn't restart a clean exit), so ask the person to
			// start the service instead of quietly taking over.
			a.post(func() {
				a.upd.phase = updateFailed
				a.upd.err = "the background service isn't running; start it in Settings"
				a.notify("The update failed: " + a.upd.err)
			})
			return
		}
		ctx, cancel := rpc()
		res, err := a.d.StartUpdate(ctx, &pb.StartUpdateRequest{
			Target: pb.UpdateTarget_UPDATE_TARGET_MASIR, Trigger: "user", ClientBinary: client,
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
	if updateActive(job.GetState()) {
		a.upd.sawJob = true
	}
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
		a.daemonAfterInstall(job)
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
// to restart into the new version. A stream that saw a real job leaves the attach flag
// clear, so the next job (for example one started by the CLI) is followed too.
func (a *app) daemonWatchEnded() {
	a.upd.attaching = false
	if a.upd.sawJob {
		a.upd.sawJob = false
		a.upd.attached = false
	}
	if a.upd.phase == updateRestarting && a.upd.restarting {
		return // restartDaemon is following the daemon it asked to restart
	}
	if a.upd.phase == updateRestarting {
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

// awaitDaemon waits for the restarted daemon to report the version the update installed,
// and for the tunnel to come back when it was on before the update.
func (a *app) awaitDaemon(version string) {
	a.async(func() {
		if err := a.waitForDaemon(version, daemonWaitTimeout); err != nil {
			msg := err.Error()
			if service.Installed() {
				msg += "; run `masir daemon rollback` to put the previous version back"
			}
			a.post(func() {
				a.upd.phase = updateFailed
				a.upd.err = msg
				a.notify("The update failed: " + a.upd.err)
			})
			return
		}
		a.post(a.daemonFinished)
	})
}

// waitForDaemon polls until the daemon reports version and, if the tunnel was on
// before the update, has it up again. It never treats a daemon without the tunnel as
// installed.
func (a *app) waitForDaemon(version string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	sawVersion := false
	for time.Now().Before(deadline) {
		ctx, cancel := rpc()
		res, err := a.d.GetStatus(ctx, &pb.GetStatusRequest{})
		cancel()
		if err == nil {
			st := res.GetStatus()
			if version == "" || sameVersion(st.GetVersion(), version) {
				sawVersion = true
				if !a.upd.tunWasOn || st.GetTunRunning() {
					return nil
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if sawVersion {
		return errors.New("the daemon came back without the tunnel; it will be back when masir reconnects")
	}
	return errors.New("the daemon didn't come back after the update")
}

// daemonAfterInstall handles a job that installed without restarting (an older daemon,
// or a target that has no restart step): restart only when the running version differs.
func (a *app) daemonAfterInstall(job *pb.UpdateJob) {
	if a.status != nil && job.GetVersion() != "" && sameVersion(a.status.GetVersion(), job.GetVersion()) {
		a.daemonFinished()
		return
	}
	if job.GetVersion() != "" {
		a.upd.tunWasOn = a.status != nil && a.status.GetTunRunning()
		a.restartDaemon(job.GetVersion())
		return
	}
	a.daemonFinished()
}

// daemonFinished moves on to the app update, or back to the version text.
func (a *app) daemonFinished() {
	if a.upd.phase == updateAppDownloading || a.upd.phase == updateAppPaused || a.upd.phase == updateAppInstalling {
		return // the app update already started from the daemon's final job
	}
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
				a.upd.phase = updateIdle
				// A daemon that runs from the old bundle must restart into the new one
				// before the app relaunches, so the tunnel comes back with this release.
				// If no daemon is running there is nothing to restart: the new app starts
				// one itself.
				if a.appUpdateCoversDaemon() && a.reachable && a.status != nil && !sameVersion(a.status.GetVersion(), u.Version) {
					a.upd.tunWasOn = a.status.GetTunRunning()
					a.upd.relaunchAfterRestart = true
					a.restartDaemon(u.Version)
					return
				}
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
	case a.daemonReleaseAvailable():
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

// restartDaemon asks the daemon to start the binary now on disk: the service or the
// app's watch starts the same process again. It waits for the expected version and the
// tunnel, so a binary that installs but doesn't run is a failure, not a success.
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
		if err := a.waitForDaemon(version, daemonWaitTimeout); err != nil {
			msg := err.Error()
			if service.Installed() {
				msg += "; run `masir daemon rollback` to put the previous version back"
			}
			a.post(func() {
				a.upd.restarting = false
				a.upd.relaunchAfterRestart = false
				a.upd.phase = updateFailed
				a.upd.err = msg
				a.notify("The update failed: " + a.upd.err)
			})
			return
		}
		relaunch := a.upd.relaunchAfterRestart
		a.post(func() {
			a.upd.restarting = false
			a.upd.relaunchAfterRestart = false
			a.upd.phase = updateIdle
			if relaunch {
				relaunchApp()
			}
		})
	})
}
