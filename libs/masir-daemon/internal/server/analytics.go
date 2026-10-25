package server

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/narixius/masir-go/libs/masir-core/service"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/analytics"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/store"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/subs"
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

var clientEvents = map[string]bool{
	"cli_command":          true,
	"self_update_finished": true,
	"cli_panic":            true,
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

// trackWithActive adds the selected proxy's country/protocol to an event. Call it
// without s.mu held; it reads the selection and then takes the lock.
func (s *Server) trackWithActive(event string, props map[string]any) {
	a := s.currentActive()
	if props == nil {
		props = map[string]any{}
	}
	addActiveProps(props, a)
	s.track(event, props)
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
		"stack_fingerprint": analytics.Fingerprint(string(stack)),
	}, true)
}

func (s *Server) commonPropsLocked(st store.State) map[string]any {
	set := st.Settings
	dohProvider := analytics.DoHProvider(set.DoHDNS)
	bootstrap := analytics.DNSProviders(set.DNSServers)
	role := "user"
	if s.id.Privileged {
		role = "privileged"
	}
	props := map[string]any{
		"app_version":              Version,
		"xray_version":             s.xray.GetVersion(),
		"xray_running":             s.xray.Running(),
		"arch":                     runtime.GOARCH,
		"daemon_role":              role,
		"privileged":               s.id.Privileged,
		"service_installed":        service.Installed(),
		"fallback":                 string(set.Fallback),
		"tun_enabled":              set.Tun,
		"allow_lan":                set.AllowLAN,
		"always_on_proxy":          set.AlwaysOnProxy,
		"xray_log_level":           set.XrayLogLevel,
		"proxy_port":               set.ProxyPort,
		"proxy_port_is_default":    set.ProxyPort == 7890,
		"doh_provider":             dohProvider,
		"doh_is_default":           dohProvider == "cloudflare" && strings.Contains(set.DoHDNS, "1.1.1.1"),
		"doh_custom":               dohProvider == "custom" || dohProvider == "unknown",
		"bootstrap_dns_providers":  bootstrap,
		"bootstrap_dns_count":      len(set.DNSServers),
		"bootstrap_dns_custom":     containsString(bootstrap, "custom"),
		"bootstrap_dns_is_default": analytics.IsDefaultDNS(set.DNSServers),
		"tun_dns_count":            len(set.TunDNS),
		"tun_dns_custom":           len(set.TunDNS) > 0 && !sameStrings(set.TunDNS, set.DNSServers),
		"gateway_is_default":       set.TunGateway == "10.10.20.1/24",
		"sub_interval_minutes":     set.SubIntervalMinutes,
		"rule_count":               len(st.Rules),
		"inbound_count":            len(st.Inbounds),
		"outbound_count":           len(st.Outbounds),
		"subscription_count":       s.subCount.Load(),
		"total_config_count":       s.totalConfigs.Load(),
	}
	for k, v := range analytics.TimezoneProps() {
		props[k] = v
	}
	return props
}

type activeInfo struct {
	Country  string
	Protocol string
	Network  string
	Security string
	Stale    bool
}

func activeInfoFromConfig(c subs.Config) activeInfo {
	return activeInfo{
		Country:  analytics.CountryFromName(c.Name),
		Protocol: c.Protocol,
		Network:  c.Network,
		Security: c.Security,
	}
}

func activeInfoFrom(a subs.Active) activeInfo {
	info := activeInfo{Country: analytics.CountryFromName(a.Name)}
	var o struct {
		Protocol       string `json:"protocol"`
		StreamSettings struct {
			Network  string `json:"network"`
			Security string `json:"security"`
		} `json:"streamSettings"`
	}
	if json.Unmarshal(a.Outbound, &o) == nil {
		info.Protocol, info.Network, info.Security = o.Protocol, o.StreamSettings.Network, o.StreamSettings.Security
	}
	return info
}

func (s *Server) currentActive() activeInfo {
	if s.subs == nil {
		return activeInfo{}
	}
	a, err := s.subs.Active()
	if err != nil || a.ConfigID == "" {
		return activeInfo{}
	}
	info := activeInfo{Country: analytics.CountryFromName(a.Name)}
	if c, err := s.subs.Config(a.SubscriptionID, a.ConfigID); err == nil {
		info.Protocol, info.Network, info.Security = c.Protocol, c.Network, c.Security
		if info.Country == "" {
			info.Country = analytics.CountryFromName(c.Name)
		}
	} else {
		info.Stale = true
	}
	return info
}

