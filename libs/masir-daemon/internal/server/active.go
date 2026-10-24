package server

import (
	"context"
	"time"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/subs"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// SelectConfig makes the config the "proxy" outbound, keeping a copy of it.
func (s *Server) SelectConfig(ctx context.Context, req *pb.SelectConfigRequest) (*pb.SelectConfigResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, err := s.subs.Config(req.SubscriptionId, req.ConfigId)
	if err != nil {
		return nil, toStatus(err)
	}
	prev, err := s.subs.Active()
	if err != nil {
		return nil, toStatus(err)
	}
	next := subs.Active{SubscriptionID: req.SubscriptionId, ConfigID: c.ID, Name: c.Name, Outbound: c.Outbound}
	if err := s.subs.SetActive(next); err != nil {
		return nil, toStatus(err)
	}
	if err := s.applyLocked(ctx, s.state); err != nil {
		if rerr := s.subs.SetActive(prev); rerr == nil {
			_ = s.applyLocked(context.WithoutCancel(ctx), s.state)
		}
		return nil, err
	}
	s.Log("info", "proxy set to "+c.Name)
	return &pb.SelectConfigResponse{}, nil
}

func (s *Server) GetActiveConfig(ctx context.Context, _ *pb.GetActiveConfigRequest) (*pb.GetActiveConfigResponse, error) {
	a, err := s.subs.Active()
	if err != nil {
		return nil, toStatus(err)
	}
	out := &pb.GetActiveConfigResponse{SubscriptionId: a.SubscriptionID, ConfigId: a.ConfigID}
	if a.ConfigID != "" {
		if c, err := s.subs.Config(a.SubscriptionID, a.ConfigID); err == nil {
			out.Config = toPBConfig(&c)
		}
	}
	return out, nil
}

func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
