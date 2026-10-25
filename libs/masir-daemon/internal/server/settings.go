package server

import (
	"context"
	"fmt"
	"time"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/store"
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
	var changed []string
	add := func(name string) { changed = append(changed, name) }
	if req.AlwaysOnProxy != nil {
		add("alwaysOnProxy")
	}
	if req.AllowLan != nil {
		add("allowLan")
	}
	if req.ProxyPort != nil {
		add("proxyPort")
	}
	if req.DohDns != nil {
		add("dohDns")
	}
	if req.DnsServers != nil {
		add("dnsServers")
	}
	if req.TunDns != nil {
		add("tunDns")
	}
	if req.TunGateway != nil {
		add("tunGateway")
	}
	if req.SubIntervalMinutes != nil {
		add("subIntervalMinutes")
	}
	if req.XrayLogLevel != nil {
		add("xrayLogLevel")
	}
	// Switching always-on proxy on means "proxy now", idle or not.
	wasIdle := req.AlwaysOnProxy != nil && *req.AlwaysOnProxy && s.idle.Swap(false)
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
		if req.SubIntervalMinutes != nil {
			set.SubIntervalMinutes = int(*req.SubIntervalMinutes)
		}
		if req.XrayLogLevel != nil {
			set.XrayLogLevel = store.NormalizeXrayLogLevel(*req.XrayLogLevel)
		}
		if req.Analytics != nil {
			set.Analytics = *req.Analytics
		}
		out = *set
		return set.Validate()
	})
	if err != nil {
		if wasIdle {
			s.idle.Store(true)
		}
		return nil, err
	}
	if len(changed) > 0 {
		s.track("settings_changed", map[string]any{"keys": changed})
	}
	if req.Analytics != nil && s.analytics != nil {
		if *req.Analytics {
			s.analytics.SetEnabled(true)
			s.track("analytics_enabled", map[string]any{"source": "settings"})
		} else {
			s.track("analytics_disabled", map[string]any{"source": "settings"})
			s.analytics.SetEnabled(false)
		}
	}
	s.subs.SetInterval(time.Duration(out.SubIntervalMinutes) * time.Minute)
	return &pb.UpdateSettingsResponse{Settings: toPBSettings(out)}, nil
}

func (s *Server) SetPower(ctx context.Context, req *pb.SetPowerRequest) (*pb.SetPowerResponse, error) {
	msg := "masir turned off"
	if req.On {
		msg = "masir turned on"
	}
	wasIdle := req.On && s.idle.Swap(false) // turning masir on ends idle
	err := s.change(ctx, msg, func(st *store.State) error {
		st.Settings.Power = req.On
		return nil
	})
	if err != nil {
		if wasIdle {
			s.idle.Store(true)
		}
		s.trackError("power_changed_failed", err, map[string]any{"on": req.On}, false)
		return nil, err
	}
	s.mu.Lock()
	tun := s.state.Settings.Tun
	s.mu.Unlock()
	s.trackWithActive("power_changed", map[string]any{
		"on":  req.On,
		"tun": tun,
	})
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
		s.trackError("routing_mode_changed_failed", err, map[string]any{"mode": string(f)}, false)
		return nil, err
	}
	s.track("routing_mode_changed", map[string]any{"mode": string(f)})
	return &pb.SetFallbackResponse{}, nil
}

// SetTunEnabled refuses to turn TUN on without root, and saves nothing then,
// so the UI can elevate the daemon and call again.
func (s *Server) SetTunEnabled(ctx context.Context, req *pb.SetTunEnabledRequest) (*pb.SetTunEnabledResponse, error) {
	if req.Enabled && !s.id.Privileged {
		err := rootRequired()
		s.trackError("tun_failed", err, map[string]any{"enabled": true}, false)
		return nil, err
	}
	err := s.change(ctx, fmt.Sprintf("tun enabled: %v", req.Enabled), func(st *store.State) error {
		st.Settings.Tun = req.Enabled
		return nil
	})
	if err != nil {
		s.trackError("tun_failed", err, map[string]any{"enabled": req.Enabled}, false)
		return nil, err
	}
	s.trackWithActive("tun_changed", map[string]any{"enabled": req.Enabled})
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

		SubIntervalMinutes: uint32(s.SubIntervalMinutes),
		XrayLogLevel:       s.XrayLogLevel,
		Analytics:          s.Analytics,
	}
}
