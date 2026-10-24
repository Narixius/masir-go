package server

import (
	"context"
	"sync"
	"time"

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
	s.mu.Lock()
	defer s.mu.Unlock()

	st := &pb.Status{
		Version:          Version,
		XrayVersion:      s.xray.GetVersion(),
		UptimeSeconds:    int64(time.Since(s.startedAt).Seconds()),
		Privileged:       s.id.Privileged,
		ServiceInstalled: service.Installed(),
		ServedUid:        int64(s.id.UID),
		ServedUser:       s.id.Name,
		TunAvailable:     s.id.Privileged,
		XrayRunning:      s.xray.Running(),
		ProxyRunning:     s.running.proxy,
		ProxyAddress:     s.running.proxyAddr,
		TunRunning:       s.running.tun,
		LastError:        s.lastErr,
		Settings:         toPBSettings(s.state.Settings),
	}
	if !s.id.Privileged {
		st.TunUnavailableReason = "ROOT_REQUIRED"
	}
	for _, rr := range s.resolved {
		if rr.FellBack {
			st.FellBackRules++
		}
	}
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
	mu   sync.Mutex
	subs map[chan struct{}]struct{}
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
	g.mu.Lock()
	defer g.mu.Unlock()
	for ch := range g.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
