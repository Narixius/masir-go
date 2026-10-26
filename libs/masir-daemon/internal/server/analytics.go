package server

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	coreanalytics "github.com/narixius/masir-go/libs/masir-core/analytics"
	"github.com/narixius/masir-go/libs/masir-core/service"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/analytics"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/store"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

const (
	// rpcFailure methods are high-frequency reads/streams; tracking them would drown
	// the useful events.
	rpcMethodPrefix = "/masir.daemon.v1.DaemonService/"
)

var ignoredRPCs = map[string]bool{
	"GetStatus": true, "WatchStatus": true, "StreamLogs": true, "CaptureEvent": true,
	"TestLatency": true, "CancelLatencyTest": true, "TestSubscriptionLatency": true,
	"WatchUpdate": true,
}

// trackLocked records an event while s.mu is held. It only reads st and immutable
// server fields, and analytics never takes s.mu itself.
func (s *Server) trackLocked(st store.State, event string, props map[string]any) {
	if s.analytics == nil {
		return
	}
	if props == nil {
		props = map[string]any{}
	}
	for k, v := range s.commonPropsLocked(st) {
		if _, ok := props[k]; !ok {
			props[k] = v
		}
	}
	s.analytics.Track(event, props)
}

// track is the unlocked variant: it copies the state, then records.
func (s *Server) track(event string, props map[string]any) {
	s.mu.Lock()
	st := s.state
	s.mu.Unlock()
	s.trackLocked(st, event, props)
}

// trackAt records a client event with its original timestamp.
func (s *Server) trackAt(event string, props map[string]any, ts time.Time) {
	s.mu.Lock()
	st := s.state
	s.mu.Unlock()
	if s.analytics == nil {
		return
	}
	if props == nil {
		props = map[string]any{}
	}
	for k, v := range s.commonPropsLocked(st) {
		if _, ok := props[k]; !ok {
			props[k] = v
		}
	}
	s.analytics.TrackAt(event, props, ts)
}

func (s *Server) trackError(event string, err error, props map[string]any, unexpected bool) {
	if err == nil || s.analytics == nil {
		return
	}
	s.mu.Lock()
	st := s.state
	s.mu.Unlock()
	base := s.commonPropsLocked(st)
	for k, v := range props {
		base[k] = v
	}
	s.analytics.TrackError(event, err, base, unexpected)
}

func (s *Server) trackErrorLocked(st store.State, event string, err error, props map[string]any, unexpected bool) {
	if err == nil || s.analytics == nil {
		return
	}
	base := s.commonPropsLocked(st)
	for k, v := range props {
		base[k] = v
	}
	s.analytics.TrackError(event, err, base, unexpected)
}

// TrackPanic records a recovered panic with a scrubbed message and stack fingerprint.
// It avoids s.mu: a panic can happen while a handler holds it.
func (s *Server) TrackPanic(value any, stack []byte) {
	if s.analytics == nil {
		return
	}
	err := fmt.Errorf("panic: %v", value)
	s.analytics.TrackError("daemon_panic", err, map[string]any{
		"app_version":       Version,
		"stack_fingerprint": coreanalytics.Fingerprint(string(stack)),
	}, true)
}

func (s *Server) commonPropsLocked(st store.State) map[string]any {
	set := st.Settings
	role := "user"
	if s.id.Privileged {
		role = "privileged"
	}
	props := map[string]any{
		"app_version":        Version,
		"xray_version":       s.xray.GetVersion(),
		"xray_running":       s.xray.Running(),
		"arch":               runtime.GOARCH,
		"daemon_role":        role,
		"privileged":         s.id.Privileged,
		"service_installed":  service.Installed(),
		"tun_enabled":        set.Tun,
		"rule_count":         len(st.Rules),
		"inbound_count":      len(st.Inbounds),
		"outbound_count":     len(st.Outbounds),
		"subscription_count": s.subCount.Load(),
		"total_config_count": s.totalConfigs.Load(),
	}
	for k, v := range analytics.TimezoneProps() {
		props[k] = v
	}
	return props
}