func addActiveProps(props map[string]any, a activeInfo) {
	if a.Country != "" {
		props["active_country"] = a.Country
	}
	if a.Protocol != "" {
		props["active_protocol"] = a.Protocol
	}
	if a.Network != "" {
		props["active_network"] = a.Network
	}
	if a.Security != "" {
		props["active_security"] = a.Security
	}
	if a.Stale {
		props["active_stale"] = true
	}
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
	if !clientEvents[req.Event] {
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
	// Clients may send product properties, never PostHog reserved ones.
	for k := range props {
		if strings.HasPrefix(k, "$") {
			delete(props, k)
		}
	}
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

type xrayLogTracker struct {
	mu     sync.Mutex
	seen   map[string]time.Time
	window time.Duration
	limit  int
	events []time.Time
}

func newXrayLogTracker() *xrayLogTracker {
	return &xrayLogTracker{seen: map[string]time.Time{}, window: 5 * time.Minute, limit: 20}
}

func (t *xrayLogTracker) allow(fingerprint string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if last, ok := t.seen[fingerprint]; ok && now.Sub(last) < t.window {
		return false
	}
	if len(t.seen) > 500 {
		for k, at := range t.seen {
			if now.Sub(at) >= t.window {
				delete(t.seen, k)
			}
		}
	}
	cutoff := now.Add(-time.Minute)
	kept := t.events[:0]
	for _, at := range t.events {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}
	t.events = kept
	if len(t.events) >= t.limit {
		return false
	}
	t.seen[fingerprint] = now
	t.events = append(t.events, now)
	return true
}

func (s *Server) trackXrayLog(level, msg string) {
	if s.analytics == nil {
		return
	}
	level = strings.ToLower(level)
	if level != "warn" && level != "warning" && level != "error" {
		return
	}
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "xray") && strings.Contains(lower, "started") {
		return
	}
	if strings.Contains(lower, "logger started") || strings.Contains(lower, "logger closing") {
		return
	}
	fp := analytics.Fingerprint(msg)
	if s.xrayLogs == nil || !s.xrayLogs.allow(fp, time.Now()) {
		return
	}
	// Do not take s.mu here: Xray can log while applyLocked holds it.
	s.analytics.TrackError("xray_warning", fmt.Errorf("%s", msg), map[string]any{
		"app_version":  Version,
		"xray_version": s.xray.GetVersion(),
		"severity":     level,
		"category":     xrayLogCategory(lower),
		"fingerprint":  fp,
	}, false)
}

func xrayLogCategory(msg string) string {
	switch {
	case strings.Contains(msg, "dns"):
		return "dns"
	case strings.Contains(msg, "tls") || strings.Contains(msg, "transport") || strings.Contains(msg, "dial"):
		return "transport"
	case strings.Contains(msg, "config") || strings.Contains(msg, "failed to load"):
		return "config"
	case strings.Contains(msg, "route") || strings.Contains(msg, "routing"):
		return "routing"
	}
	return "other"
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

func configProtocols(configs []subs.Config) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range configs {
		if c.Protocol != "" && !seen[c.Protocol] {
			seen[c.Protocol] = true
			out = append(out, c.Protocol)
		}
	}
	return out
}

func configCountries(configs []subs.Config) []string {
	names := make([]string, len(configs))
	for i, c := range configs {
		names[i] = c.Name
	}
	return analytics.CountriesFromNames(names)
}

// trackSubscriptionRefresh is the background refresher's event hook.
func (s *Server) trackSubscriptionRefresh(sub *subs.Subscription, err error, took time.Duration) {
	props := map[string]any{
		"trigger":     "background",
		"duration_ms": took.Milliseconds(),
	}
	if sub != nil {
		props["config_count"] = len(sub.Configs)
		props["protocols"] = configProtocols(sub.Configs)
	}
	if err != nil {
		s.trackError("subscription_refreshed", err, props, false)
		return
	}
	props["success"] = true
	s.track("subscription_refreshed", props)
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
