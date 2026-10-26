package main

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// setting is one user-facing setting; key is its name on the command line.
type setting struct {
	key, label, desc string
	aliases          []string
	kind             string // "bool", "list", "mode", "level", or "" for text
	get              func(*pb.Settings) string
	validate         func(string) error
	// apply turns a value into a change; mode has its own RPC.
	apply func(req *pb.UpdateSettingsRequest, v string)
}

var settingsTable = []setting{
	{key: "routingMode", label: "Routing mode", desc: "where traffic no rule matches goes", aliases: []string{"mode", "fallback"}, kind: "mode",
		get: func(s *pb.Settings) string {
			if s.GetFallback() == pb.Fallback_FALLBACK_PROXY {
				return "proxy"
			}
			return "direct"
		},
		validate: func(v string) error {
			if v != "proxy" && v != "direct" {
				return fail("routing mode is proxy or direct")
			}
			return nil
		}},
	{key: "mixedPort", label: "Proxy port", desc: "SOCKS5 and HTTP on one port", aliases: []string{"port", "proxyport"},
		get: func(s *pb.Settings) string { return strconv.Itoa(int(s.GetProxyPort())) },
		validate: func(v string) error {
			if n, err := strconv.Atoi(v); err != nil || n < 1 || n > 65535 {
				return fail("a port is 1-65535")
			}
			return nil
		},
		apply: func(r *pb.UpdateSettingsRequest, v string) { n, _ := strconv.Atoi(v); p := uint32(n); r.ProxyPort = &p }},
	{key: "allowLan", label: "Allow LAN", desc: "let other devices on your network use the proxy", aliases: []string{"lan"}, kind: "bool",
		get:   func(s *pb.Settings) string { return strconv.FormatBool(s.GetAllowLan()) },
		apply: func(r *pb.UpdateSettingsRequest, v string) { b := v == "true"; r.AllowLan = &b }},
	{key: "alwaysOnProxy", label: "Always-on proxy", desc: "keep the proxy port up even when masir is off", aliases: []string{"alwayson"}, kind: "bool",
		get:   func(s *pb.Settings) string { return strconv.FormatBool(s.GetAlwaysOnProxy()) },
		apply: func(r *pb.UpdateSettingsRequest, v string) { b := v == "true"; r.AlwaysOnProxy = &b }},
	{key: "subInterval", label: "Subscription refresh", desc: "minutes between background subscription refreshes",
		aliases: []string{"subrefresh", "subscriptioninterval", "refresh"}, kind: "minutes",
		get:      func(s *pb.Settings) string { return strconv.Itoa(int(s.GetSubIntervalMinutes())) },
		validate: validateMinutes,
		apply: func(r *pb.UpdateSettingsRequest, v string) {
			n, _ := strconv.Atoi(strings.TrimSpace(v))
			m := uint32(n)
			r.SubIntervalMinutes = &m
		}},
	{key: "dohDns", label: "DNS over HTTPS", desc: "resolves names, through the proxy", aliases: []string{"doh"},
		get: func(s *pb.Settings) string { return s.GetDohDns() },
		validate: func(v string) error {
			if u, err := url.Parse(v); err != nil || u.Scheme != "https" || u.Host == "" {
				return fail("enter an https:// URL, like https://1.1.1.1/dns-query")
			}
			return nil
		},
		apply: func(r *pb.UpdateSettingsRequest, v string) { r.DohDns = &v }},
	{key: "dnsServers", label: "Bootstrap DNS", desc: "plain DNS IPs for proxy server names, sent direct", aliases: []string{"dns"}, kind: "list",
		get:      func(s *pb.Settings) string { return strings.Join(s.GetDnsServers(), ",") },
		validate: requireIPs(true),
		apply:    func(r *pb.UpdateSettingsRequest, v string) { r.DnsServers = &pb.StringList{Values: splitList(v)} }},
	{key: "tunDns", label: "TUN DNS", desc: "DNS the TUN interface advertises; empty uses the bootstrap DNS", kind: "list",
		get:      func(s *pb.Settings) string { return strings.Join(s.GetTunDns(), ",") },
		validate: requireIPs(false),
		apply:    func(r *pb.UpdateSettingsRequest, v string) { r.TunDns = &pb.StringList{Values: splitList(v)} }},
	{key: "logLevel", label: "Xray log level", desc: "debug, info, warning, error, or none", aliases: []string{"xraylog", "log"}, kind: "level",
		get: func(s *pb.Settings) string {
			if s.GetXrayLogLevel() == "" {
				return "warning"
			}
			return s.GetXrayLogLevel()
		},
		validate: func(v string) error {
			switch v {
			case "debug", "info", "warning", "error", "none":
				return nil
			}
			return fail("log level is debug, info, warning, error, or none")
		},
		apply: func(r *pb.UpdateSettingsRequest, v string) { r.XrayLogLevel = &v }},
	{key: "analytics", label: "Anonymous analytics", desc: "send anonymous usage and error data to improve masir", aliases: []string{"telemetry", "usage", "posthog"}, kind: "bool",
		get:   func(s *pb.Settings) string { return strconv.FormatBool(s.GetAnalytics()) },
		apply: func(r *pb.UpdateSettingsRequest, v string) { b := v == "true"; r.Analytics = &b }},
	{key: "gateway", label: "TUN gateway", desc: "IPv4 range for the TUN interface", aliases: []string{"tungateway"},
		get: func(s *pb.Settings) string { return s.GetTunGateway() },
		validate: func(v string) error {
			if ip, _, err := net.ParseCIDR(v); err != nil || ip.To4() == nil {
				return fail("enter an IPv4 CIDR, like 10.10.20.1/24")
			}
			return nil
		},
		apply: func(r *pb.UpdateSettingsRequest, v string) { r.TunGateway = &v }},
}

