package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"regexp"
	"slices"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// ruleMatch is a rule's match part: an xray field rule without the outbound.
type ruleMatch map[string]any

type matcherKind struct{ key, label, hint string }

// The order the wizard offers them in, most used first.
var matcherKinds = []matcherKind{
	{"process", "Apps", "an app and all its helper processes"},
	{"domain", "Websites", "domains, sites and services"},
	{"ip", "IP addresses", "addresses, ranges and countries"},
	{"port", "Ports", "destination ports"},
	{"network", "Network", "TCP or UDP"},
	{"protocol", "Protocol", "http, tls, quic, bittorrent"},
	{"source", "Source IP", "which devices, when sharing over LAN"},
	{"sourcePort", "Source port", "the connecting side's port"},
}

// listKeys hold arrays in xray's rule format; the rest are strings.
var listKeys = map[string]bool{"domain": true, "ip": true, "protocol": true, "process": true, "source": true}

// flagKeys maps matcher flags to rule keys.
var flagKeys = map[string]string{
	"domain": "domain", "ip": "ip", "port": "port", "network": "network", "protocol": "protocol",
	"process": "process", "app": "process", "source": "source", "source-port": "sourcePort",
}

func parseMatch(raw string) (ruleMatch, error) {
	var m ruleMatch
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fail("rule JSON: %v", err)
	}
	if m == nil {
		m = ruleMatch{}
	}
	return m, nil
}

func (m ruleMatch) values(key string) []string {
	switch v := m[key].(type) {
	case string:
		return splitList(v)
	case []any:
		var out []string
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return v
	case float64:
		return []string{fmt.Sprint(int(v))}
	}
	return nil
}

func (m ruleMatch) set(key string, vals []string) {
	if len(vals) == 0 {
		delete(m, key)
		return
	}
	if listKeys[key] {
		m[key] = vals
	} else {
		m[key] = strings.Join(vals, ",")
	}
}

func (m ruleMatch) json() string {
	b, _ := json.Marshal(m)
	return string(b)
}

