package server

import (
	"context"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/store"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/subs"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

func (s *Server) ListSubscriptions(ctx context.Context, _ *pb.ListSubscriptionsRequest) (*pb.ListSubscriptionsResponse, error) {
	all, err := s.subs.List()
	if err != nil {
		return nil, toStatus(err)
	}
	out := make([]*pb.Subscription, len(all))
	for i := range all {
		out[i] = s.toPBSubscription(&all[i])
	}
	return &pb.ListSubscriptionsResponse{Subscriptions: out}, nil
}

func (s *Server) GetSubscription(ctx context.Context, req *pb.GetSubscriptionRequest) (*pb.GetSubscriptionResponse, error) {
	sub, err := s.subs.Get(req.Id)
	if err != nil {
		return nil, toStatus(err)
	}
	configs := make([]*pb.SubscriptionConfig, len(sub.Configs))
	for i := range sub.Configs {
		configs[i] = toPBConfig(&sub.Configs[i])
	}
	return &pb.GetSubscriptionResponse{Subscription: s.toPBSubscription(sub), Configs: configs}, nil
}

// validCustomInterval checks a subscription's own refresh interval; 0 means "use the
// setting".
func validCustomInterval(minutes uint32) error {
	if minutes == 0 {
		return nil
	}
	if err := store.ValidSubInterval(int(minutes)); err != nil {
		return toStatus(err)
	}
	return nil
}

func (s *Server) AddSubscription(ctx context.Context, req *pb.AddSubscriptionRequest) (*pb.AddSubscriptionResponse, error) {
	if err := validCustomInterval(req.CustomUpdateIntervalMinutes); err != nil {
		return nil, err
	}
	sub, err := s.subs.Add(ctx, req.Url, req.Name, req.CustomUpdateIntervalMinutes)
	if err != nil {
		return nil, toStatus(err)
	}
	s.track("subscription_added", map[string]any{
		"config_count": len(sub.Configs),
	})
	return &pb.AddSubscriptionResponse{Subscription: s.toPBSubscription(sub)}, nil
}

func (s *Server) AlterSubscription(ctx context.Context, req *pb.AlterSubscriptionRequest) (*pb.AlterSubscriptionResponse, error) {
	if req.CustomUpdateIntervalMinutes != nil {
		if err := validCustomInterval(*req.CustomUpdateIntervalMinutes); err != nil {
			return nil, err
		}
	}
	sub, err := s.subs.Alter(ctx, req.Id, req.Name, req.Url, req.CustomUpdateIntervalMinutes)
	if err != nil {
		return nil, toStatus(err)
	}
	s.Log("info", "subscription altered: "+sub.Name)
	s.refreshSubscriptionCounts()
	s.track("subscription_altered", map[string]any{
		"name_changed":     req.Name != nil,
		"url_changed":      req.Url != nil,
		"interval_changed": req.CustomUpdateIntervalMinutes != nil,
		"config_count":     len(sub.Configs),
	})
	return &pb.AlterSubscriptionResponse{Subscription: s.toPBSubscription(sub)}, nil
}

func (s *Server) RemoveSubscription(ctx context.Context, req *pb.RemoveSubscriptionRequest) (*pb.RemoveSubscriptionResponse, error) {
	if err := s.subs.Remove(ctx, req.Id); err != nil {
		return nil, toStatus(err)
	}
	s.Log("info", "subscription removed: "+req.Id)
	s.refreshSubscriptionCounts()
	s.track("subscription_removed", map[string]any{})
	return &pb.RemoveSubscriptionResponse{}, nil
}

func (s *Server) UpdateSubscription(ctx context.Context, req *pb.UpdateSubscriptionRequest) (*pb.UpdateSubscriptionResponse, error) {
	sub, err := s.subs.Update(ctx, req.Id)
	if err != nil {
		return nil, toStatus(err)
	}
	s.refreshSubscriptionCounts()
	return &pb.UpdateSubscriptionResponse{Subscription: s.toPBSubscription(sub)}, nil
}

func (s *Server) TestSubscriptionLatency(req *pb.TestSubscriptionLatencyRequest, stream pb.DaemonService_TestSubscriptionLatencyServer) error {
	ctx := stream.Context()
	err := s.subs.TestLatency(ctx, req.SubscriptionId, req.ConfigIds, req.ProbeUrl, func(id string, l subs.Latency) {
		// A send fails only once the client is gone, which also cancels ctx and stops the test.
		_ = stream.Send(&pb.TestSubscriptionLatencyResponse{ConfigId: id, Result: toPBLatency(&l)})
	})
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return toStatus(err)
	}
	return nil
}

func (srv *Server) toPBSubscription(s *subs.Subscription) *pb.Subscription {
	out := &pb.Subscription{
		Id:                          s.ID,
		Name:                        s.Name,
		Url:                         s.URL,
		CreatedAtMs:                 s.CreatedAt.UnixMilli(),
		UpdatedAtMs:                 unixMilli(s.UpdatedAt),
		CheckedAtMs:                 unixMilli(s.CheckedAt),
		LastError:                   s.LastError,
		UpdateIntervalMinutes:       uint32(srv.subs.Interval(s).Minutes()),
		CustomUpdateIntervalMinutes: s.CustomIntervalMinutes,
		ConfigCount:                 uint32(len(s.Configs)),
	}
	if u := s.Usage; u != nil {
		out.Usage = &pb.Usage{
			UploadBytes:   u.Upload,
			DownloadBytes: u.Download,
			TotalBytes:    u.Total,
			ExpireAtMs:    unixMilli(u.Expire),
		}
	}
	return out
}

func toPBConfig(c *subs.Config) *pb.SubscriptionConfig {
	return &pb.SubscriptionConfig{
		Id:           c.ID,
		Name:         c.Name,
		Protocol:     c.Protocol,
		Address:      c.Address,
		Port:         uint32(c.Port),
		Network:      c.Network,
		Security:     c.Security,
		OutboundJson: string(c.Outbound),
		Latency:      toPBLatency(c.Latency),
	}
}

func toPBLatency(l *subs.Latency) *pb.LatencyResult {
	if l == nil {
		return nil
	}
	return &pb.LatencyResult{LatencyMs: l.Ms, Error: l.Error, TestedAtMs: unixMilli(l.TestedAt)}
}