// Subscription refresh intervals, in minutes: the daemon looks for due subscriptions
// every 5 minutes, so less couldn't run sooner; 30 days at most.
const minRefreshMinutes, maxRefreshMinutes = 5, 30 * 24 * 60

func validateMinutes(v string) error {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fail("enter a whole number of minutes, like 720 for 12 hours")
	}
	if n < minRefreshMinutes || n > maxRefreshMinutes {
		return fail("enter %d to %d minutes (5 minutes to 30 days)", minRefreshMinutes, maxRefreshMinutes)
	}
	return nil
}

// humanMinutes reads a number of minutes as days, hours and minutes: 90 is "1h 30m".
func humanMinutes(n int) string {
	d, h, m := n/(24*60), n/60%24, n%60
	var parts []string
	if d > 0 {
		parts = append(parts, strconv.Itoa(d)+"d")
	}
	if h > 0 {
		parts = append(parts, strconv.Itoa(h)+"h")
	}
	if m > 0 || len(parts) == 0 {
		parts = append(parts, strconv.Itoa(m)+"m")
	}
	return strings.Join(parts, " ")
}

func requireIPs(required bool) func(string) error {
	return func(v string) error {
		ips := splitList(v)
		if required && len(ips) == 0 {
			return fail("enter at least one IP")
		}
		for _, ip := range ips {
			if net.ParseIP(ip) == nil {
				return fail("%q is not an IP address", ip)
			}
		}
		return nil
	}
}

func normKey(k string) string {
	return strings.ToLower(strings.NewReplacer("-", "", "_", "", " ", "").Replace(k))
}

func findSetting(k string) (setting, error) {
	n := normKey(k)
	for _, s := range settingsTable {
		if normKey(s.key) == n || slices.Contains(s.aliases, n) {
			return s, nil
		}
	}
	keys := make([]string, len(settingsTable))
	for i, s := range settingsTable {
		keys[i] = s.key
	}
	return setting{}, fail("unknown setting %q; one of: %s", k, strings.Join(keys, ", "))
}

