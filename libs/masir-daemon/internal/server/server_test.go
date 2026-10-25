package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/narixius/masir-go/libs/masir-core/paths"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/identity"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/store"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/subs"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/xray"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// newTestServer is a booted server in a temp dir, proxy on a free localhost port.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	xc := xray.New("")
	t.Cleanup(func() { _ = xc.Close() })
	s := New(xc, identity.Identity{UID: os.Getuid(), GID: os.Getgid(), Name: "tester"})
	s.dir = t.TempDir()
	s.state.Settings.AllowLAN = false
	s.state.Settings.ProxyPort = freePort(t)
	m, err := subs.NewManager(filepath.Join(s.dir, "subscriptions"), filepath.Join(s.dir, "active.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	m.OnChange = s.subscriptionsChanged
	s.subs = m
	return s
}

func trojan(host string) string {
	return fmt.Sprintf(`{"protocol":"trojan","settings":{"servers":[{"address":%q,"port":443,"password":"x"}]},"streamSettings":{"security":"tls"}}`, host)
}

// addSub serves names as one subscription and adds it; returns its id and config ids by name.
func addSub(t *testing.T, s *Server, names ...string) (string, map[string]string) {
	t.Helper()
	var outs []string
	for _, n := range names {
		o := strings.Replace(trojan(strings.ToLower(n)+".example.com"), `{"protocol"`, fmt.Sprintf(`{"tag":%q,"protocol"`, n), 1)
		outs = append(outs, o)
	}
	body := "[" + strings.Join(outs, ",") + "]"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }))
	t.Cleanup(srv.Close)

	ctx := context.Background()
	added, err := s.AddSubscription(ctx, &pb.AddSubscriptionRequest{Url: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSubscription(ctx, &pb.GetSubscriptionRequest{Id: added.Subscription.Id})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, c := range got.Configs {
		ids[c.Name] = c.Id
	}
	return added.Subscription.Id, ids
}

func route(t *testing.T, s *Server, domain string) string {
	t.Helper()
	res, err := s.TestRouting(context.Background(), &pb.TestRoutingRequest{Domain: domain, Port: 443})
	if err != nil {
		t.Fatalf("route %s: %v", domain, err)
	}
	return res.OutboundTag
}

func listening(port uint32) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func on(t *testing.T, s *Server) {
	t.Helper()
	if _, err := s.SetPower(context.Background(), &pb.SetPowerRequest{On: true}); err != nil {
		t.Fatal(err)
	}
}

func TestPowerAndAlwaysOnProxy(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t)
	port := uint32(s.state.Settings.ProxyPort)

	on(t, s)
	if !s.xray.Running() || !listening(port) {
		t.Fatal("power on should bring up xray and the proxy")
	}
	if _, err := os.Stat(filepath.Join(s.dir, "xray.json")); err != nil {
		t.Fatalf("xray.json output: %v", err)
	}

	if _, err := s.SetPower(ctx, &pb.SetPowerRequest{On: false}); err != nil {
		t.Fatal(err)
	}
	if s.xray.Running() || listening(port) {
		t.Fatal("power off without always-on proxy should stop xray")
	}

	yes := true
	if _, err := s.UpdateSettings(ctx, &pb.UpdateSettingsRequest{AlwaysOnProxy: &yes}); err != nil {
		t.Fatal(err)
	}
	if !s.xray.Running() || !listening(port) {
		t.Fatal("always-on proxy should run xray with power off")
	}

	saved, err := store.Load(s.dir)
	if err != nil || saved.Settings.Power || !saved.Settings.AlwaysOnProxy {
		t.Fatalf("saved settings = %+v, %v", saved.Settings, err)
	}

	bad := uint32(0)
	if _, err := s.UpdateSettings(ctx, &pb.UpdateSettingsRequest{ProxyPort: &bad}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("port 0: %v", err)
	}

	level := "info"
	if _, err := s.UpdateSettings(ctx, &pb.UpdateSettingsRequest{XrayLogLevel: &level}); err != nil {
		t.Fatal(err)
	}
	if s.state.Settings.XrayLogLevel != "info" {
		t.Fatalf("xray log level = %q", s.state.Settings.XrayLogLevel)
	}
	loud := "loud"
	if _, err := s.UpdateSettings(ctx, &pb.UpdateSettingsRequest{XrayLogLevel: &loud}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("bad log level: %v", err)
	}
}

