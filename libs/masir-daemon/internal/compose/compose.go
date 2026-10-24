// Package compose builds the full Xray config from masird's state. DNS is captured
// inside Xray rather than by changing system settings, and fallback is the last
// routing rule.
package compose

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/store"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/subs"
)

const (
	ProxyInboundTag = "masir-proxy"
	TunInboundTag   = "tun-in"
	proxyTag        = "proxy"
)

// privateRanges stands in for geoip:private so the config builds without geoip.dat.
var privateRanges = []string{
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
	"172.16.0.0/12", "192.168.0.0/16", "224.0.0.0/4", "255.255.255.255/32",
	"::1/128", "fc00::/7", "fe80::/10", "ff00::/8",
}

type Input struct {
	State   store.State
	Proxy   bool            // the mixed proxy inbound
	Tun     bool            // the TUN inbound
	TunName string          // interface name; empty lets Xray pick
	Active  json.RawMessage // the selected config's outbound; nil when nothing is selected
	// Resolve finds a pinned config, following it by name when its ID changed upstream.
	Resolve func(store.Target) (subs.Config, bool)
}

// ResolvedRule says where a user rule's traffic actually goes.
type ResolvedRule struct {
	Rule        store.Rule
	OutboundTag string
	FellBack    bool // the target couldn't be used, so OutboundTag is the fallback
}

type Output struct {
	Config []byte
	Rules  []ResolvedRule
}