func loadSettings() (*pb.GetSettingsResponse, error) {
	return call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.GetSettingsResponse, error) {
		return c.GetSettings(ctx, &pb.GetSettingsRequest{})
	})
}

// normValue accepts on/off, yes/no for booleans and spaces in lists.
func normValue(s setting, v string) (string, error) {
	v = strings.TrimSpace(v)
	switch s.kind {
	case "bool":
		switch strings.ToLower(v) {
		case "true", "on", "yes", "1", "enable", "enabled":
			return "true", nil
		case "false", "off", "no", "0", "disable", "disabled":
			return "false", nil
		}
		return "", fail("%s is on or off", s.key)
	case "list":
		v = strings.Join(splitList(strings.ReplaceAll(v, " ", ",")), ",")
	case "mode":
		v = strings.ToLower(v)
	case "level":
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "warn" {
			v = "warning"
		}
	}
	if s.validate != nil {
		if err := s.validate(v); err != nil {
			return "", err
		}
	}
	return v, nil
}

// applySetting saves one value and reports how it took effect.
func applySetting(s setting, v string) error {
	if s.kind == "mode" {
		f := pb.Fallback_FALLBACK_DIRECT
		if v == "proxy" {
			f = pb.Fallback_FALLBACK_PROXY
		}
		_, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.SetFallbackResponse, error) {
			return c.SetFallback(ctx, &pb.SetFallbackRequest{Fallback: f})
		})
		if status.Code(err) == codes.FailedPrecondition {
			msg, _ := describe(err)
			return advised(sentence(msg), "Pick one with:", "masir use")
		}
		if err != nil {
			return err
		}
	} else {
		req := &pb.UpdateSettingsRequest{}
		s.apply(req, v)
		if _, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.UpdateSettingsResponse, error) {
			return c.UpdateSettings(ctx, req)
		}); err != nil {
			return err
		}
	}
	st, err := currentStatus()
	if err != nil {
		return err
	}
	shown := displayValue(s, v)
	note := appliedNote(st)
	if s.kind == "minutes" { // the daemon's own schedule, not Xray's: in effect at once
		note = "the daemon uses it from now on"
	}
	if s.key == "logLevel" && st.XrayRunning {
		note = "Xray restarts for a moment to use it"
	}
	ui.Success(fmt.Sprintf("%s set to %s (%s).", s.label, ui.Visual(shown), note))
	if (s.key == "mixedPort" || s.key == "allowLan") && st.ProxyRunning {
		ui.Info("Proxy now on " + st.ProxyAddress + ".")
	}
	return nil
}

func displayValue(s setting, v string) string {
	switch {
	case s.kind == "bool" && v == "true":
		return ui.Green("on")
	case s.kind == "bool":
		return ui.Gray("off")
	case v == "":
		return ui.Dim("(none)")
	case s.kind == "minutes":
		if n, err := strconv.Atoi(v); err == nil {
			return ui.Cyan(v+" min") + ui.Dim(" ("+humanMinutes(n)+")")
		}
	case s.kind == "level":
		return ui.Cyan(v)
	}
	return ui.Cyan(strings.ReplaceAll(v, ",", ", "))
}

func cmdSettings(a args) error {
	if !ui.Interactive() {
		return cmdSettingsList(a)
	}
	last := ""
	for {
		res, err := loadSettings()
		if err != nil {
			return err
		}
		opts := make([]ui.Option[string], 0, len(settingsTable)+1)
		for _, s := range settingsTable {
			v := s.get(res.Settings)
			hint := stripANSI(displayValue(s, v))
			if v == s.get(res.Defaults) {
				hint += " (default)"
			}
			opts = append(opts, ui.Option[string]{Label: s.label, Hint: hint, Value: s.key})
		}
		opts = append(opts, ui.Option[string]{Label: "Done", Value: ""})
		key, err := ui.Select("Which setting?", opts, last)
		if err != nil || key == "" {
			if ui.IsCancel(err) || key == "" {
				return nil
			}
			return err
		}
		last = key
		s, err := findSetting(key)
		if err != nil {
			return err
		}
		if err := editSetting(s, res); err != nil && !ui.IsCancel(err) {
			showError(err)
		}
	}
}