func TestRulesTargetCustomOutbounds(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t)
	on(t, s)

	if _, err := s.AddOutbound(ctx, &pb.AddOutboundRequest{Tag: "alt", OutboundJson: `{"protocol":"freedom"}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddRouting(ctx, &pb.AddRoutingRequest{RuleTag: "r", RuleJson: `{"domain":["full:a.test"]}`, Target: "outbound:alt"}); err != nil {
		t.Fatal(err)
	}
	if got := route(t, s, "a.test"); got != "alt" {
		t.Fatalf("a.test -> %s, want alt", got)
	}
	if got := route(t, s, "b.test"); got != "direct" {
		t.Fatalf("unruled traffic -> %s, want the direct fallback", got)
	}

	rules, _ := s.ListRoutingRules(ctx, &pb.ListRoutingRulesRequest{})
	if len(rules.Rules) != 1 || rules.Rules[0].Target != "outbound:alt" || rules.Rules[0].ResolvedOutboundTag != "alt" {
		t.Fatalf("rules = %v", rules.Rules)
	}

	if _, err := s.RemoveOutbound(ctx, &pb.RemoveOutboundRequest{Tag: "alt"}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("removing a rule's target: %v", err)
	}
	for name, req := range map[string]*pb.AddRoutingRequest{
		"bad target":  {RuleTag: "x", RuleJson: `{"domain":["full:x.test"]}`, Target: "elsewhere"},
		"outboundTag": {RuleTag: "x", RuleJson: `{"domain":["full:x.test"],"outboundTag":"direct"}`, Target: "direct"},
	} {
		if _, err := s.AddRouting(ctx, req); status.Code(err) != codes.InvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}

	block := "block"
	if _, err := s.AlterRouting(ctx, &pb.AlterRoutingRequest{RuleTag: "r", Target: &block}); err != nil {
		t.Fatal(err)
	}
	if got := route(t, s, "a.test"); got != "block" {
		t.Fatalf("after alter a.test -> %s", got)
	}
}

func TestFallbackNeedsSelection(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t)
	on(t, s)

	if _, err := s.SetFallback(ctx, &pb.SetFallbackRequest{Fallback: pb.Fallback_FALLBACK_PROXY}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("proxy fallback with nothing selected: %v", err)
	}
	sub, ids := addSub(t, s, "Main")
	if _, err := s.SelectConfig(ctx, &pb.SelectConfigRequest{SubscriptionId: sub, ConfigId: ids["Main"]}); err != nil {
		t.Fatal(err)
	}
	if got := route(t, s, "anything.test"); got != "direct" {
		t.Fatalf("selecting alone shouldn't change fallback; got %s", got)
	}
	if _, err := s.SetFallback(ctx, &pb.SetFallbackRequest{Fallback: pb.Fallback_FALLBACK_PROXY}); err != nil {
		t.Fatal(err)
	}
	if got := route(t, s, "anything.test"); got != "proxy" {
		t.Fatalf("proxy fallback -> %s", got)
	}
}

func TestPinnedConfigsAcrossSubscriptions(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t)
	on(t, s)

	subA, idsA := addSub(t, s, "Main")
	subB, idsB := addSub(t, s, "Netflix")
	if _, err := s.SelectConfig(ctx, &pb.SelectConfigRequest{SubscriptionId: subA, ConfigId: idsA["Main"]}); err != nil {
		t.Fatal(err)
	}
	_, _ = s.SetFallback(ctx, &pb.SetFallbackRequest{Fallback: pb.Fallback_FALLBACK_PROXY})

	target := "config:" + subB + "/" + idsB["Netflix"]
	if _, err := s.AddRouting(ctx, &pb.AddRoutingRequest{RuleTag: "netflix", RuleJson: `{"domain":["full:netflix.test"]}`, Target: target}); err != nil {
		t.Fatal(err)
	}
	pinnedTag := "sub-" + subB + "-" + idsB["Netflix"]
	if got := route(t, s, "netflix.test"); got != pinnedTag {
		t.Fatalf("pinned rule -> %s, want %s", got, pinnedTag)
	}
	if got := route(t, s, "other.test"); got != "proxy" {
		t.Fatalf("unruled -> %s, want proxy", got)
	}

	if _, err := s.AddRouting(ctx, &pb.AddRoutingRequest{RuleTag: "x", RuleJson: `{"domain":["full:x.test"]}`, Target: "config:" + subB + "/nope"}); status.Code(err) != codes.NotFound {
		t.Fatalf("pinning a missing config: %v", err)
	}

	// Removing the pinned config's subscription sends that rule to proxy.
	if _, err := s.RemoveSubscription(ctx, &pb.RemoveSubscriptionRequest{Id: subB}); err != nil {
		t.Fatal(err)
	}
	if got := route(t, s, "netflix.test"); got != "proxy" {
		t.Fatalf("after removing its subscription, pinned rule -> %s, want proxy", got)
	}
	rules, _ := s.ListRoutingRules(ctx, &pb.ListRoutingRulesRequest{})
	if r := rules.Rules[0]; !r.FellBack || r.ResolvedOutboundTag != "proxy" || r.Target != target {
		t.Fatalf("rule = %v", r)
	}

	// Removing the selection's subscription keeps the proxy running from its copy.
	if _, err := s.RemoveSubscription(ctx, &pb.RemoveSubscriptionRequest{Id: subA}); err != nil {
		t.Fatal(err)
	}
	if got := route(t, s, "other.test"); got != "proxy" {
		t.Fatalf("selection lost with its subscription; unruled -> %s", got)
	}
	active, _ := s.GetActiveConfig(ctx, &pb.GetActiveConfigRequest{})
	if active.ConfigId != idsA["Main"] || active.Config != nil {
		t.Fatalf("active = %+v; want the id kept and config unset (stale)", active)
	}
}

func TestTunNeedsRoot(t *testing.T) {
	s := newTestServer(t)
	_, err := s.SetTunEnabled(context.Background(), &pb.SetTunEnabledRequest{Enabled: true})
	st := status.Convert(err)
	if st.Code() != codes.FailedPrecondition {
		t.Fatalf("got %v, want FailedPrecondition", err)
	}
	var reason string
	for _, d := range st.Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.Domain == "masir" {
			reason = info.Reason
		}
	}
	if reason != "ROOT_REQUIRED" {
		t.Fatalf("details = %v, want ErrorInfo ROOT_REQUIRED", st.Details())
	}
	if s.state.Settings.Tun {
		t.Fatal("tun must not be saved without root")
	}
	if _, err := s.SetTunEnabled(context.Background(), &pb.SetTunEnabledRequest{Enabled: false}); err != nil {
		t.Fatalf("turning tun off needs no root: %v", err)
	}
}

func TestBootRestoresSavedState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData")) // Windows reads its config dir from here
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	dir, err := paths.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	st := store.State{Settings: store.DefaultSettings()}
	st.Settings.Power = true
	st.Settings.AllowLAN = false
	st.Settings.ProxyPort = freePort(t)
	if err := store.Save(dir, st); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	xc := xray.New("")
	defer xc.Close()
	s := New(xc, identity.Identity{UID: os.Getuid(), GID: os.Getgid(), Name: "tester"})
	if err := s.Boot(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if !xc.Running() || !listening(uint32(st.Settings.ProxyPort)) {
		t.Fatal("boot should bring up xray from the saved power setting")
	}
	if _, err := os.Stat(filepath.Join(dir, "xray.json")); err != nil {
		t.Fatalf("xray.json output: %v", err)
	}
	if _, err := s.ListSubscriptions(ctx, &pb.ListSubscriptionsRequest{}); err != nil {
		t.Fatalf("subscriptions after boot: %v", err)
	}
}

func TestFailedChangeLeavesStateAlone(t *testing.T) {
	ctx := context.Background()
	s := newTestServer(t)
	on(t, s)

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	in := fmt.Sprintf(`{"protocol":"socks","listen":"127.0.0.1","port":%d}`, busy.Addr().(*net.TCPAddr).Port)
	if _, err := s.AddInbound(ctx, &pb.AddInboundRequest{Tag: "clash", InboundJson: in}); err == nil {
		t.Fatal("expected a busy port to fail")
	}
	if len(s.state.Inbounds) != 0 {
		t.Fatal("failed change reached memory")
	}
	saved, _ := store.Load(s.dir)
	if len(saved.Inbounds) != 0 {
		t.Fatal("failed change reached disk")
	}
	if !s.xray.Running() {
		t.Fatal("xray should still run the previous config")
	}
}

func TestIdleBootWaitsForPower(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData")) // Windows reads its config dir from here
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	dir, err := paths.ConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	st := store.State{Settings: store.DefaultSettings()}
	st.Settings.Power = true // left on by the last daemon
	st.Settings.AlwaysOnProxy = true
	st.Settings.AllowLAN = false
	st.Settings.ProxyPort = freePort(t)
	if err := store.Save(dir, st); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	xc := xray.New("")
	defer xc.Close()
	s := New(xc, identity.Identity{UID: os.Getuid(), GID: os.Getgid(), Name: "tester"})
	s.StartIdle()
	if err := s.Boot(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if xc.Running() || listening(uint32(st.Settings.ProxyPort)) {
		t.Fatal("an idle daemon must not start xray, even with power and always-on proxy saved on")
	}
	if saved, _ := store.Load(dir); saved.Settings.Power {
		t.Error("an idle start saves masir as off, so status doesn't claim it's connected")
	}

	// Other work doesn't wake it: rules change, xray stays down.
	if _, err := s.AddRouting(ctx, &pb.AddRoutingRequest{RuleTag: "x", RuleJson: `{"domain":["full:x.com"]}`, Target: "direct"}); err != nil {
		t.Fatal(err)
	}
	if xc.Running() {
		t.Fatal("managing rules must not start xray on an idle daemon")
	}

	if _, err := s.SetPower(ctx, &pb.SetPowerRequest{On: true}); err != nil {
		t.Fatal(err)
	}
	if !xc.Running() || !listening(uint32(st.Settings.ProxyPort)) || s.Idle() {
		t.Fatal("turning masir on ends idle and starts xray")
	}
}

func TestIdleEndsWhenAlwaysOnProxyIsSwitchedOn(t *testing.T) {
	s := newTestServer(t)
	s.StartIdle()
	port := uint32(freePort(t))
	if _, err := s.UpdateSettings(context.Background(), &pb.UpdateSettingsRequest{ProxyPort: &port}); err != nil {
		t.Fatal(err)
	}
	if s.xray.Running() {
		t.Fatal("still idle after an unrelated setting")
	}
	on := true
	if _, err := s.UpdateSettings(context.Background(), &pb.UpdateSettingsRequest{AlwaysOnProxy: &on}); err != nil {
		t.Fatal(err)
	}
	if !s.xray.Running() || !listening(port) || s.Idle() {
		t.Fatal("switching always-on proxy on brings the proxy up")
	}
}