func Build(in Input) (Output, error) {
	st := in.State
	hasProxy := len(in.Active) > 0
	proxyOrDirect := "direct"
	if hasProxy {
		proxyOrDirect = proxyTag
	}

	// Outbounds: built-ins, the selection, pinned configs, custom ones, then DNS plumbing.
	outbounds := []any{
		obj{"tag": "direct", "protocol": "freedom"},
		obj{"tag": "block", "protocol": "blackhole"},
	}
	serverOutbounds := []json.RawMessage{}
	if hasProxy {
		o, err := withTag(in.Active, proxyTag)
		if err != nil {
			return Output{}, fmt.Errorf("selected config: %w", err)
		}
		outbounds = append(outbounds, o)
		serverOutbounds = append(serverOutbounds, in.Active)
	}

	var resolved []ResolvedRule
	pinned := map[string]bool{}
	for _, r := range st.Rules {
		rr := ResolvedRule{Rule: r}
		switch r.Target.Kind {
		case store.TargetDirect, store.TargetBlock:
			rr.OutboundTag = string(r.Target.Kind)
		case store.TargetProxy:
			rr.OutboundTag, rr.FellBack = proxyOrDirect, !hasProxy
		case store.TargetOutbound:
			rr.OutboundTag = r.Target.Tag
		case store.TargetConfig:
			c, ok := subs.Config{}, false
			if in.Resolve != nil {
				c, ok = in.Resolve(r.Target)
			}
			if !ok {
				rr.OutboundTag, rr.FellBack = proxyOrDirect, true
				break
			}
			tag := "sub-" + r.Target.SubscriptionID + "-" + c.ID
			rr.OutboundTag = tag
			if !pinned[tag] {
				pinned[tag] = true
				o, err := withTag(c.Outbound, tag)
				if err != nil {
					return Output{}, fmt.Errorf("pinned config %s: %w", c.Name, err)
				}
				outbounds = append(outbounds, o)
				serverOutbounds = append(serverOutbounds, c.Outbound)
			}
		default:
			return Output{}, fmt.Errorf("rule %q has unknown target %q", r.Tag, r.Target.Kind)
		}
		resolved = append(resolved, rr)
	}
	for _, it := range st.Outbounds {
		outbounds = append(outbounds, it.Config)
		serverOutbounds = append(serverOutbounds, it.Config)
	}
	outbounds = append(outbounds,
		// hijack A and AAAA so Xray's DNS answers them; other query types go out as they are
		obj{
			"tag": "dns-out", "protocol": "dns",
			"settings":       obj{"rules": []obj{{"action": "hijack", "qType": "1,28"}, {"action": "direct"}}},
			"streamSettings": obj{"sockopt": obj{"dialerProxy": "loopback-dns"}},
		},
		obj{
			"tag": "loopback-dns", "protocol": "loopback",
			"settings": obj{"inboundTag": "loopback-dns-in", "sniffing": sniffing(true)},
		},
	)

	// Inbounds.
	var inbounds []any
	if in.Proxy {
		listen := "127.0.0.1"
		if st.Settings.AllowLAN {
			listen = "0.0.0.0"
		}
		mixed := obj{
			"tag": ProxyInboundTag, "listen": listen, "port": st.Settings.ProxyPort, "protocol": "mixed",
			"settings": obj{"auth": "noauth", "udp": true},
		}
		// With user rules, sniff so domain and protocol rules also match apps that connect by IP.
		if len(st.Rules) > 0 {
			mixed["sniffing"] = sniffing(false)
		}
		inbounds = append(inbounds, mixed)
	}
	if in.Tun {
		tunDNS := st.Settings.TunDNS
		if len(tunDNS) == 0 {
			tunDNS = st.Settings.DNSServers
		}
		settings := obj{
			"autoOutboundsInterface": "auto",
			"autoSystemRoutingTable": []string{"0.0.0.0/0", "::/0"},
			"dns":                    tunDNS,
			"gateway":                []string{st.Settings.TunGateway},
			"mtu":                    1500,
		}
		if in.TunName != "" {
			settings["name"] = in.TunName
		}
		inbounds = append(inbounds, obj{"tag": TunInboundTag, "protocol": "tun", "settings": settings, "sniffing": sniffing(true)})
	}
	for _, it := range st.Inbounds {
		inbounds = append(inbounds, it.Config)
	}

	// DNS: everything through DoH over the proxy; proxy server names bootstrap over plain DNS, direct.
	doh := parseDoH(st.Settings.DoHDNS)
	domains := serverDomains(serverOutbounds)
	if !doh.isIP && !slices.Contains(domains, "full:"+doh.host) {
		domains = append(domains, "full:"+doh.host)
	}
	dnsServers := []any{obj{"address": st.Settings.DoHDNS, "queryStrategy": "UseIPv4"}}
	if len(domains) > 0 {
		for _, s := range st.Settings.DNSServers {
			dnsServers = append(dnsServers, obj{"address": s, "domains": domains, "finalQuery": true, "tag": "dns-bootstrap"})
		}
	}

	// Rules: plumbing first, so no user rule can break DNS or loop Xray; then user rules;
	// then the LAN bypass; then fallback.
	upstream := obj{"ruleTag": "masir-dns-upstream", "port": doh.port, "outboundTag": proxyOrDirect}
	if doh.isIP {
		upstream["ip"] = []string{doh.host}
	} else {
		upstream["domain"] = []string{"full:" + doh.host}
	}
	rules := []any{
		obj{"ruleTag": "masir-anti-loop", "process": []string{"self/", "xray/"}, "outboundTag": "direct"},
		obj{"ruleTag": "masir-dns-bootstrap", "inboundTag": []string{"dns-bootstrap"}, "outboundTag": "direct"},
		obj{"ruleTag": "masir-dns-loopback", "inboundTag": []string{"loopback-dns-in"}, "outboundTag": proxyOrDirect},
		obj{"ruleTag": "masir-dns-hijack", "port": 53, "network": "udp", "outboundTag": "dns-out"},
		upstream,
	}
	userIPRules := false
	for _, rr := range resolved {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(rr.Rule.Match, &m); err != nil {
			return Output{}, fmt.Errorf("rule %q: %w", rr.Rule.Tag, err)
		}
		if _, ok := m["ip"]; ok {
			userIPRules = true
		}
		m["ruleTag"], _ = json.Marshal(rr.Rule.Tag)
		m["outboundTag"], _ = json.Marshal(rr.OutboundTag)
		rules = append(rules, m)
	}
	fallback := "direct"
	if st.Settings.Fallback == store.FallbackProxy && hasProxy {
		fallback = proxyTag
	}
	rules = append(rules,
		obj{"ruleTag": "masir-lan-bypass", "ip": privateRanges, "outboundTag": "direct"},
		obj{"ruleTag": "masir-default-route", "network": "tcp,udp", "outboundTag": fallback},
	)

	// IP rules only see IP destinations under AsIs; resolve unmatched domains so they apply too.
	strategy := "AsIs"
	if userIPRules {
		strategy = "IPIfNonMatch"
	}

	cfg, err := json.Marshal(obj{
		"log":       obj{"loglevel": "warning"},
		"dns":       obj{"servers": dnsServers},
		"inbounds":  orEmpty(inbounds),
		"outbounds": outbounds,
		"routing":   obj{"domainStrategy": strategy, "rules": rules},
	})
	if err != nil {
		return Output{}, err
	}
	return Output{Config: cfg, Rules: resolved}, nil
}

// TunName returns a free utunN on macOS, the only names it allows; elsewhere Xray picks.
func TunName() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	used := map[string]bool{}
	if ifs, err := net.Interfaces(); err == nil {
		for _, i := range ifs {
			used[i.Name] = true
		}
	}
	for n := 10; ; n++ { // macOS itself takes the low numbers
		if name := "utun" + strconv.Itoa(n); !used[name] {
			return name
		}
	}
}

type obj = map[string]any

func sniffing(fakedns bool) obj {
	dest := []string{"http", "tls", "quic"}
	if fakedns {
		dest = append(dest, "fakedns")
	}
	// routeOnly: the sniffed name only feeds routing; the destination is never rewritten.
	return obj{"enabled": true, "destOverride": dest, "routeOnly": true}
}

func withTag(raw json.RawMessage, tag string) (json.RawMessage, error) {
	var o map[string]json.RawMessage
	if err := json.Unmarshal(raw, &o); err != nil {
		return nil, err
	}
	o["tag"], _ = json.Marshal(tag)
	return json.Marshal(o)
}

func orEmpty(v []any) []any {
	if v == nil {
		return []any{}
	}
	return v
}

type dohEndpoint struct {
	host string
	isIP bool
	port int
}

func parseDoH(raw string) dohEndpoint {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return dohEndpoint{host: "1.1.1.1", isIP: true, port: 443}
	}
	port := 443
	if p, err := strconv.Atoi(u.Port()); err == nil {
		port = p
	}
	return dohEndpoint{host: u.Hostname(), isIP: net.ParseIP(u.Hostname()) != nil, port: port}
}

// serverDomains lists the hostnames (not IPs) these outbounds dial, as full: matchers.
func serverDomains(outbounds []json.RawMessage) []string {
	var out []string
	seen := map[string]bool{}
	for _, raw := range outbounds {
		for _, h := range serverHosts(raw) {
			if net.ParseIP(h) != nil || seen[h] {
				continue
			}
			seen[h] = true
			out = append(out, "full:"+h)
		}
	}
	return out
}

// serverHosts covers the shapes Xray accepts: flat settings.address, vnext, servers,
// and wireguard peers[].endpoint (whose address is the tunnel IP, not a server).
func serverHosts(raw json.RawMessage) []string {
	var o struct {
		Protocol string `json:"protocol"`
		Settings struct {
			Address any `json:"address"`
			Vnext   []struct {
				Address string `json:"address"`
			} `json:"vnext"`
			Servers []struct {
				Address string `json:"address"`
			} `json:"servers"`
			Peers []struct {
				Endpoint string `json:"endpoint"`
			} `json:"peers"`
		} `json:"settings"`
	}
	if json.Unmarshal(raw, &o) != nil {
		return nil
	}
	var hosts []string
	if o.Protocol != "wireguard" {
		switch a := o.Settings.Address.(type) {
		case string:
			hosts = append(hosts, a)
		case []any:
			for _, v := range a {
				if s, ok := v.(string); ok {
					hosts = append(hosts, s)
				}
			}
		}
	}
	for _, v := range o.Settings.Vnext {
		hosts = append(hosts, v.Address)
	}
	for _, s := range o.Settings.Servers {
		hosts = append(hosts, s.Address)
	}
	for _, p := range o.Settings.Peers {
		if h, _, err := net.SplitHostPort(p.Endpoint); err == nil {
			hosts = append(hosts, h)
		}
	}
	return slices.DeleteFunc(hosts, func(h string) bool { return strings.TrimSpace(h) == "" })
}
