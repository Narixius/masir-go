package server

import (
	"context"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/narixius/masir-go/libs/masir-core/paths"
	"github.com/narixius/masir-go/libs/masir-core/service"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/rulecheck"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/store"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/update"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/xray"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

const (
	// checkEvery is how often the daemon asks GitHub for a new masir release. It only
	// tells UIs; installing waits for StartUpdate. It is also how long an ignored
	// prompt lasts when the daemon isn't restarted: a fresh check makes it show again.
	checkEvery = 48 * time.Hour
	// geoMaxAge is how old the geo files get before the daemon refreshes them itself.
	geoMaxAge = 7 * 24 * time.Hour
)

// latestRelease asks GitHub for the newest release. Tests replace it.
var latestRelease = update.Latest

// updates holds the daemon's update jobs and what the last release check found.
type updates struct {
	masir, geo *update.Runner
	release    *pb.ReleaseInfo // guarded by Server.mu
	checkMu    sync.Mutex      // one GitHub check at a time
}

func (s *Server) initUpdates(dir string) {
	s.upd = &updates{
		masir: update.NewRunner(pb.UpdateTarget_UPDATE_TARGET_MASIR, s.updateDir(dir, "masir")),
		geo:   update.NewRunner(pb.UpdateTarget_UPDATE_TARGET_GEO, s.updateDir(dir, "geo")),
	}
}

// updateDir is where a download is staged and checked before it is installed. A root
// daemon stages it where only root can write, readable by the user so the CLI can
// install from it: in the user's own directory they could swap the file after it was
// verified. It sits beside the service's own copy of masir, on disk and executable (a
// run directory is often a RAM disk mounted noexec, and the new version is run from here
// to check it before it is installed). If that directory can't be made safely, the empty
// name makes updates fail.
func (s *Server) updateDir(configDir, target string) string {
	if !s.id.Privileged || runtime.GOOS == "windows" { // Windows keeps it with the daemon's other files
		return filepath.Join(configDir, "updates", target)
	}
	dirs := privilegedUpdateDirs(s.id.UID, target)
	for _, d := range dirs {
		if err := paths.EnsureRootDir(d, 0, 0o755); err != nil {
			log.Printf("updates are off: %v", err)
			return ""
		}
	}
	return dirs[len(dirs)-1]
}

// privilegedUpdateDirs lists, outermost first, the directories a root daemon serving uid
// stages target's download in.
func privilegedUpdateDirs(uid int, target string) []string {
	install := service.InstallDir()
	updates := filepath.Join(install, "updates")
	user := filepath.Join(updates, strconv.Itoa(uid))
	return []string{install, updates, user, filepath.Join(user, target)}
}

func (s *Server) clients() update.Clients {
	// No overall timeout: a download may rightly take long. Connecting and the first
	// response byte are bounded instead.
	transport := func(dial func(ctx context.Context, network, addr string) (net.Conn, error)) *http.Transport {
		return &http.Transport{
			DialContext:           dial,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 30 * time.Second,
			Proxy:                 nil,
		}
	}
	c := update.Clients{
		Direct:    &http.Client{Transport: transport((&net.Dialer{Timeout: 15 * time.Second}).DialContext)},
		UserAgent: "masir/" + Version,
	}
	if s.xray.Running() {
		c.Proxy = &http.Client{Transport: transport(s.xray.Dial)}
	}
	return c
}

func (s *Server) runner(t pb.UpdateTarget) (*update.Runner, error) {
	switch t {
	case pb.UpdateTarget_UPDATE_TARGET_MASIR:
		return s.upd.masir, nil
	case pb.UpdateTarget_UPDATE_TARGET_GEO:
		return s.upd.geo, nil
	}
	return nil, status.Error(codes.InvalidArgument, "target must be masir or geo")
}

func (s *Server) masirTask(force bool, clientBinary, trigger string) update.Task {
	return s.wrapUpdate("masir", force, trigger, update.Masir{
		Clients:       s.clients(),
		Current:       Version,
		Force:         force,
		Binary:        executable(),
		ServiceBinary: s.serviceBinary(),
		ClientBinary:  s.clientBinary(clientBinary),
		Privileged:    s.id.Privileged,
		WorkDir:       s.updateDir(s.dir, "masir"),
		Preflight:     s.checkWithNewVersion,
		Restart:       s.restartForUpdate,
	}.Run)
}

// clientBinary accepts the CLI's path only when this daemon has the rights to replace
// it, it looks like a masir binary, and only root can change it: a copy the user owns
// could be swapped for a link while root writes it, so the CLI replaces that one itself.
func (s *Server) clientBinary(path string) string {
	if !s.id.Privileged || path == "" || s.id.UID < 0 {
		return "" // Windows has no numeric owner to preserve; the CLI elevates itself
	}
	name := filepath.Base(path)
	if name != "masir" && name != "masir.exe" {
		return ""
	}
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || !rootControlled(path) {
		return ""
	}
	return path
}

// geoTask installs fresh geo data, but only after checking that every enabled rule can be
// built with it: an update that would break rules is blocked, and only a user who has
// agreed (disableBroken) gets those rules turned off so it can go ahead.
func (s *Server) geoTask(force bool, trigger string, disableBroken bool) update.Task {
	return s.wrapUpdate("geo", force, trigger, update.Geo{
		Clients:       s.clients(),
		AssetDir:      s.xray.AssetDir(),
		WorkDir:       s.updateDir(s.dir, "geo"),
		Force:         force,
		Preflight:     s.geoPreflight,
		DisableBroken: disableBroken,
		Disable:       s.disableForGeo,
		Reload: func() error {
			err := xray.ReloadGeodata()
			s.recheckRules()
			return err
		},
	}.Run)
}

// geoPreflight returns the enabled rules the staged geo files can't build.
func (s *Server) geoPreflight(geoip, geosite string) []*pb.RuleProblem {
	s.mu.Lock()
	rules := s.state.Rules
	s.mu.Unlock()
	return toPBProblems(rulecheck.Check(rules, rulecheck.Data{GeoIP: geoip, GeoSite: geosite}), nil)
}

// disableForGeo turns off the rules a geo update would break, saying why on each.
func (s *Server) disableForGeo(ctx context.Context, problems []*pb.RuleProblem) error {
	byReason := map[string][]string{}
	for _, p := range problems {
		reason := "the updated geo data lacks " + strings.Join(p.Missing, ", ")
		byReason[reason] = append(byReason[reason], p.RuleTag)
	}
	var tagsAll []string
	for _, tags := range byReason {
		tagsAll = append(tagsAll, tags...)
	}
	// One change for all of them: with any still broken, none could be applied.
	return s.change(ctx, "rules disabled for the geo update: "+strings.Join(tagsAll, ", "), func(st *store.State) error {
		for reason, tags := range byReason {
			for _, tag := range tags {
				if err := st.SetRuleEnabled(tag, false, reason); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// recheckRules refreshes the problems status reports, after the geo data changed.
func (s *Server) recheckRules() {
	s.mu.Lock()
	s.ruleProblems = rulecheck.Check(s.state.Rules, s.geoData())
	s.mu.Unlock()
	s.changes.notify()
}

// wrapUpdate records the start and outcome of every update, including background geo
// refreshes that don't go through StartUpdate.
func (s *Server) wrapUpdate(target string, force bool, trigger string, task update.Task) update.Task {
	if trigger == "" {
		trigger = "api"
	}
	return func(ctx context.Context, r *update.Reporter) error {
		start := time.Now()
		s.track("update_started", map[string]any{
			"target":  target,
			"force":   force,
			"trigger": trigger,
		})
		err := task(ctx, r)
		if ctx.Err() != nil {
			// Paused or stopped: the control RPC records that outcome; this isn't a
			// finished update and shouldn't look like a failure.
			return err
		}
		job := r.Snapshot()
		state := strings.ToLower(strings.TrimPrefix(job.State.String(), "UPDATE_STATE_"))
		props := map[string]any{
			"target":      target,
			"state":       state,
			"duration_ms": time.Since(start).Milliseconds(),
			"bytes_done":  job.BytesDone,
			"bytes_total": job.BytesTotal,
			"version":     job.Version,
			"trigger":     trigger,
		}
		if err != nil {
			s.trackError("update_finished", err, props, false)
		} else {
			props["success"] = true
			s.track("update_finished", props)
		}
		return err
	}
}

// restartForUpdate ends the daemon so that main starts the newly installed binary.
func (s *Server) restartForUpdate() {
	s.Log("info", "restarting into the new version")
	s.TrackDaemonStopped("update")
	s.restart.Store(true)
	s.stopOnce.Do(func() { close(s.stopped) })
}

// RestartRequested reports whether the daemon stopped to start a new version.
func (s *Server) RestartRequested() bool { return s.restart.Load() }

func (s *Server) CheckUpdate(ctx context.Context, req *pb.CheckUpdateRequest) (*pb.CheckUpdateResponse, error) {
	info, err := s.checkRelease(ctx, req.GetForce())
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	s.track("update_checked", map[string]any{
		"trigger":         "user",
		"available":       info.Available,
		"current_version": info.CurrentVersion,
		"latest_version":  info.LatestVersion,
	})
	return &pb.CheckUpdateResponse{Release: info}, nil
}

// IgnoreUpdate hides the prompt for version until the next fresh check or a daemon
// restart. The update stays available for the CLI and for CheckUpdate.
func (s *Server) IgnoreUpdate(_ context.Context, req *pb.IgnoreUpdateRequest) (*pb.IgnoreUpdateResponse, error) {
	s.mu.Lock()
	if s.upd == nil || s.upd.release == nil {
		s.mu.Unlock()
		return nil, status.Error(codes.FailedPrecondition, "no update has been checked yet")
	}
	version := strings.TrimSpace(req.GetVersion())
	if version == "" {
		version = s.upd.release.LatestVersion
	}
	s.upd.release.IgnoredVersion = version
	info := proto.Clone(s.upd.release).(*pb.ReleaseInfo)
	s.mu.Unlock()
	s.changes.notify()
	s.track("update_ignored", map[string]any{"version": version})
	return &pb.IgnoreUpdateResponse{Release: info}, nil
}

// checkRelease returns the release GitHub last named, or a fresh one when force is set
// or the cache is older than checkEvery. A fresh check clears an ignored prompt.
func (s *Server) checkRelease(ctx context.Context, force bool) (*pb.ReleaseInfo, error) {
	if info := s.cachedRelease(force); info != nil {
		return info, nil
	}
	s.upd.checkMu.Lock()
	defer s.upd.checkMu.Unlock()
	if info := s.cachedRelease(force); info != nil {
		return info, nil
	}
	rel, err := latestRelease(ctx, s.clients(), update.ReleaseRepo)
	if err != nil {
		return nil, err
	}
	info := &pb.ReleaseInfo{
		CurrentVersion: Version,
		LatestVersion:  rel.Tag,
		Available:      update.Newer(rel.Tag, Version),
		Url:            rel.URL,
		PublishedAtMs:  rel.Published.UnixMilli(),
		CheckedAtMs:    time.Now().UnixMilli(),
	}
	s.mu.Lock()
	s.upd.release = info
	s.mu.Unlock()
	s.changes.notify()
	return proto.Clone(info).(*pb.ReleaseInfo), nil
}

// cachedRelease is the last check when it is still fresh, and force is not set.
func (s *Server) cachedRelease(force bool) *pb.ReleaseInfo {
	if force {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.upd == nil || s.upd.release == nil {
		return nil
	}
	if time.Since(time.UnixMilli(s.upd.release.CheckedAtMs)) >= checkEvery {
		return nil
	}
	return proto.Clone(s.upd.release).(*pb.ReleaseInfo)
}

func (s *Server) StartUpdate(_ context.Context, req *pb.StartUpdateRequest) (*pb.StartUpdateResponse, error) {
	r, err := s.runner(req.Target)
	if err != nil {
		return nil, err
	}
	trigger := req.Trigger
	if trigger == "" {
		trigger = "api"
	}
	task := s.geoTask(req.Force, trigger, req.DisableBrokenRules && trigger != "background")
	if req.Target == pb.UpdateTarget_UPDATE_TARGET_MASIR {
		task = s.masirTask(req.Force, req.ClientBinary, trigger)
	}
	job, err := r.Start(task)
	if err != nil {
		return nil, jobErr(err)
	}
	s.Log("info", "update started: "+targetName(req.Target))
	return &pb.StartUpdateResponse{Job: job}, nil
}

func (s *Server) PauseUpdate(_ context.Context, req *pb.PauseUpdateRequest) (*pb.PauseUpdateResponse, error) {
	r, err := s.runner(req.Target)
	if err != nil {
		return nil, err
	}
	job, err := r.Pause()
	if err != nil {
		return nil, jobErr(err)
	}
	s.track("update_paused", map[string]any{
		"target":      targetName(req.Target),
		"bytes_done":  job.BytesDone,
		"bytes_total": job.BytesTotal,
	})
	return &pb.PauseUpdateResponse{Job: job}, nil
}

func (s *Server) ResumeUpdate(_ context.Context, req *pb.ResumeUpdateRequest) (*pb.ResumeUpdateResponse, error) {
	r, err := s.runner(req.Target)
	if err != nil {
		return nil, err
	}
	job, err := r.Resume()
	if err != nil {
		return nil, jobErr(err)
	}
	s.track("update_resumed", map[string]any{"target": targetName(req.Target)})
	return &pb.ResumeUpdateResponse{Job: job}, nil
}

func (s *Server) StopUpdate(_ context.Context, req *pb.StopUpdateRequest) (*pb.StopUpdateResponse, error) {
	r, err := s.runner(req.Target)
	if err != nil {
		return nil, err
	}
	job, err := r.Stop()
	if err != nil {
		return nil, jobErr(err)
	}
	s.track("update_stopped", map[string]any{
		"target":      targetName(req.Target),
		"bytes_done":  job.BytesDone,
		"bytes_total": job.BytesTotal,
	})
	return &pb.StopUpdateResponse{Job: job}, nil
}

func (s *Server) WatchUpdate(req *pb.WatchUpdateRequest, stream grpc.ServerStreamingServer[pb.WatchUpdateResponse]) error {
	r, err := s.runner(req.Target)
	if err != nil {
		return err
	}
	ch, stop := r.Watch()
	defer stop()
	for {
		job := r.Snapshot()
		if err := stream.Send(&pb.WatchUpdateResponse{Job: job}); err != nil {
			return err
		}
		if update.Finished(job.State) || job.State == pb.UpdateState_UPDATE_STATE_UNSPECIFIED {
			return nil
		}
		select {
		case <-stream.Context().Done():
			return nil
		case <-s.stopped:
			return nil
		case <-ch:
		}
	}
}

// maintainUpdates checks for new releases daily, and keeps geo data present and fresh.
func (s *Server) maintainUpdates(ctx context.Context) {
	select { // let Xray come up first, so a filtered network can use the proxy
	case <-ctx.Done():
		return
	case <-time.After(time.Minute):
	}
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	var lastAttempt time.Time
	for {
		stale := true
		s.mu.Lock()
		if s.upd != nil && s.upd.release != nil {
			stale = time.Since(time.UnixMilli(s.upd.release.CheckedAtMs)) >= checkEvery
		}
		s.mu.Unlock()
		if stale && (lastAttempt.IsZero() || time.Since(lastAttempt) >= checkEvery) {
			lastAttempt = time.Now()
			cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			if info, err := s.checkRelease(cctx, true); err != nil {
				s.Log("warn", "update check: "+err.Error())
			} else if info.Available {
				s.track("update_checked", map[string]any{
					"trigger":         "background",
					"available":       true,
					"current_version": info.CurrentVersion,
					"latest_version":  info.LatestVersion,
				})
			}
			cancel()
		}
		if age, ok := s.geoAge(); (!ok || age > geoMaxAge) && !s.upd.geo.Active() {
			if _, err := s.upd.geo.Start(s.geoTask(false, "background", false)); err == nil {
				s.Log("info", "refreshing geo data")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// geoAge is how long ago geosite.dat was written; ok is false when it's missing.
func (s *Server) geoAge() (time.Duration, bool) {
	fi, err := os.Stat(filepath.Join(s.xray.AssetDir(), "geosite.dat"))
	if err != nil {
		return 0, false
	}
	return time.Since(fi.ModTime()), true
}

// serviceBinary is the installed service's copy of masir, if this daemon is privileged
// enough to replace it and it's not the binary already being replaced.
func (s *Server) serviceBinary() string {
	if !s.id.Privileged || !service.Installed() {
		return ""
	}
	p := service.BinaryPath()
	if _, err := os.Stat(p); err != nil || p == executable() {
		return ""
	}
	return p
}

// executable is the running binary, resolved through symlinks so the file itself is
// replaced, not a link to it.
func executable() string {
	p, err := os.Executable()
	if err != nil {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

func jobErr(err error) error {
	switch {
	case errors.Is(err, update.ErrBusy), errors.Is(err, update.ErrNotRunning),
		errors.Is(err, update.ErrNotPaused), errors.Is(err, update.ErrCritical):
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	return status.Error(codes.Internal, err.Error())
}

func targetName(t pb.UpdateTarget) string {
	if t == pb.UpdateTarget_UPDATE_TARGET_GEO {
		return "geo data"
	}
	return "masir"
}