func editSetting(s setting, res *pb.GetSettingsResponse) error {
	cur, def := s.get(res.Settings), s.get(res.Defaults)
	if cur != def {
		what, err := ui.Select(s.label, []ui.Option[string]{
			{Label: "Change it", Hint: "now " + stripANSI(displayValue(s, cur)), Value: "change"},
			{Label: "Reset to default", Hint: "follow the default again: " + stripANSI(displayValue(s, def)), Value: "reset"},
			{Label: "Back", Value: "back"},
		}, "change")
		if err != nil || what == "back" {
			return err
		}
		if what == "reset" {
			return applySetting(s, def)
		}
	}
	var v string
	var err error
	switch s.kind {
	case "bool":
		var on bool
		on, err = ui.Confirm(fmt.Sprintf("%s? (%s)", s.label, s.desc), cur == "true")
		v = strconv.FormatBool(on)
	case "mode":
		v, err = ui.Select(s.label+" (catch-all at the end of rules)", []ui.Option[string]{
			{Label: "Proxy", Hint: "route unmatched traffic through the active proxy", Value: "proxy"},
			{Label: "Direct", Hint: "send unmatched traffic directly without proxy", Value: "direct"},
		}, cur)
	case "level":
		v, err = ui.Select(s.label, []ui.Option[string]{
			{Label: "Debug", Hint: "everything, including connection details", Value: "debug"},
			{Label: "Info", Hint: "startup and notable events", Value: "info"},
			{Label: "Warning", Hint: "warnings and errors only (default)", Value: "warning"},
			{Label: "Error", Hint: "errors only", Value: "error"},
			{Label: "None", Hint: "turn Xray's own logging off", Value: "none"},
		}, cur)
	default:
		v, err = ui.Text(fmt.Sprintf("%s (%s)", s.label, s.desc), cur, func(in string) error {
			_, err := normValue(s, in)
			return err
		})
	}
	if err != nil {
		return err
	}
	if v, err = normValue(s, v); err != nil {
		return err
	}
	if v == cur {
		ui.Info("Unchanged.")
		return nil
	}
	return applySetting(s, v)
}

func cmdSettingsList(args) error {
	res, err := loadSettings()
	if err != nil {
		return err
	}
	rows := make([][2]string, len(settingsTable))
	for i, s := range settingsTable {
		v := s.get(res.Settings)
		shown := displayValue(s, v)
		if v == s.get(res.Defaults) {
			shown += ui.Dim(" (default)")
		}
		rows[i] = [2]string{s.label + ui.Dim(" "+s.key), shown}
	}
	ui.Note(ui.Visual(ui.Tree(rows))+"\n\n"+ui.Dim("Change with `masir settings set <key> <value>`."), "Settings")
	return nil
}

func cmdSettingsGet(a args) error {
	s, err := findSetting(a.arg(0))
	if err != nil {
		return err
	}
	res, err := loadSettings()
	if err != nil {
		return err
	}
	fmt.Println(s.get(res.Settings))
	return nil
}

func cmdSettingsSet(a args) error {
	if len(a.pos) < 2 {
		return fail("usage: masir settings set <key> <value...>")
	}
	s, err := findSetting(a.arg(0))
	if err != nil {
		return err
	}
	v, err := normValue(s, strings.Join(a.pos[1:], ","))
	if err != nil {
		return err
	}
	return applySetting(s, v)
}

func cmdSettingsReset(a args) error {
	s, err := findSetting(a.arg(0))
	if err != nil {
		return err
	}
	res, err := loadSettings()
	if err != nil {
		return err
	}
	return applySetting(s, s.get(res.Defaults))
}