func (m ruleMatch) clone() ruleMatch {
	out := ruleMatch{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

// describe lists the matchers as tree rows, with friendly names.
func (m ruleMatch) describe() [][2]string {
	var rows [][2]string
	for _, k := range matcherKinds {
		vals := m.values(k.key)
		if len(vals) == 0 {
			continue
		}
		shown := make([]string, len(vals))
		for i, v := range vals {
			shown[i] = friendly(v)
		}
		if k.key == "network" || k.key == "port" || k.key == "sourcePort" {
			shown = []string{strings.Join(vals, ",")}
		}
		rows = append(rows, [2]string{k.label, strings.Join(shown, ", ")})
	}
	for k, v := range m { // anything the wizard doesn't know, shown as JSON
		if !slices.ContainsFunc(matcherKinds, func(mk matcherKind) bool { return mk.key == k }) {
			b, _ := json.Marshal(v)
			rows = append(rows, [2]string{k, string(b)})
		}
	}
	return rows
}

func (m ruleMatch) firstFriendly() string {
	for _, k := range matcherKinds {
		if vals := m.values(k.key); len(vals) > 0 {
			return stripANSI(friendly(vals[0]))
		}
	}
	return "rule"
}

// matchFromFlags applies matcher flags to m: replacing each given key, or adding to it
// with appendMode.
func matchFromFlags(a args, m ruleMatch, appendMode bool) (bool, error) {
	changed := false
	for flag, key := range flagKeys {
		if !a.has(flag) {
			continue
		}
		vals := a.list(flag)
		if key == "process" && flag == "process" {
			vals = nil // process names may contain commas; take them whole
			for _, v := range a.flags[flag] {
				if v = strings.TrimSpace(v); v != "" {
					vals = append(vals, v)
				}
			}
		}
		if err := validateMatcher(key, vals); err != nil {
			return false, err
		}
		if appendMode || (key == "process" && flag == "app" && a.has("process")) {
			vals = dedupe(append(m.values(key), vals...))
		}
		m.set(key, vals)
		changed = true
	}
	return changed, nil
}

func validateMatcher(key string, vals []string) error {
	for _, v := range vals {
		var err error
		switch key {
		case "domain":
			err = validateDomain(v)
		case "ip", "source":
			err = validateIP(v)
		case "port", "sourcePort":
			err = validatePorts(v)
		case "network":
			if v != "tcp" && v != "udp" {
				err = fail("network is tcp or udp")
			}
		case "protocol":
			if !slices.Contains([]string{"http", "tls", "quic", "bittorrent"}, v) {
				err = fail("protocol is http, tls, quic or bittorrent")
			}
		}
		if err != nil {
			return fail("%s %q: %v", key, v, err)
		}
	}
	return nil
}

// pickMatch is the wizard's "what should this rule match" step, starting from m.
func pickMatch(m ruleMatch) (ruleMatch, error) {
	opts := make([]ui.Option[string], len(matcherKinds))
	var initial []string
	for i, k := range matcherKinds {
		opts[i] = ui.Option[string]{Label: k.label, Hint: k.hint, Value: k.key}
		if len(m.values(k.key)) > 0 {
			initial = append(initial, k.key)
		}
	}
	kinds, err := ui.MultiSelect("What should this rule match?", opts, initial, true)
	if err != nil {
		return nil, err
	}
	out := ruleMatch{}
	for k, v := range m { // keep keys the wizard doesn't handle
		if !slices.ContainsFunc(matcherKinds, func(mk matcherKind) bool { return mk.key == k }) {
			out[k] = v
		}
	}
	for _, k := range matcherKinds {
		if !slices.Contains(kinds, k.key) {
			continue
		}
		vals, err := pickMatcher(k.key, m.values(k.key))
		if err != nil {
			return nil, err
		}
		out.set(k.key, vals)
	}
	if len(out) == 0 {
		return nil, fail("a rule needs at least one thing to match")
	}
	return out, nil
}

func pickMatcher(key string, current []string) ([]string, error) {
	switch key {
	case "process":
		return pickApps(current)
	case "domain":
		return ui.NewPicker(ui.PickParams{
			Message: "Which websites?", Multi: true, Required: true, Initial: current,
			AddCustom: true, Validate: validateDomain,
			Groups: []ui.PickGroup{geoGroup("By country", siteCountries), geoGroup("Sites & services", siteServices)},
			More:   geoSource(pb.GeoKind_GEO_KIND_SITE),
		}).Run()
	case "ip":
		return ui.NewPicker(ui.PickParams{
			Message: "Which IP addresses?", Multi: true, Required: true, Initial: current,
			AddCustom: true, Validate: validateIP,
			Groups: []ui.PickGroup{geoGroup("By country", ipCountries), geoGroup("Other", ipOther)},
			More:   geoSource(pb.GeoKind_GEO_KIND_IP),
		}).Run()
	case "port", "sourcePort":
		label := "Ports (e.g. 443, 1000-2000)"
		if key == "sourcePort" {
			label = "Source ports (e.g. 5000-6000)"
		}
		v, err := ui.Text(label, strings.Join(current, ","), validatePorts)
		return splitList(v), err
	case "network":
		cur := strings.Join(current, ",")
		if cur == "" {
			cur = "tcp,udp"
		}
		v, err := ui.Select("Which network?", []ui.Option[string]{
			{Label: "TCP and UDP", Value: "tcp,udp"}, {Label: "TCP only", Value: "tcp"}, {Label: "UDP only", Value: "udp"},
		}, cur)
		return splitList(v), err
	case "protocol":
		return ui.MultiSelect("Which protocols? (detected from the traffic)", []ui.Option[string]{
			{Label: "http", Value: "http"}, {Label: "tls", Hint: "HTTPS and most encrypted traffic", Value: "tls"},
			{Label: "quic", Hint: "HTTP/3", Value: "quic"}, {Label: "bittorrent", Value: "bittorrent"},
		}, current, true)
	case "source":
		v, err := ui.Text("Source IPs or ranges (e.g. 192.168.1.20, 10.0.0.0/8)", strings.Join(current, ","), validateIPs)
		return splitList(v), err
	}
	return nil, fail("unknown matcher %q", key)
}

func pickApps(current []string) ([]string, error) {
	return ui.NewPicker(ui.PickParams{
		Message: "Pick an app", Multi: true, Required: true, Initial: current,
		AddCustom: true, Validate: func(v string) error {
			if strings.TrimSpace(v) == "" {
				return fail("enter a process name or path")
			}
			return nil
		},
		Groups: appGroups(),
	}).Run()
}

// pickTarget asks where matching traffic goes; current is a target string.
func pickTarget(all []subNodes, current string) (string, error) {
	opts := []ui.Option[string]{
		{Label: "proxy", Hint: "the active proxy", Value: "proxy"},
		{Label: "a specific proxy…", Hint: "pin to one config, from any subscription", Value: "pin"},
		{Label: "direct", Hint: "skip the proxy", Value: "direct"},
		{Label: "block", Hint: "drop the traffic", Value: "block"},
	}
	if res, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.ListOutboundsResponse, error) {
		return c.ListOutbounds(ctx, &pb.ListOutboundsRequest{})
	}); err == nil {
		for _, o := range res.Outbounds {
			opts = append(opts, ui.Option[string]{Label: o.Tag, Hint: "custom outbound", Value: "outbound:" + o.Tag})
		}
	}
	initial := current
	if strings.HasPrefix(current, "config:") {
		initial = "pin"
	}
	choice, err := ui.Select("Where should matching traffic go?", opts, initial)
	if err != nil || choice != "pin" {
		return choice, err
	}
	if len(all) == 0 {
		return "", fail("no subscriptions to pin to; add one with `masir sub add <url>`")
	}
	key, err := pickNode(all, "Pin the rule to which proxy?", strings.TrimPrefix(current, "config:"))
	if err != nil {
		return "", err
	}
	return "config:" + key, nil
}

