package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
	"github.com/narixius/masir-go/libs/masir-core/service"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// cmdUpdate updates masir: the daemon installs the new release over its own binary,
// then this CLI updates itself too if it's a different copy (the root service keeps
// its own).
func cmdUpdate(a args) error {
	sp := ui.Spin("Checking for updates")
	res, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.CheckUpdateResponse, error) {
		return c.CheckUpdate(ctx, &pb.CheckUpdateRequest{})
	})
	if err != nil {
		sp.Fail("Couldn't check for updates: " + message(err))
		return errReported
	}
	rel := res.Release
	force := a.boolean("force", false)
	switch {
	case rel.Available:
		sp.Stop(fmt.Sprintf("masir %s is available %s", ui.Green(rel.LatestVersion), ui.Dim("(you have "+rel.CurrentVersion+")")))
		ui.Message(ui.Dim(rel.Url))
	case !force:
		sp.Stop(fmt.Sprintf("masir %s is the latest version.", rel.CurrentVersion))
		if !strings.HasPrefix(rel.CurrentVersion, "v") && rel.CurrentVersion != rel.LatestVersion {
			ui.Info(fmt.Sprintf("This is a development build; `masir update --force` installs %s.", rel.LatestVersion))
		}
		return nil
	default:
		sp.Stop(fmt.Sprintf("Reinstalling masir %s.", rel.LatestVersion))
	}
	if a.boolean("check", false) {
		return nil
	}
	if rel.Available && !a.boolean("yes", false) {
		if !ui.Interactive() {
			return fail("pass --yes to install %s without a terminal", rel.LatestVersion)
		}
		ok, err := ui.Confirm(fmt.Sprintf("Install masir %s now? The proxy restarts for a moment.", rel.LatestVersion), true)
		if err != nil || !ok {
			return err
		}
	}
	if self, err := self(); err == nil && !writableDir(filepath.Dir(self)) {
		if st, err := currentStatus(); err == nil && !st.Privileged {
			ui.Note(fmt.Sprintf("The daemon isn't running with %s, so it can't update your copy at %s directly.\nmasir will ask for permission at the end, after the daemon is updated.",
				privilegeOwner, self), ui.Yellow("▲ Your copy needs permission"))
		}
	}
	if err := startUpdate(pb.UpdateTarget_UPDATE_TARGET_MASIR, force, false); err != nil {
		return err
	}
	return watchUpdate(pb.UpdateTarget_UPDATE_TARGET_MASIR)
}

// writableDir reports whether this user may create a file in dir.
func writableDir(dir string) bool {
	f, err := os.CreateTemp(dir, ".masir-write-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
}

func cmdUpdateGeo(a args) error {
	if err := startUpdate(pb.UpdateTarget_UPDATE_TARGET_GEO, a.boolean("force", false), a.boolean("disable-broken-rules", false)); err != nil {
		return err
	}
	return watchUpdate(pb.UpdateTarget_UPDATE_TARGET_GEO)
}

func startUpdate(target pb.UpdateTarget, force, disableBroken bool) error {
	client, _ := self()
	_, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.StartUpdateResponse, error) {
		return c.StartUpdate(ctx, &pb.StartUpdateRequest{Target: target, Force: force, ClientBinary: client, Trigger: "user", DisableBrokenRules: disableBroken})
	})
	return err
}

// blockedGeoUpdate is what the user sees when the daemon found that the new geo data can't
// build some enabled rules, and so installed nothing. Whether to turn those rules off is
// the user's call; with no terminal to ask, the flag says it ahead of time.
func blockedGeoUpdate(job *pb.UpdateJob) error {
	if !ui.Interactive() {
		ui.Warn(fmt.Sprintf("The new geo data wasn't installed: it can't build %s:", counted(len(job.BlockedRules), "of your rules")))
		fmt.Println(ui.Visual(indent(problemText(job.BlockedRules), "  ")))
		fmt.Println(ui.Dim("The current geo data is unchanged. Fix or remove those rules (`masir rules edit`), or turn them off and install with `masir update geo --disable-broken-rules`."))
		return errReported
	}
	ui.Note(problemText(job.BlockedRules), ui.Yellow("▲ Rules the new geo data can't build"))
	ok, err := ui.Confirm("Turn these rules off and install the new geo data? (They stay saved; the current data stays if you say no.)", false)
	if err != nil {
		return err
	}
	if !ok {
		ui.Info("Kept the current geo data. Fix or remove those rules (`masir rules edit`), then run `masir update geo` again.")
		return nil
	}
	if err := startUpdate(pb.UpdateTarget_UPDATE_TARGET_GEO, false, true); err != nil {
		return err
	}
	return watchUpdate(pb.UpdateTarget_UPDATE_TARGET_GEO)
}

