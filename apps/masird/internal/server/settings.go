package server

import (
	"context"
	"fmt"

	"github.com/narixius/masir-go/apps/masird/internal/store"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) GetSettings(ctx context.Context, _ *pb.GetSettingsRequest) (*pb.GetSettingsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return &pb.GetSettingsResponse{Settings: toPBSettings(s.state.Settings), Defaults: toPBSettings(store.DefaultSettings())}, nil
}

func (s *Server) UpdateSettings(ctx context.Context, req *pb.UpdateSettingsRequest) (*pb.UpdateSettingsResponse, error) {
	var out store.Settings
	err := s.change(ctx, "settings updated", func(st *store.State) error {
		set := &st.Settings
		if req.AlwaysOnProxy != nil {
			set.AlwaysOnProxy = *req.AlwaysOnProxy
		}
		if req.AllowLan != nil {
			set.AllowLAN = *req.AllowLan
		}
		if req.ProxyPort != nil {
			set.ProxyPort = int(*req.ProxyPort)
		}
		if req.DohDns != nil {
			set.DoHDNS = *req.DohDns
		}
		if req.DnsServers != nil {
			set.DNSServers = req.DnsServers.Values
		}
		if req.TunDns != nil {
			set.TunDNS = req.TunDns.Values
		}
		if req.TunGateway != nil {
			set.TunGateway = *req.TunGateway
		}
		out = *set
		return set.Validate()
	})
	if err != nil {
		return nil, err
	}
	return &pb.UpdateSettingsResponse{Settings: toPBSettings(out)}, nil
}

func (s *Server) SetPower(ctx context.Context, req *pb.SetPowerRequest) (*pb.SetPowerResponse, error) {
	msg := "masir turned off"
	if req.On {
		msg = "masir turned on"
	}
	err := s.change(ctx, msg, func(st *store.State) error {
		st.Settings.Power = req.On
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &pb.SetPowerResponse{}, nil
}

func (s *Server) SetFallback(ctx context.Context, req *pb.SetFallbackRequest) (*pb.SetFallbackResponse, error) {
	var f store.Fallback
	switch req.Fallback {
	case pb.Fallback_FALLBACK_DIRECT:
		f = store.FallbackDirect
	case pb.Fallback_FALLBACK_PROXY:
		f = store.FallbackProxy
		active, err := s.subs.Active()
		if err != nil {
			return nil, toStatus(err)
		}
		if len(active.Outbound) == 0 {
			return nil, status.Error(codes.FailedPrecondition, "select a config before using the proxy fallback")
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "fallback is required")
	}
	err := s.change(ctx, "fallback set to "+string(f), func(st *store.State) error {
		st.Settings.Fallback = f
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &pb.SetFallbackResponse{}, nil
}

// SetTunEnabled refuses to turn TUN on without root, and saves nothing then,
// so the UI can elevate the daemon and call again.
func (s *Server) SetTunEnabled(ctx context.Context, req *pb.SetTunEnabledRequest) (*pb.SetTunEnabledResponse, error) {
	if req.Enabled && !s.id.Privileged {
		return nil, rootRequired()
	}
	err := s.change(ctx, fmt.Sprintf("tun enabled: %v", req.Enabled), func(st *store.State) error {
		st.Settings.Tun = req.Enabled
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &pb.SetTunEnabledResponse{}, nil
}

func rootRequired() error {
	st := status.New(codes.FailedPrecondition, "tun mode needs the daemon to run as root")
	if d, err := st.WithDetails(&errdetails.ErrorInfo{Domain: "masir", Reason: "ROOT_REQUIRED"}); err == nil {
		st = d
	}
	return st.Err()
}

func toPBSettings(s store.Settings) *pb.Settings {
	f := pb.Fallback_FALLBACK_DIRECT
	if s.Fallback == store.FallbackProxy {
		f = pb.Fallback_FALLBACK_PROXY
	}
	return &pb.Settings{
		Power:         s.Power,
		Tun:           s.Tun,
		AlwaysOnProxy: s.AlwaysOnProxy,
		Fallback:      f,
		AllowLan:      s.AllowLAN,
		ProxyPort:     uint32(s.ProxyPort),
		DohDns:        s.DoHDNS,
		DnsServers:    s.DNSServers,
		TunDns:        s.TunDNS,
		TunGateway:    s.TunGateway,
	}
}