// targetFromFlags reads -o/--outbound and --via: proxy, direct, block, outbound:<tag>,
// or a proxy id, which pins the rule to that config.
func targetFromFlags(a args, all []subNodes) (string, bool, error) {
	v := a.str("outbound")
	if a.has("via") {
		v = a.str("via")
	}
	switch {
	case v == "":
		return "", false, nil
	case v == "proxy" || v == "direct" || v == "block" || strings.HasPrefix(v, "outbound:") || strings.HasPrefix(v, "config:"):
		return v, true, nil
	}
	s, cfg, ok := findNode(all, v)
	if !ok {
		return "", false, fail("no proxy with id %q (see `masir use`)", v)
	}
	return "config:" + nodeKey(s.sub.Id, cfg.Id), true, nil
}

func cmdRulesAdd(a args) error {
	all, err := loadNodes()
	if err != nil {
		return err
	}
	rules, err := listRules()
	if err != nil {
		return err
	}

	match := ruleMatch{}
	if a.has("json") {
		if match, err = parseMatch(a.str("json")); err != nil {
			return err
		}
	}
	fromFlags, err := matchFromFlags(a, match, true)
	if err != nil {
		return err
	}
	target, haveTarget, err := targetFromFlags(a, all)
	if err != nil {
		return err
	}

	interactive := !fromFlags && !a.has("json")
	if interactive {
		if !ui.Interactive() {
			return fail("pass matchers as flags (see `masir rules add --help`), or run in a terminal for the wizard")
		}
		if match, err = pickMatch(match); err != nil {
			return err
		}
	}
	if len(match) == 0 {
		return fail("a rule needs at least one thing to match")
	}
	if !haveTarget {
		target = "proxy"
		if interactive {
			if target, err = pickTarget(all, "proxy"); err != nil {
				return err
			}
		}
	}

	name := a.str("name")
	if name == "" {
		name = uniqueName(slug(match.firstFriendly()+"-"+targetWord(target, all)), rules)
		if interactive {
			if name, err = ui.Text("Rule name", name, validateRuleName(rules, "")); err != nil {
				return err
			}
		}
	} else if err := validateRuleName(rules, "")(name); err != nil {
		return err
	}

	sp := ui.Spin("Checking the rule with xray")
	if _, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.AddRoutingResponse, error) {
		return c.AddRouting(ctx, &pb.AddRoutingRequest{RuleTag: name, RuleJson: match.json(), Target: target})
	}); err != nil {
		sp.Fail(message(err))
		return errReported
	}
	st, _ := currentStatus()
	note := "saved; applies when masir connects"
	if st != nil && st.XrayRunning {
		note = "applied to running xray"
	}
	sp.Stop(ui.Visual(fmt.Sprintf("Rule %q %s %s added (%s)", name, ui.Dim("→"), targetText(target, "", false, all), note)))
	ui.Message(ui.Visual(ui.Tree(match.describe())))
	return nil
}