func cmdUpdatePause(a args) error {
	target, err := updateTarget(a, "pause")
	if err != nil {
		return err
	}
	res, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.PauseUpdateResponse, error) {
		return c.PauseUpdate(ctx, &pb.PauseUpdateRequest{Target: target})
	})
	if err != nil {
		return err
	}
	ui.Success(fmt.Sprintf("Paused the %s update at %s. `masir update resume` continues it.", updateWord(target), progressText(res.Job)))
	return nil
}

func cmdUpdateResume(a args) error {
	target, err := updateTarget(a, "resume")
	if err != nil {
		return err
	}
	if _, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.ResumeUpdateResponse, error) {
		return c.ResumeUpdate(ctx, &pb.ResumeUpdateRequest{Target: target})
	}); err != nil {
		return err
	}
	return watchUpdate(target)
}

func cmdUpdateStop(a args) error {
	target, err := updateTarget(a, "stop")
	if err != nil {
		return err
	}
	if _, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.StopUpdateResponse, error) {
		return c.StopUpdate(ctx, &pb.StopUpdateRequest{Target: target})
	}); err != nil {
		return err
	}
	ui.Success(fmt.Sprintf("Stopped the %s update and deleted what it downloaded.", updateWord(target)))
	return nil
}

// cmdUpdateStatus shows each update job, and follows one that's running.
func cmdUpdateStatus(a args) error {
	targets := []pb.UpdateTarget{pb.UpdateTarget_UPDATE_TARGET_MASIR, pb.UpdateTarget_UPDATE_TARGET_GEO}
	if a.arg(0) != "" {
		t, err := parseTarget(a.arg(0))
		if err != nil {
			return err
		}
		targets = []pb.UpdateTarget{t}
	}
	for _, t := range targets {
		job, err := currentJob(t)
		if err != nil {
			return err
		}
		switch job.State {
		case pb.UpdateState_UPDATE_STATE_DOWNLOADING, pb.UpdateState_UPDATE_STATE_CHECKING,
			pb.UpdateState_UPDATE_STATE_VERIFYING, pb.UpdateState_UPDATE_STATE_INSTALLING:
			if err := watchUpdate(t); err != nil {
				return err
			}
		default:
			ui.Info(fmt.Sprintf("%s: %s", capitalize(updateWord(t)), jobSummary(job)))
		}
	}
	return nil
}

// updateTarget is the target a control command names, or the only job in progress.
func updateTarget(a args, verb string) (pb.UpdateTarget, error) {
	if a.arg(0) != "" {
		return parseTarget(a.arg(0))
	}
	var active []pb.UpdateTarget
	for _, t := range []pb.UpdateTarget{pb.UpdateTarget_UPDATE_TARGET_MASIR, pb.UpdateTarget_UPDATE_TARGET_GEO} {
		job, err := currentJob(t)
		if err != nil {
			return 0, err
		}
		if inProgress(job.State) {
			active = append(active, t)
		}
	}
	switch len(active) {
	case 0:
		return 0, fail("no update is in progress")
	case 1:
		return active[0], nil
	}
	return 0, fail("both masir and geo data are updating; say which: masir update %s masir|geo", verb)
}

func parseTarget(s string) (pb.UpdateTarget, error) {
	switch strings.ToLower(s) {
	case "masir", "app":
		return pb.UpdateTarget_UPDATE_TARGET_MASIR, nil
	case "geo", "geodata":
		return pb.UpdateTarget_UPDATE_TARGET_GEO, nil
	}
	return 0, fail("%q isn't masir or geo", s)
}

// currentJob reads a job's state: WatchUpdate sends it first.
func currentJob(t pb.UpdateTarget) (*pb.UpdateJob, error) {
	c, err := daemon()
	if err != nil {
		return nil, err
	}
	ctx, cancel := rpc()
	defer cancel()
	stream, err := c.WatchUpdate(ctx, &pb.WatchUpdateRequest{Target: t})
	if err != nil {
		return nil, err
	}
	res, err := stream.Recv()
	if err != nil {
		return nil, err
	}
	return res.Job, nil
}

func inProgress(s pb.UpdateState) bool {
	switch s {
	case pb.UpdateState_UPDATE_STATE_CHECKING, pb.UpdateState_UPDATE_STATE_DOWNLOADING, pb.UpdateState_UPDATE_STATE_PAUSED,
		pb.UpdateState_UPDATE_STATE_VERIFYING, pb.UpdateState_UPDATE_STATE_INSTALLING:
		return true
	}
	return false
}

// watchUpdate follows a job with a spinner until it ends. Ctrl-C only stops watching:
// the job carries on in the daemon.
func watchUpdate(target pb.UpdateTarget) error {
	c, err := daemon()
	if err != nil {
		return err
	}
	stream, err := c.WatchUpdate(appCtx, &pb.WatchUpdateRequest{Target: target})
	if err != nil {
		return err
	}
	pr := ui.NewProgress("Starting the " + updateWord(target) + " update")
	var job *pb.UpdateJob
	for {
		res, err := stream.Recv()
		if err != nil {
			if appCtx.Err() != nil {
				pr.Stop("Stopped watching. The update goes on in the background: `masir update status` follows it, `masir update pause` or `stop` controls it.")
				return nil
			}
			if errors.Is(err, io.EOF) {
				break
			}
			if job != nil && job.State == pb.UpdateState_UPDATE_STATE_RESTARTING {
				break // the daemon went away to restart, as expected
			}
			pr.Fail(message(err))
			return errReported
		}
		job = res.Job
		switch job.State {
		case pb.UpdateState_UPDATE_STATE_DOWNLOADING:
			pr.SetLabel("Downloading " + job.File)
			pr.Set(job.BytesDone, job.BytesTotal)
		case pb.UpdateState_UPDATE_STATE_CHECKING, pb.UpdateState_UPDATE_STATE_VERIFYING, pb.UpdateState_UPDATE_STATE_INSTALLING:
			pr.SetLabel(stateText(job))
			pr.Set(0, 0)
		default:
			pr.SetLabel(stateText(job))
		}
	}
	if job == nil {
		pr.Fail("the daemon didn't report the update")
		return errReported
	}

	switch job.State {
	case pb.UpdateState_UPDATE_STATE_DONE:
		if target == pb.UpdateTarget_UPDATE_TARGET_GEO {
			pr.Stop(fmt.Sprintf("Geo data updated to %s; Xray is using it now.", job.Version))
		} else {
			pr.Stop("Update installed.")
		}
	case pb.UpdateState_UPDATE_STATE_UP_TO_DATE:
		if target == pb.UpdateTarget_UPDATE_TARGET_GEO {
			pr.Stop(fmt.Sprintf("Geo data is already the latest (%s).", job.Version))
		} else {
			pr.Stop(fmt.Sprintf("masir %s is already installed.", job.Version))
		}
	case pb.UpdateState_UPDATE_STATE_PAUSED:
		pr.Stop(fmt.Sprintf("Paused at %s. `masir update resume` continues it.", progressText(job)))
	case pb.UpdateState_UPDATE_STATE_STOPPED:
		pr.Stop("The update was stopped.")
	case pb.UpdateState_UPDATE_STATE_FAILED:
		pr.Fail("Update failed: " + friendlyUpdateError(job.Error))
		return errReported
	case pb.UpdateState_UPDATE_STATE_BLOCKED:
		pr.Stop("The new geo data isn't installed yet: it can't build some of your rules.")
		return blockedGeoUpdate(job)
	case pb.UpdateState_UPDATE_STATE_RESTARTING:
		pr.SetLabel("Starting masir " + job.Version)
		failed := false
		if err := awaitDaemon(job.Version); err != nil {
			pr.Fail(err.Error())
			failed = true
		}
		if failed {
			return errReported
		}
		pr.Stop(fmt.Sprintf("The masir %s daemon is running.", job.Version))
		if err := updateSelf(job); err != nil {
			return err
		}
		return syncServiceBinary(job)
	default:
		pr.Stop("Update finished.")
	}
	return nil
}

// awaitDaemon waits for the daemon to come back running version: launchd or systemd starts
// the service again, and a background daemon restarts itself.
func awaitDaemon(version string) error {
	cachedClient = nil
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		if appCtx.Err() != nil {
			return appCtx.Err()
		}
		if c, ok := runningDaemon(); ok {
			ctx, cancel := context.WithTimeout(appCtx, 2*time.Second)
			res, err := c.GetStatus(ctx, &pb.GetStatusRequest{})
			cancel()
			if err == nil && strings.TrimPrefix(res.Status.Version, "v") == strings.TrimPrefix(version, "v") {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if _, err := daemon(); err != nil { // a background daemon that didn't come back: start it
		return fmt.Errorf("the daemon didn't come back after the update: %v", message(err))
	}
	return nil
}

// updateSelf puts the new binary in place of this one, when the daemon didn't already:
// it replaces the copy it runs from, which is a different file under the root service.
func updateSelf(job *pb.UpdateJob) error {
	if job.StagedDir == "" {
		return nil
	}
	name := "masir"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	src := filepath.Join(job.StagedDir, name)
	self, err := os.Executable()
	if err != nil {
		return nil
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	if same, _ := sameContent(src, self); same {
		trackSelfUpdate("daemon", true, job, nil)
		ui.Success("masir " + job.Version + " is installed.")
		return nil
	}
	if err := replaceSelf(src, self); err != nil {
		if os.IsPermission(err) || errors.Is(err, fs.ErrPermission) {
			return finishSelfWithElevation(job, src, self)
		}
		trackSelfUpdate("failed", false, job, err)
		ui.Warn(fmt.Sprintf("The daemon is updated to %s, but this copy of masir couldn't replace itself: %s", job.Version, friendlyFileError(err, self)))
		return nil
	}
	trackSelfUpdate("cli", true, job, nil)
	ui.Success("masir " + job.Version + " is installed.")
	return nil
}

func trackSelfUpdate(method string, success bool, job *pb.UpdateJob, err error) {
	props := map[string]any{"method": method, "success": success, "version": job.Version}
	if err != nil {
		for k, v := range errorProps(err) {
			props[k] = v
		}
	}
	captureEvent("self_update_finished", props)
}

// finishSelfWithElevation finishes the CLI update when the file needs root: the daemon
// may already be privileged and have done it; otherwise masir asks once, and if that is
// refused leaves a copyable command rather than a raw cp.
func finishSelfWithElevation(job *pb.UpdateJob, src, self string) error {
	if !ui.Interactive() {
		selfUpdateHint(job, src, self)
		return nil
	}
	ui.Note(fmt.Sprintf("The daemon is updated to %s, but your copy of masir at %s is owned by %s.\n"+
		"masir can finish the update for you; %s.", job.Version, self, privilegeOwner, elevationAsk),
		ui.Yellow("▲ One more step"))
	if err := elevate("daemon", "sync-self", "--from", src, "--to", self); err == nil {
		trackSelfUpdate("sudo", true, job, nil)
		ui.Success("masir " + job.Version + " is installed.")
		return nil
	} else if cancelled(err) {
		return err
	}
	selfUpdateHint(job, src, self)
	return nil
}

// selfUpdateHint leaves one copyable command when masir can't finish by itself.
func selfUpdateHint(job *pb.UpdateJob, src, dst string) {
	trackSelfUpdate("manual", false, job, nil)
	ui.Warn("The daemon is updated, but this copy of masir still has the old version.")
	if runtime.GOOS == "windows" {
		ui.Message("Run this once as Administrator:")
	} else {
		ui.Message("To finish it yourself, run:")
	}
	ui.Command(syncSelfCommand(src, dst))
}

// syncSelfCommand is the manual fallback, safe to copy as one line.
func syncSelfCommand(src, dst string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf("%s daemon sync-self --from %s --to %s", quoteArg(dst), quoteArg(src), quoteArg(dst))
	}
	return fmt.Sprintf("sudo %s daemon sync-self --from %s --to %s", quoteArg(dst), quoteArg(src), quoteArg(dst))
}

// quoteArg quotes a path for the shell the fallback command is copied into.
func quoteArg(s string) string {
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// friendlyFileError explains a failed file replacement without a raw Go error.
func friendlyFileError(err error, path string) string {
	msg := err.Error()
	switch {
	case os.IsPermission(err) || strings.Contains(msg, "permission denied"):
		return fmt.Sprintf("it doesn't have permission to write %s", path)
	case strings.Contains(msg, "read-only file system"):
		return fmt.Sprintf("%s is on a read-only filesystem", path)
	case strings.Contains(msg, "no space left on device"):
		return "the disk is full"
	case strings.Contains(msg, "text file busy"):
		return "the file is still running; try again in a moment"
	case strings.Contains(msg, "being used by another process"):
		return "another process is using the file; close it and try again"
	}
	return msg
}

// installPath pulls the path out of the daemon's "install <path>: <error>" message.
func installPath(raw string) string {
	rest, ok := strings.CutPrefix(raw, "install ")
	if !ok {
		return ""
	}
	if i := strings.Index(rest, ": "); i > 0 {
		return rest[:i]
	}
	return ""
}

// friendlyUpdateError turns the daemon's raw update failure into what to do next.
func friendlyUpdateError(raw string) string {
	target := installPath(raw)
	if target == "" {
		target = "the daemon's binary"
	}
	switch {
	case strings.Contains(raw, "permission denied"):
		return fmt.Sprintf("masir doesn't have permission to replace %s. Run `masir daemon enable` to install the root service, or move masir to a writable directory.", target)
	case strings.Contains(raw, "read-only file system"):
		return fmt.Sprintf("%s is read-only; move masir to a writable directory.", target)
	case strings.Contains(raw, "no space left on device"):
		return "the disk is full; free some space and run `masir update` again."
	case strings.Contains(raw, "text file busy"), strings.Contains(raw, "being used by another process"):
		return "the binary is in use; close the other masir process and run `masir update` again."
	case strings.Contains(raw, "checksum"):
		return raw + " Run `masir update` again to download a fresh copy."
	}
	return raw
}

// syncServiceBinary brings the system service's own copy of masir up to the new
// version when the update didn't (it ran in a daemon that isn't the service, without
// the rights to write there). Only when the service is installed and its binary exists.
func syncServiceBinary(job *pb.UpdateJob) error {
	if job.StagedDir == "" || !service.Installed() {
		return nil
	}
	dst := service.BinaryPath()
	if _, err := os.Stat(dst); err != nil {
		return nil
	}
	name := "masir"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	src := filepath.Join(job.StagedDir, name)
	if same, _ := sameContent(src, dst); same {
		return nil
	}
	if !elevated() {
		ui.Note(fmt.Sprintf("masir's system service runs its own copy at %s, which only %s can change.\n"+
			"To update it to %s as well, so the service doesn't start the old version, "+elevationAsk+".",
			dst, privilegeOwner, job.Version), ui.Yellow("▲ Updating the system service"))
		if err := elevate("daemon", "sync-service", "--from", src); err != nil {
			if !errors.Is(err, errReported) {
				ui.Warn(fmt.Sprintf("The service still has the old version (%s). `masir daemon enable` updates it.", message(err)))
			}
			return nil
		}
		ui.Success("The system service is updated to " + job.Version + ".")
		return nil
	}
	if err := syncService(src); err != nil {
		return err
	}
	ui.Success("The system service is updated to " + job.Version + ".")
	return nil
}

// daemonSyncService copies a new masir binary into the service's location. It's what
// syncServiceBinary runs with root rights.
func daemonSyncService(a args) error {
	if !elevated() {
		return elevate("daemon", "sync-service", "--from", a.str("from"))
	}
	return syncService(a.str("from"))
}

// daemonSyncSelf replaces a masir binary with a staged one. It's what updateSelf runs
// with root rights when the CLI's own copy is root-owned.
func daemonSyncSelf(a args) error {
	src, dst := a.str("from"), a.str("to")
	if src == "" {
		return fail("--from names the new masir binary")
	}
	if dst == "" {
		var err error
		if dst, err = self(); err != nil {
			return err
		}
	}
	if !elevated() {
		return elevate("daemon", "sync-self", "--from", src, "--to", dst)
	}
	if err := replaceSelf(src, dst); err != nil {
		return fail("replace %s: %s", dst, friendlyFileError(err, dst))
	}
	return nil
}

func syncService(src string) error {
	if src == "" {
		return fail("--from names the new masir binary")
	}
	if _, err := os.Stat(src); err != nil {
		return err
	}
	stopServiceForInstall() // Windows can't replace a running executable
	return copyFile(src, service.BinaryPath(), 0o755)
}

func replaceSelf(src, dst string) error {
	tmp := dst + ".new"
	if err := copyFile(src, tmp, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" { // a running .exe can be renamed but not overwritten
		os.Remove(dst + ".old")
		if err := os.Rename(dst, dst+".old"); err != nil {
			os.Remove(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func sameContent(a, b string) (bool, error) {
	ha, err := hashFile(a)
	if err != nil {
		return false, err
	}
	hb, err := hashFile(b)
	return err == nil && ha == hb, err
}

func hashFile(path string) ([32]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return [32]byte{}, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return [32]byte{}, err
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out, nil
}

func updateWord(t pb.UpdateTarget) string {
	if t == pb.UpdateTarget_UPDATE_TARGET_GEO {
		return "geo data"
	}
	return "masir"
}

func stateText(j *pb.UpdateJob) string {
	switch j.State {
	case pb.UpdateState_UPDATE_STATE_CHECKING:
		return "Finding the latest release"
	case pb.UpdateState_UPDATE_STATE_VERIFYING:
		return "Verifying checksums"
	case pb.UpdateState_UPDATE_STATE_INSTALLING:
		return "Installing " + j.Version
	case pb.UpdateState_UPDATE_STATE_PAUSED:
		return "Paused at " + progressText(j)
	}
	return capitalize(strings.ToLower(strings.TrimPrefix(j.State.String(), "UPDATE_STATE_")))
}

func progressText(j *pb.UpdateJob) string {
	if j.BytesTotal <= 0 {
		return ui.Bytes(j.BytesDone)
	}
	return fmt.Sprintf("%d%% · %s of %s", j.BytesDone*100/j.BytesTotal, ui.Bytes(j.BytesDone), ui.Bytes(j.BytesTotal))
}

func jobSummary(j *pb.UpdateJob) string {
	when := ""
	if j.FinishedAtMs > 0 {
		when = ui.Dim(" (" + time.UnixMilli(j.FinishedAtMs).Format("15:04") + ")")
	}
	switch j.State {
	case pb.UpdateState_UPDATE_STATE_UNSPECIFIED:
		return "no update since the daemon started"
	case pb.UpdateState_UPDATE_STATE_PAUSED:
		return "paused at " + progressText(j) + " — `masir update resume` continues it"
	case pb.UpdateState_UPDATE_STATE_DONE:
		return "updated to " + j.Version + when
	case pb.UpdateState_UPDATE_STATE_UP_TO_DATE:
		return "already the latest (" + j.Version + ")" + when
	case pb.UpdateState_UPDATE_STATE_FAILED:
		return ui.Red("failed: "+j.Error) + when
	case pb.UpdateState_UPDATE_STATE_BLOCKED:
		return ui.Yellow(fmt.Sprintf("not installed: the new data can't build %s", counted(len(j.BlockedRules), "of your rules"))) + when
	case pb.UpdateState_UPDATE_STATE_STOPPED:
		return "stopped" + when
	}
	return stateText(j)
}
