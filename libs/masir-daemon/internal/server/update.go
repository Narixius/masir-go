package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/narixius/masir-go/libs/masir-core/service"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/update"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/xray"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

const (
	// checkEvery is how often the daemon asks GitHub for a new masir release. It only
	// tells UIs; installing waits for StartUpdate.
	checkEvery = 24 * time.Hour
	// geoMaxAge is how old the geo files get before the daemon refreshes them itself.
	geoMaxAge = 7 * 24 * time.Hour
)

// updates holds the daemon's update jobs and what the last release check found.
type updates struct {
	masir, geo *update.Runner
	release    *pb.ReleaseInfo // guarded by Server.mu
}

func (s *Server) initUpdates(dir string) {
	s.upd = &updates{
		masir: update.NewRunner(pb.UpdateTarget_UPDATE_TARGET_MASIR, filepath.Join(dir, "updates", "masir")),
		geo:   update.NewRunner(pb.UpdateTarget_UPDATE_TARGET_GEO, filepath.Join(dir, "updates", "geo")),
	}
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

func (s *Server) masirTask(force bool, clientBinary string) update.Task {
	return update.Masir{
		Clients:       s.clients(),
		Current:       Version,
		Force:         force,
		Binary:        executable(),
		ServiceBinary: s.serviceBinary(),
		ClientBinary:  s.clientBinary(clientBinary),
		Privileged:    s.id.Privileged,
		WorkDir:       filepath.Join(s.dir, "updates", "masir"),
		Restart:       s.restartForUpdate,
	}.Run
}

// clientBinary accepts the CLI's path only when this daemon has the rights to replace
// it and it looks like a masir binary, so a client can't ask root to overwrite anything.
func (s *Server) clientBinary(path string) string {
	if !s.id.Privileged || path == "" || s.id.UID < 0 {
		return "" // Windows has no numeric owner to preserve; the CLI elevates itself
	}
	name := filepath.Base(path)
	if name != "masir" && name != "masir.exe" {
		return ""
	}
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	return path
}

func (s *Server) geoTask(force bool) update.Task {
	return update.Geo{
		Clients:  s.clients(),
		AssetDir: s.xray.AssetDir(),
		WorkDir:  filepath.Join(s.dir, "updates", "geo"),
		Force:    force,
		Reload:   xray.ReloadGeodata,
	}.Run
}

// restartForUpdate ends the daemon so that main starts the newly installed binary.
func (s *Server) restartForUpdate() {
	s.Log("info", "restarting into the new version")
	s.restart.Store(true)
	s.stopOnce.Do(func() { close(s.stopped) })
}

// RestartRequested reports whether the daemon stopped to start a new version.
func (s *Server) RestartRequested() bool { return s.restart.Load() }

func (s *Server) CheckUpdate(ctx context.Context, _ *pb.CheckUpdateRequest) (*pb.CheckUpdateResponse, error) {
	info, err := s.checkRelease(ctx)
	if err != nil {
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	return &pb.CheckUpdateResponse{Release: info}, nil
}

func (s *Server) checkRelease(ctx context.Context) (*pb.ReleaseInfo, error) {
	rel, err := update.Latest(ctx, s.clients(), update.ReleaseRepo)
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

func (s *Server) StartUpdate(_ context.Context, req *pb.StartUpdateRequest) (*pb.StartUpdateResponse, error) {
	r, err := s.runner(req.Target)
	if err != nil {
		return nil, err
	}
	task := s.geoTask(req.Force)
	if req.Target == pb.UpdateTarget_UPDATE_TARGET_MASIR {
		task = s.masirTask(req.Force, req.ClientBinary)
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
	var lastCheck time.Time
	for {
		if time.Since(lastCheck) >= checkEvery {
			cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			if _, err := s.checkRelease(cctx); err != nil {
				s.Log("warn", "update check: "+err.Error())
			} else {
				lastCheck = time.Now()
			}
			cancel()
		}
		if age, ok := s.geoAge(); (!ok || age > geoMaxAge) && !s.upd.geo.Active() {
			if _, err := s.upd.geo.Start(s.geoTask(false)); err == nil {
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
