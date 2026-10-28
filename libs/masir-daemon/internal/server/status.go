package server

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/narixius/masir-go/libs/masir-core/service"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

func (s *Server) GetStatus(ctx context.Context, _ *pb.GetStatusRequest) (*pb.GetStatusResponse, error) {
	return &pb.GetStatusResponse{Status: s.status()}, nil
}

func (s *Server) WatchStatus(_ *pb.WatchStatusRequest, stream pb.DaemonService_WatchStatusServer) error {
	ch, unsubscribe := s.changes.subscribe()
	defer unsubscribe()
	for {
		if err := stream.Send(&pb.WatchStatusResponse{Status: s.status()}); err != nil {
			return err
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

func (s *Server) status() *pb.Status {
	// Ask the OS and the filesystem before taking the lock: both can run a subprocess,
	// and the status stream should not hold up a change while it waits.
	svc := service.Status()
	inBundle := bundled()

	s.mu.Lock()
	defer s.mu.Unlock()

	settings := toPBSettings(s.state.Settings)
	if s.idle.Load() {
		// This process intentionally keeps Xray down. Report masir as off so a UI shows
		// the saved choice as "off" instead of a connection that never starts; the
		// saved setting itself is untouched and comes back with the next normal daemon.
		settings.Power = false
	}
	st := &pb.Status{
		Version:          Version,
		XrayVersion:      s.xray.GetVersion(),
		UptimeSeconds:    int64(time.Since(s.startedAt).Seconds()),
		StartedAtMs:      s.startedAt.UnixMilli(),
		Privileged:       s.id.Privileged,
		ServiceInstalled: svc.Installed,
		ServiceRunning:   svc.Running,
		ServiceEnabled:   svc.Enabled,
		Bundled:          inBundle,
		ServedUid:        int64(s.id.UID),
		ServedUser:       s.id.Name,
		TunAvailable:     s.id.Privileged,
		XrayRunning:      s.xray.Running(),
		ProxyRunning:     s.running.proxy,
		ProxyAddress:     s.running.proxyAddr,
		TunRunning:       s.running.tun,
		LastError:        s.lastErr,
		Settings:         settings,
		Revision:         s.changes.revision.Load(),
	}
	if !s.id.Privileged {
		st.TunUnavailableReason = "ROOT_REQUIRED"
	}
	if s.upd != nil && s.upd.release != nil {
		st.LatestRelease = proto.Clone(s.upd.release).(*pb.ReleaseInfo)
	}
	st.GeoDir = s.xray.AssetDir()
	if fi, err := os.Stat(filepath.Join(s.xray.AssetDir(), "geosite.dat")); err == nil {
		st.GeoUpdatedAt = fi.ModTime().UTC().Format(time.RFC3339)
	}
	for _, rr := range s.resolved {
		if rr.FellBack {
			st.FellBackRules++
		}
	}
	st.RuleProblems = toPBProblems(s.ruleProblems, s.suspended)
	if s.subs != nil {
		if a, err := s.subs.Active(); err == nil && a.ConfigID != "" {
			st.ActiveSubscriptionId, st.ActiveConfigId, st.ActiveConfigName = a.SubscriptionID, a.ConfigID, a.Name
			_, err := s.subs.Config(a.SubscriptionID, a.ConfigID)
			st.ActiveStale = err != nil
		}
	}
	return st
}

// signal wakes every subscriber after a change. Each holds at most one pending wake-up,
// so a slow watcher just sees the latest status, never a backlog.
type signal struct {
	mu       sync.Mutex
	subs     map[chan struct{}]struct{}
	revision atomic.Uint64 // counts notifications: Status.revision
}

func newSignal() *signal { return &signal{subs: map[chan struct{}]struct{}{}} }

func (g *signal) subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	g.mu.Lock()
	g.subs[ch] = struct{}{}
	g.mu.Unlock()
	return ch, func() {
		g.mu.Lock()
		delete(g.subs, ch)
		g.mu.Unlock()
	}
}

func (g *signal) notify() {
	g.revision.Add(1)
	g.mu.Lock()
	defer g.mu.Unlock()
	for ch := range g.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// bundled reports whether this daemon runs from inside the desktop app's bundle. That
// copy belongs to the app updater, so an update it starts must not replace it.
func bundled() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	slash := filepath.ToSlash(exe)
	if strings.Contains(slash, "/resources/bin/") || strings.Contains(slash, "/Resources/bin/") {
		return true
	}
	if runtime.GOOS != "darwin" {
		return false
	}
	for p := filepath.Clean(exe); ; p = filepath.Dir(p) {
		if strings.HasSuffix(p, ".app") {
			return true
		}
		if filepath.Dir(p) == p {
			return false
		}
	}
}