func (s *Server) analyticsLoop(ctx context.Context) {
	if s.analytics == nil {
		return
	}
	s.analytics.DrainSpool(ctx)
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.analytics.DrainSpool(ctx)
		}
	}
}

// CloseAnalytics flushes queued events. Safe to call more than once.
func (s *Server) CloseAnalytics() {
	if s.analytics != nil {
		s.analytics.FlushAndClose()
	}
}

// TrackDaemonError records a daemon-level failure that isn't tied to one RPC. These
// are unexpected, so they include the scrubbed message.
func (s *Server) TrackDaemonError(where string, err error) {
	s.trackError("daemon_error", err, map[string]any{"where": where}, true)
}

// TrackDaemonStopped records the end of the process once.
func (s *Server) TrackDaemonStopped(reason string) {
	s.stopTracked.Do(func() {
		s.track("daemon_stopped", map[string]any{
			"reason":         reason,
			"uptime_seconds": int64(time.Since(s.startedAt).Seconds()),
		})
	})
}

// CaptureEvent accepts one CLI event. Invalid or oversized events are ignored rather
// than failing the CLI.
func (s *Server) CaptureEvent(_ context.Context, req *pb.CaptureEventRequest) (*pb.CaptureEventResponse, error) {
	if s.analytics == nil || !s.analytics.Enabled() {
		return &pb.CaptureEventResponse{}, nil
	}
	if !analytics.ClientEventAllowed(req.Event) {
		return nil, status.Error(codes.InvalidArgument, "event not allowed")
	}
	if len(req.PropertiesJson) > 16<<10 {
		return nil, status.Error(codes.InvalidArgument, "properties too large")
	}
	props := map[string]any{}
	if req.PropertiesJson != "" {
		if err := json.Unmarshal([]byte(req.PropertiesJson), &props); err != nil {
			return nil, status.Error(codes.InvalidArgument, "properties must be a JSON object")
		}
	}
	analytics.CleanClientProps(props)
	props["source"] = "cli"
	ts := time.Now()
	if req.TimestampMs > 0 {
		ts = time.UnixMilli(req.TimestampMs)
	}
	s.trackAt(req.Event, props, ts)
	return &pb.CaptureEventResponse{}, nil
}

// UnaryInterceptor reports failures of user-facing RPCs. Reads and streams are skipped.
func (s *Server) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		start := time.Now()
		resp, err := handler(ctx, req)
		if err == nil {
			return resp, nil
		}
		method := strings.TrimPrefix(info.FullMethod, rpcMethodPrefix)
		if ignoredRPCs[method] || status.Code(err) == codes.Canceled || status.Code(err) == codes.OK {
			return resp, err
		}
		s.trackError("rpc_failed", err, map[string]any{
			"method":      method,
			"duration_ms": time.Since(start).Milliseconds(),
		}, false)
		return resp, err
	}
}

func matcherKinds(raw string) []string {
	var m map[string]json.RawMessage
	if json.Unmarshal([]byte(raw), &m) != nil {
		return nil
	}
	known := []string{"process", "domain", "ip", "port", "network", "protocol", "source", "sourcePort"}
	var out []string
	for _, k := range known {
		if _, ok := m[k]; ok {
			out = append(out, k)
		}
	}
	return out
}

func targetKind(raw string) string {
	switch {
	case raw == "direct", raw == "block", raw == "proxy":
		return raw
	case strings.HasPrefix(raw, "config:"):
		return "config"
	case strings.HasPrefix(raw, "outbound:"):
		return "outbound"
	}
	return "unknown"
}

func (s *Server) refreshSubscriptionCounts() {
	if s.subs == nil {
		return
	}
	all, err := s.subs.List()
	if err != nil {
		return
	}
	configs := 0
	for i := range all {
		configs += len(all[i].Configs)
	}
	s.subCount.Store(int64(len(all)))
	s.totalConfigs.Store(int64(configs))
}
