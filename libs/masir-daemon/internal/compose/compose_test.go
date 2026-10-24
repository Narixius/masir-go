package compose

import (
	"context"
	"encoding/json"
	"net"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/store"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/subs"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/xray"
)

const (
	activeOut = `{"protocol":"trojan","settings":{"servers":[{"address":"active.example.com","port":443,"password":"x"}]},"streamSettings":{"security":"tls"}}`
	pinnedOut = `{"protocol":"trojan","settings":{"servers":[{"address":"pinned.example.com","port":443,"password":"y"}]},"streamSettings":{"security":"tls"}}`
)

type config struct {
	DNS struct {
		Servers []map[string]any `json:"servers"`
	} `json:"dns"`
	Inbounds  []map[string]any `json:"inbounds"`
	Outbounds []map[string]any `json:"outbounds"`
	Routing   struct {
		DomainStrategy string           `json:"domainStrategy"`
		Rules          []map[string]any `json:"rules"`
	} `json:"routing"`
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func testState(t *testing.T) store.State {
	t.Helper()
	st := store.State{Settings: store.DefaultSettings()}
	st.Settings.AllowLAN = false
	st.Settings.ProxyPort = freePort(t)
	return st
}

func build(t *testing.T, in Input) (Output, config) {
	t.Helper()
	out, err := Build(in)
	if err != nil {
		t.Fatal(err)
	}
	var c config
	if err := json.Unmarshal(out.Config, &c); err != nil {
		t.Fatal(err)
	}
	return out, c
}

func tags(items []map[string]any, key string) []string {
	var out []string
	for _, it := range items {
		s, _ := it[key].(string)
		out = append(out, s)
	}
	return out
}

func ruleByTag(c config, tag string) map[string]any {
	for _, r := range c.Routing.Rules {
		if r["ruleTag"] == tag {
			return r
		}
	}
	return nil
}

func TestBuildFollowsMasirTS(t *testing.T) {
	st := testState(t)
	st.Settings.Fallback = store.FallbackProxy
	if err := st.AddRule("ir-direct", []byte(`{"ip":["1.2.3.0/24"]}`), store.Target{Kind: store.TargetDirect}, false); err != nil {
		t.Fatal(err)
	}
	if err := st.AddRule("pin", []byte(`{"domain":["full:pinned.test"]}`),
		store.Target{Kind: store.TargetConfig, SubscriptionID: "s1", ConfigID: "c1", Name: "Pinned"}, false); err != nil {
		t.Fatal(err)
	}
	resolve := func(tg store.Target) (subs.Config, bool) {
		return subs.Config{ID: "c1", Name: "Pinned", Outbound: json.RawMessage(pinnedOut)}, tg.ConfigID == "c1"
	}

	out, c := build(t, Input{State: st, Proxy: true, Active: json.RawMessage(activeOut), Resolve: resolve})

	if got := tags(c.Outbounds, "tag"); !slices.Equal(got, []string{"direct", "block", "proxy", "sub-s1-c1", "dns-out", "loopback-dns"}) {
		t.Errorf("outbounds = %v", got)
	}
	want := []string{"masir-anti-loop", "masir-dns-bootstrap", "masir-dns-loopback", "masir-dns-hijack", "masir-dns-upstream",
		"ir-direct", "pin", "masir-lan-bypass", "masir-default-route"}
	if got := tags(c.Routing.Rules, "ruleTag"); !slices.Equal(got, want) {
		t.Errorf("rules = %v\nwant    %v", got, want)
	}
	if r := ruleByTag(c, "masir-default-route"); r["outboundTag"] != "proxy" {
		t.Errorf("fallback rule = %v", r)
	}
	if r := ruleByTag(c, "pin"); r["outboundTag"] != "sub-s1-c1" {
		t.Errorf("pinned rule = %v", r)
	}
	if c.Routing.DomainStrategy != "IPIfNonMatch" {
		t.Errorf("an ip rule should switch domainStrategy, got %q", c.Routing.DomainStrategy)
	}

	// DoH first; bootstrap servers resolve the proxy servers' names, direct.
	if c.DNS.Servers[0]["address"] != st.Settings.DoHDNS {
		t.Errorf("first dns server = %v", c.DNS.Servers[0])
	}
	boot := c.DNS.Servers[1]
	if boot["tag"] != "dns-bootstrap" || boot["address"] != "1.1.1.1" {
		t.Errorf("bootstrap = %v", boot)
	}
	domains, _ := json.Marshal(boot["domains"])
	for _, d := range []string{"full:active.example.com", "full:pinned.example.com"} {
		if !strings.Contains(string(domains), d) {
			t.Errorf("bootstrap domains %s miss %s", domains, d)
		}
	}

	mixed := c.Inbounds[0]
	if mixed["tag"] != ProxyInboundTag || mixed["protocol"] != "mixed" || mixed["listen"] != "127.0.0.1" || mixed["sniffing"] == nil {
		t.Errorf("proxy inbound = %v", mixed)
	}
	if len(out.Rules) != 2 || out.Rules[1].FellBack {
		t.Errorf("resolved = %+v", out.Rules)
	}

	// It has to actually run.
	p, err := xray.ParseProfile(out.Config)
	if err != nil {
		t.Fatal(err)
	}
	core := xray.New("")
	defer core.Close()
	if err := core.Apply(context.Background(), p); err != nil {
		t.Fatalf("xray rejected the composed config: %v", err)
	}
	for domain, want := range map[string]string{"pinned.test": "sub-s1-c1", "anything.test": "proxy"} {
		res, err := core.TestRoute(context.Background(), xray.RouteQuery{Domain: domain, Port: 443})
		if err != nil || res.OutboundTag != want {
			t.Errorf("route %s = %+v, %v; want %s", domain, res, err, want)
		}
	}
	if res, _ := core.TestRoute(context.Background(), xray.RouteQuery{IP: "192.168.1.10", Port: 80}); res.OutboundTag != "direct" {
		t.Errorf("LAN address routed to %s, want direct", res.OutboundTag)
	}
}

func TestBuildWithoutSelection(t *testing.T) {
	st := testState(t)
	st.Settings.Fallback = store.FallbackProxy // e.g. the selection's subscription is long gone
	_ = st.AddRule("p", []byte(`{"domain":["full:p.test"]}`), store.Target{Kind: store.TargetProxy}, false)
	_ = st.AddRule("gone", []byte(`{"domain":["full:g.test"]}`),
		store.Target{Kind: store.TargetConfig, SubscriptionID: "s", ConfigID: "missing"}, false)

	out, c := build(t, Input{State: st, Proxy: true, Resolve: func(store.Target) (subs.Config, bool) { return subs.Config{}, false }})

	for _, tag := range []string{"masir-default-route", "masir-dns-loopback", "masir-dns-upstream", "p", "gone"} {
		if r := ruleByTag(c, tag); r["outboundTag"] != "direct" {
			t.Errorf("%s = %v; with no proxy it must go direct", tag, r["outboundTag"])
		}
	}
	if slices.Contains(tags(c.Outbounds, "tag"), "proxy") {
		t.Error("no selection, yet a proxy outbound")
	}
	for _, rr := range out.Rules {
		if !rr.FellBack {
			t.Errorf("rule %s should report falling back", rr.Rule.Tag)
		}
	}
}

func TestBuildPinnedFallsBackToProxy(t *testing.T) {
	st := testState(t)
	_ = st.AddRule("gone", []byte(`{"domain":["full:g.test"]}`),
		store.Target{Kind: store.TargetConfig, SubscriptionID: "s", ConfigID: "missing"}, false)
	out, c := build(t, Input{State: st, Proxy: true, Active: json.RawMessage(activeOut),
		Resolve: func(store.Target) (subs.Config, bool) { return subs.Config{}, false }})
	if r := ruleByTag(c, "gone"); r["outboundTag"] != "proxy" || !out.Rules[0].FellBack {
		t.Fatalf("rule = %v, resolved = %+v", r, out.Rules)
	}
	if r := ruleByTag(c, "masir-default-route"); r["outboundTag"] != "direct" {
		t.Fatalf("fallback direct should stay direct, got %v", r)
	}
}

func TestBuildInbounds(t *testing.T) {
	st := testState(t)
	st.Settings.AllowLAN = true

	_, c := build(t, Input{State: st, Proxy: true, Tun: true, TunName: "utun42"})
	if c.Inbounds[0]["listen"] != "0.0.0.0" {
		t.Errorf("allow LAN should listen on all interfaces: %v", c.Inbounds[0])
	}
	if _, ok := c.Inbounds[0]["sniffing"]; ok {
		t.Error("no user rules, no sniffing on the proxy")
	}
	tun := c.Inbounds[1]
	settings, _ := tun["settings"].(map[string]any)
	if tun["tag"] != TunInboundTag || tun["protocol"] != "tun" || settings["name"] != "utun42" ||
		settings["autoOutboundsInterface"] != "auto" {
		t.Errorf("tun inbound = %v", tun)
	}
	if dns, _ := json.Marshal(settings["dns"]); string(dns) != `["1.1.1.1","8.8.8.8"]` {
		t.Errorf("tun dns should fall back to dns servers, got %s", dns)
	}

	_, off := build(t, Input{State: st})
	if len(off.Inbounds) != 0 {
		t.Errorf("proxy off should have no inbounds, got %v", off.Inbounds)
	}
}

func TestTunBlocksDNSLeaksOnWindows(t *testing.T) {
	st := testState(t)
	leak := func(os string) any {
		goos = os
		defer func() { goos = runtime.GOOS }()
		_, c := build(t, Input{State: st, Proxy: true, Tun: true})
		settings, _ := c.Inbounds[1]["settings"].(map[string]any)
		return settings["autoSystemWfpBlockLeak"]
	}
	if got, _ := json.Marshal(leak("windows")); string(got) != `["dns"]` {
		t.Errorf("windows: autoSystemWfpBlockLeak = %s, want [\"dns\"]", got)
	}
	if got := leak("darwin"); got != nil {
		t.Errorf("the filter is Windows-only, got %v on macOS", got)
	}
}