func cmdRulesList(a args) error {
	res, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.ListRoutingRulesResponse, error) {
		return c.ListRoutingRules(ctx, &pb.ListRoutingRulesRequest{})
	})
	if err != nil {
		return err
	}
	if a.boolean("json", false) {
		fmt.Println(protojson.MarshalOptions{Multiline: true}.Format(res))
		return nil
	}
	st, err := currentStatus()
	if err != nil {
		return err
	}
	all := nodesIfPinned(res.Rules)

	var b strings.Builder
	fmt.Fprintf(&b, "\n%s\n", ui.Bold(fmt.Sprintf("Rules (%d)", len(res.Rules))))
	for i, r := range res.Rules {
		m, _ := parseMatch(r.RuleJson)
		fmt.Fprintf(&b, "\n%s %s %s %s\n", ui.Dim(fmt.Sprintf("%d.", i+1)), ui.Bold(r.RuleTag), ui.Dim("→"),
			targetText(r.Target, r.ResolvedOutboundTag, r.FellBack, all))
		if rows := m.describe(); len(rows) > 0 {
			b.WriteString(indent(ui.Tree(rows), "   ") + "\n")
		}
	}
	mode := "direct"
	if st.Settings.GetFallback() == pb.Fallback_FALLBACK_PROXY {
		mode = "proxy"
	}
	fmt.Fprintf(&b, "\n%s %s %s %s %s\n", ui.Dim(fmt.Sprintf("%d.", len(res.Rules)+1)), ui.Bold("default"), ui.Dim("→"),
		targetText(mode, "", false, nil), ui.Dim("(catch-all • change with `masir mode`)"))
	fmt.Fprintf(&b, "\n%s\n", ui.Dim("Top to bottom, the first match wins. DNS and LAN rules are built in."))
	fmt.Print(ui.Visual(b.String())) // rule and config names, in display order
	return nil
}

func cmdRulesRemove(a args) error {
	rules, err := listRules()
	if err != nil {
		return err
	}
	names := a.pos
	if len(names) == 0 {
		if len(rules) == 0 {
			ui.Info("No rules to remove.")
			return nil
		}
		if !ui.Interactive() {
			return fail("name the rules to remove: masir rules rm <name...>")
		}
		all := nodesIfPinned(rules)
		opts := make([]ui.Option[string], len(rules))
		for i, r := range rules {
			opts[i] = ui.Option[string]{Label: r.RuleTag, Hint: "→ " + stripANSI(targetText(r.Target, r.ResolvedOutboundTag, r.FellBack, all)), Value: r.RuleTag}
		}
		if names, err = ui.MultiSelect("Select rules to remove", opts, nil, true); err != nil {
			return err
		}
	}
	for _, n := range names {
		if !slices.ContainsFunc(rules, func(r *pb.RoutingRule) bool { return r.RuleTag == n }) {
			return fail("no rule named %q", n)
		}
	}
	for _, n := range names {
		if _, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.RemoveRoutingResponse, error) {
			return c.RemoveRouting(ctx, &pb.RemoveRoutingRequest{RuleTag: n})
		}); err != nil {
			return err
		}
		ui.Success(ui.Visual(fmt.Sprintf("Removed rule %q.", n)))
	}
	return nil
}

func cmdRulesTest(a args) error {
	dest := a.arg(0)
	if dest == "" {
		return fail("usage: masir rules test <domain|ip> [--port 443] [--network tcp]")
	}
	port := uint32(443)
	if v := a.str("port"); v != "" {
		var n int
		if _, err := fmt.Sscan(v, &n); err != nil || n < 1 || n > 65535 {
			return fail("--port is 1-65535")
		}
		port = uint32(n)
	}
	network := a.str("network")
	if network == "" {
		network = "tcp"
	}
	req := &pb.TestRoutingRequest{Port: port, Network: network}
	kind := "domain"
	if ip := net.ParseIP(dest); ip != nil {
		req.Ip, kind = dest, "IP"
	} else {
		req.Domain = dest
	}
	res, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.TestRoutingResponse, error) {
		return c.TestRouting(ctx, req)
	})
	if err != nil {
		if strings.Contains(message(err), "not running") {
			return fail("masir is off; routes can only be tested while it runs (`masir connect`)")
		}
		return err
	}
	st, _ := currentStatus()
	ui.Note(ui.Visual(strings.Join([]string{
		fmt.Sprintf("%s  %s:%d (%s)", ui.Dim("Destination"), dest, port, network),
		fmt.Sprintf("%s  %s", ui.Dim("Goes to    "), outboundText(res.OutboundTag, st, loadNodesQuiet())),
	}, "\n")), "Route for "+kind)
	return nil
}

