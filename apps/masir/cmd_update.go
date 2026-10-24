package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
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
	if err := startUpdate(pb.UpdateTarget_UPDATE_TARGET_MASIR, force); err != nil {
		return err
	}
	return watchUpdate(pb.UpdateTarget_UPDATE_TARGET_MASIR)
}

func cmdUpdateGeo(a args) error {
	if err := startUpdate(pb.UpdateTarget_UPDATE_TARGET_GEO, a.boolean("force", false)); err != nil {
		return err
	}
	return watchUpdate(pb.UpdateTarget_UPDATE_TARGET_GEO)
}

func startUpdate(target pb.UpdateTarget, force bool) error {
	_, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.StartUpdateResponse, error) {
		return c.StartUpdate(ctx, &pb.StartUpdateRequest{Target: target, Force: force})
	})
	return err
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
	sp := ui.SpinWith("Starting the "+updateWord(target)+" update", "Stopped watching")
	speed := newSpeed()
	var job *pb.UpdateJob
	for {
		res, err := stream.Recv()
		if err != nil {
			if appCtx.Err() != nil {
				ui.Info("The update goes on in the background: `masir update status` follows it, `masir update pause` or `stop` controls it.")
				return nil
			}
			if errors.Is(err, io.EOF) {
				break
			}
			if job != nil && job.State == pb.UpdateState_UPDATE_STATE_RESTARTING {
				break // the daemon went away to restart, as expected
			}
			sp.Fail(message(err))
			return errReported
		}
		job = res.Job
		if job.State == pb.UpdateState_UPDATE_STATE_DOWNLOADING {
			sp.Message(fmt.Sprintf("Downloading %s  %s%s", job.File, progressText(job), speed.text(job.BytesDone)))
		} else {
			sp.Message(stateText(job))
		}
	}
	if job == nil {
		sp.Fail("the daemon didn't report the update")
		return errReported
	}

	switch job.State {
	case pb.UpdateState_UPDATE_STATE_DONE:
		if target == pb.UpdateTarget_UPDATE_TARGET_GEO {
			sp.Stop(fmt.Sprintf("Geo data updated to %s; Xray is using it now.", job.Version))
		} else {
			sp.Stop("Update installed.")
		}
	case pb.UpdateState_UPDATE_STATE_UP_TO_DATE:
		if target == pb.UpdateTarget_UPDATE_TARGET_GEO {
			sp.Stop(fmt.Sprintf("Geo data is already the latest (%s).", job.Version))
		} else {
			sp.Stop(fmt.Sprintf("masir %s is already installed.", job.Version))
		}
	case pb.UpdateState_UPDATE_STATE_PAUSED:
		sp.Stop(fmt.Sprintf("Paused at %s. `masir update resume` continues it.", progressText(job)))
	case pb.UpdateState_UPDATE_STATE_STOPPED:
		sp.Stop("The update was stopped.")
	case pb.UpdateState_UPDATE_STATE_FAILED:
		sp.Fail("Update failed: " + job.Error)
		return errReported
	case pb.UpdateState_UPDATE_STATE_RESTARTING:
		sp.Message("Starting masir " + job.Version)
		if err := awaitDaemon(job.Version); err != nil {
			sp.Fail(err.Error())
			return errReported
		}
		sp.Stop(fmt.Sprintf("The masir %s daemon is running.", job.Version))
		return updateSelf(job)
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
		ui.Success("masir " + job.Version + " is installed.")
		return nil
	}
	if err := replaceSelf(src, self); err != nil {
		ui.Warn(fmt.Sprintf("The daemon is updated, but this copy of masir couldn't replace itself (%v). Run:\n  sudo cp %q %q", err, src, self))
		return nil
	}
	ui.Success("masir " + job.Version + " is installed.")
	return nil
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
		return formatBytes(j.BytesDone)
	}
	return fmt.Sprintf("%d%% · %s of %s", j.BytesDone*100/j.BytesTotal, formatBytes(j.BytesDone), formatBytes(j.BytesTotal))
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
	case pb.UpdateState_UPDATE_STATE_STOPPED:
		return "stopped" + when
	}
	return stateText(j)
}

// speed averages download speed over the last few seconds.
type speed struct {
	samples []sample
}

type sample struct {
	at    time.Time
	bytes int64
}

func newSpeed() *speed { return &speed{} }

func (s *speed) text(done int64) string {
	now := time.Now()
	s.samples = append(s.samples, sample{now, done})
	for len(s.samples) > 1 && now.Sub(s.samples[0].at) > 5*time.Second {
		s.samples = s.samples[1:]
	}
	first := s.samples[0]
	dt := now.Sub(first.at).Seconds()
	if dt < 0.5 || done <= first.bytes {
		return ""
	}
	return " · " + formatBytes(int64(float64(done-first.bytes)/dt)) + "/s"
}