func listRules() ([]*pb.RoutingRule, error) {
	res, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.ListRoutingRulesResponse, error) {
		return c.ListRoutingRules(ctx, &pb.ListRoutingRulesRequest{})
	})
	if err != nil {
		return nil, err
	}
	return res.Rules, nil
}

// nodesIfPinned loads subscriptions only when a rule pins a config, to name it.
func nodesIfPinned(rules []*pb.RoutingRule) []subNodes {
	for _, r := range rules {
		if strings.HasPrefix(r.Target, "config:") {
			return loadNodesQuiet()
		}
	}
	return nil
}

func loadNodesQuiet() []subNodes {
	all, _ := loadNodes()
	return all
}

// targetText colors a rule's target: block red, direct green, proxy cyan, pinned
// configs by name, and a fallen-back pin in yellow.
func targetText(target, resolved string, fellBack bool, all []subNodes) string {
	switch {
	case target == "block":
		return ui.Red("block")
	case target == "direct":
		return ui.Green("direct")
	case target == "proxy":
		if fellBack {
			return ui.Cyan("proxy") + ui.Yellow(" (none selected, goes direct)")
		}
		return ui.Cyan("proxy")
	case strings.HasPrefix(target, "outbound:"):
		return ui.Magenta(strings.TrimPrefix(target, "outbound:"))
	case strings.HasPrefix(target, "config:"):
		name := strings.TrimPrefix(target, "config:")
		if _, cfg, ok := findNode(all, name); ok {
			name = cfg.Name
		}
		if fellBack {
			return ui.Yellow(name+" unavailable") + ui.Dim(" → ") + ui.Cyan(resolved)
		}
		return ui.Cyan(name) + ui.Dim(" (pinned)")
	}
	return target
}

// outboundText names an outbound tag from the running config for people.
func outboundText(tag string, st *pb.Status, all []subNodes) string {
	switch {
	case tag == "proxy":
		if st != nil && st.ActiveConfigName != "" {
			return ui.Cyan("proxy") + ui.Dim(" · "+st.ActiveConfigName)
		}
		return ui.Cyan("proxy")
	case tag == "direct":
		return ui.Green("direct")
	case tag == "block":
		return ui.Red("block")
	case tag == "dns-out":
		return "DNS (answered by masir)"
	case strings.HasPrefix(tag, "sub-"):
		for _, s := range all {
			for _, c := range s.configs {
				if tag == "sub-"+s.sub.Id+"-"+c.Id {
					return ui.Cyan(c.Name) + ui.Dim(" (pinned, "+s.sub.Name+")")
				}
			}
		}
	}
	return ui.Magenta(tag)
}

func targetWord(target string, all []subNodes) string {
	if strings.HasPrefix(target, "config:") {
		if _, cfg, ok := findNode(all, strings.TrimPrefix(target, "config:")); ok {
			return cfg.Name
		}
		return "pinned"
	}
	return strings.TrimPrefix(target, "outbound:")
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slug(s string) string {
	s = nonSlug.ReplaceAllString(strings.ToLower(stripANSI(s)), "-")
	s = strings.Trim(s, "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if s == "" || strings.HasPrefix(s, "masir-") {
		s = "rule-" + s
	}
	return strings.TrimSuffix(s, "-")
}

func uniqueName(base string, rules []*pb.RoutingRule) string {
	taken := func(n string) bool {
		return slices.ContainsFunc(rules, func(r *pb.RoutingRule) bool { return r.RuleTag == n })
	}
	name := base
	for i := 2; taken(name); i++ {
		name = fmt.Sprintf("%s-%d", base, i)
	}
	return name
}

func validateRuleName(rules []*pb.RoutingRule, self string) func(string) error {
	return func(n string) error {
		switch {
		case strings.TrimSpace(n) == "":
			return fail("a rule needs a name")
		case strings.HasPrefix(n, "masir-"):
			return fail("names starting with masir- are reserved")
		case n != self && slices.ContainsFunc(rules, func(r *pb.RoutingRule) bool { return r.RuleTag == n }):
			return fail("a rule named %q already exists", n)
		}
		return nil
	}
}

func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func dedupe(vals []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range vals {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	return out
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }
