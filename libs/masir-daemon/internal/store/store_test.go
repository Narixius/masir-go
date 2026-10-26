package store

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/userfs"
)

func TestParseTarget(t *testing.T) {
	for in, want := range map[string]Target{
		"direct":         {Kind: TargetDirect},
		"proxy":          {Kind: TargetProxy},
		"config:s1/c1":   {Kind: TargetConfig, SubscriptionID: "s1", ConfigID: "c1"},
		"outbound:my-wg": {Kind: TargetOutbound, Tag: "my-wg"},
	} {
		got, err := ParseTarget(in)
		if err != nil || got != want || got.String() != in {
			t.Errorf("ParseTarget(%q) = %+v, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "Direct", "config:", "config:s1", "config:/c1", "outbound:", "sub-x"} {
		if _, err := ParseTarget(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseTarget(%q) = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestRules(t *testing.T) {
	var st State
	direct := Target{Kind: TargetDirect}
	if err := st.AddRule("", []byte(`{"ruleTag":"from-json","domain":["full:a.com"]}`), direct, false); err != nil {
		t.Fatal(err)
	}
	if st.Rules[0].Tag != "from-json" || string(st.Rules[0].Match) != `{"domain":["full:a.com"]}` {
		t.Fatalf("rule = %+v", st.Rules[0])
	}
	if err := st.AddRule("first", []byte(`{"domain":["full:b.com"]}`), direct, true); err != nil || st.Rules[0].Tag != "first" {
		t.Fatalf("prepend: %v %+v", err, st.Rules)
	}

	for name, tc := range map[string]struct {
		tag, json string
		target    Target
		want      error
	}{
		"duplicate":        {"first", `{"domain":["full:c.com"]}`, direct, ErrExists},
		"outboundTag":      {"x", `{"domain":["full:c.com"],"outboundTag":"direct"}`, direct, ErrInvalid},
		"reserved tag":     {"masir-x", `{"domain":["full:c.com"]}`, direct, ErrInvalid},
		"no tag":           {"", `{"domain":["full:c.com"]}`, direct, ErrInvalid},
		"bad match":        {"x", `{"port":"not-a-port"}`, direct, ErrInvalid},
		"unknown outbound": {"x", `{"domain":["full:c.com"]}`, Target{Kind: TargetOutbound, Tag: "nope"}, ErrNotFound},
	} {
		if err := st.AddRule(tc.tag, []byte(tc.json), tc.target, false); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}

	block := Target{Kind: TargetBlock}
	if err := st.AlterRule("first", nil, &block); err != nil || st.Rules[0].Target != block || st.Rules[0].Tag != "first" {
		t.Fatalf("alter target: %v %+v", err, st.Rules[0])
	}
	if err := st.RemoveRule("first"); err != nil || len(st.Rules) != 1 {
		t.Fatalf("remove: %v %+v", err, st.Rules)
	}
}

func TestOutboundsAndInbounds(t *testing.T) {
	var st State
	if err := st.AddOutbound("wg", []byte(`{"protocol":"freedom"}`)); err != nil {
		t.Fatal(err)
	}
	if string(st.Outbounds[0].Config) != `{"protocol":"freedom","tag":"wg"}` {
		t.Fatalf("tag should be written into the config: %s", st.Outbounds[0].Config)
	}
	for _, tag := range []string{"proxy", "direct", "dns-out", "masir-x", "sub-abc-def"} {
		if err := st.AddOutbound(tag, []byte(`{"protocol":"freedom"}`)); !errors.Is(err, ErrInvalid) {
			t.Errorf("AddOutbound(%q) = %v, want ErrInvalid", tag, err)
		}
	}
	if err := st.AddOutbound("bad", []byte(`{"protocol":"nope"}`)); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown protocol: %v", err)
	}

	_ = st.AddRule("to-wg", []byte(`{"domain":["full:a.com"]}`), Target{Kind: TargetOutbound, Tag: "wg"}, false)
	if err := st.RemoveOutbound("wg"); !errors.Is(err, ErrInUse) {
		t.Fatalf("removing a rule's target: %v", err)
	}
	_ = st.RemoveRule("to-wg")
	if err := st.RemoveOutbound("wg"); err != nil {
		t.Fatal(err)
	}

	if err := st.AddInbound("tun-in", []byte(`{"protocol":"socks","port":1080}`)); !errors.Is(err, ErrInvalid) {
		t.Errorf("reserved inbound: %v", err)
	}
	if err := st.AddInbound("extra", []byte(`{"protocol":"socks","listen":"127.0.0.1","port":1080}`)); err != nil {
		t.Fatal(err)
	}
	if err := st.AlterInbound("missing", []byte(`{"protocol":"socks","listen":"127.0.0.1","port":1}`)); !errors.Is(err, ErrNotFound) {
		t.Errorf("alter missing: %v", err)
	}
}

func TestLoadSave(t *testing.T) {
	dir := t.TempDir()
	st, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Settings.AllowLAN || st.Settings.AlwaysOnProxy || st.Settings.ProxyPort != 7890 || st.Settings.Fallback != FallbackProxy || st.Settings.XrayLogLevel != "warning" || !st.Settings.Analytics {
		t.Fatalf("defaults = %+v", st.Settings)
	}

	st.Settings.Power = true
	st.Settings.XrayLogLevel = "info"
	st.Settings.Analytics = false
	_ = st.AddRule("r", []byte(`{"domain":["full:a.com"]}`), Target{Kind: TargetConfig, SubscriptionID: "s", ConfigID: "c", Name: "n"}, false)
	_ = st.AddOutbound("wg", []byte(`{"protocol":"freedom"}`))
	if err := Save(dir, st); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, settingsFile))
	if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o600) {
		t.Fatalf("settings file: %v %v", info, err)
	}

	back, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Settings.Power || back.Settings.XrayLogLevel != "info" || back.Settings.Analytics || len(back.Rules) != 1 || back.Rules[0].Target.Name != "n" || len(back.Outbounds) != 1 {
		t.Fatalf("round trip = %+v", back)
	}

	// A settings file from an older version keeps defaults for what it doesn't mention,
	// and keeps what it does: an install that saved allowLan on stays shared.
	if err := os.WriteFile(filepath.Join(dir, settingsFile), []byte(`{"power":true,"allowLan":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old, err := Load(dir)
	if err != nil || !old.Settings.AllowLAN || old.Settings.AlwaysOnProxy || old.Settings.ProxyPort != 7890 {
		t.Fatalf("partial settings = %+v, %v", old.Settings, err)
	}
}

func TestSettingsValidate(t *testing.T) {
	for name, mutate := range map[string]func(*Settings){
		"port":     func(s *Settings) { s.ProxyPort = 0 },
		"fallback": func(s *Settings) { s.Fallback = "sideways" },
		"doh":      func(s *Settings) { s.DoHDNS = "http://1.1.1.1/dns-query" },
		"dns":      func(s *Settings) { s.DNSServers = []string{"dns.google"} },
		"no dns":   func(s *Settings) { s.DNSServers = nil },
		"gateway":  func(s *Settings) { s.TunGateway = "fd00::1/64" },
		"log":      func(s *Settings) { s.XrayLogLevel = "verbose" },
	} {
		s := DefaultSettings()
		mutate(&s)
		if err := s.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
	if err := DefaultSettings().Validate(); err != nil {
		t.Fatalf("defaults invalid: %v", err)
	}
	if got := NormalizeXrayLogLevel(" WARN "); got != "warning" {
		t.Fatalf("NormalizeXrayLogLevel = %q, want warning", got)
	}
}

func TestRenameAndMoveRules(t *testing.T) {
	var st State
	for _, n := range []string{"a", "b", "c"} {
		if err := st.AddRule(n, []byte(`{"domain":["full:`+n+`.com"]}`), Target{Kind: TargetDirect}, false); err != nil {
			t.Fatal(err)
		}
	}
	order := func() string {
		s := ""
		for _, r := range st.Rules {
			s += r.Tag
		}
		return s
	}
	if err := st.MoveRule("c", 0); err != nil || order() != "cab" {
		t.Fatalf("move first: %v %s", err, order())
	}
	if err := st.MoveRule("c", 99); err != nil || order() != "abc" {
		t.Fatalf("move past the end: %v %s", err, order())
	}
	if err := st.RenameRule("b", "bee"); err != nil || st.Rules[1].Tag != "bee" {
		t.Fatalf("rename: %v %+v", err, st.Rules)
	}
	for name, tc := range map[string]struct {
		from, to string
		want     error
	}{
		"taken":    {"a", "c", ErrExists},
		"reserved": {"a", "masir-x", ErrInvalid},
		"missing":  {"zz", "y", ErrNotFound},
	} {
		if err := st.RenameRule(tc.from, tc.to); !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", name, err, tc.want)
		}
	}
	if err := st.MoveRule("zz", 0); !errors.Is(err, ErrNotFound) {
		t.Errorf("move missing: %v", err)
	}
}

func TestSubIntervalBounds(t *testing.T) {
	for minutes, ok := range map[int]bool{4: false, 5: true, 720: true, 43200: true, 43201: false, 0: false, -1: false} {
		s := DefaultSettings()
		s.SubIntervalMinutes = minutes
		if err := s.Validate(); (err == nil) != ok {
			t.Errorf("%d minutes: valid=%v, want %v (%v)", minutes, err == nil, ok, err)
		}
	}
	if DefaultSettings().SubIntervalMinutes != 720 {
		t.Error("the default is 12 hours")
	}
}

// A privileged daemon reaches the user's directory only through a pinned handle; first
// runs (no files yet), saves and loads must work the same through it.
func TestLoadSaveThroughAPinnedDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows doesn't pin the directory")
	}
	dir := filepath.Join(t.TempDir(), "masir")
	if err := userfs.Pin(dir, os.Getuid(), os.Getgid()); err != nil {
		t.Fatal(err)
	}
	defer userfs.Unpin()

	st, err := Load(dir)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	st.Settings.XrayLogLevel = "info"
	if err := Save(dir, st); err != nil {
		t.Fatal(err)
	}
	back, err := Load(dir)
	if err != nil || back.Settings.XrayLogLevel != "info" {
		t.Fatalf("round trip = %+v, %v", back.Settings, err)
	}
}

func TestDisabledRulesStayDisabledUntilTheUserEnablesThem(t *testing.T) {
	dir := t.TempDir()
	st := State{Settings: DefaultSettings()}
	if err := st.AddRule("ssh", []byte(`{"port":"22"}`), Target{Kind: TargetDirect}, false); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRuleEnabled("ssh", false, "geoip:in-ir is gone"); err != nil {
		t.Fatal(err)
	}
	// Editing a disabled rule doesn't switch it back on.
	if err := st.AlterRule("ssh", []byte(`{"port":"2222"}`), nil); err != nil {
		t.Fatal(err)
	}
	if r := st.Rules[0]; !r.Disabled || r.DisabledReason != "geoip:in-ir is gone" {
		t.Fatalf("after an edit: %+v", r)
	}
	// It survives a save and a load.
	if err := Save(dir, st); err != nil {
		t.Fatal(err)
	}
	back, err := Load(dir)
	if err != nil || !back.Rules[0].Disabled || back.Rules[0].DisabledReason == "" {
		t.Fatalf("after a reload: %+v, %v", back.Rules, err)
	}
	if err := back.SetRuleEnabled("ssh", true, ""); err != nil {
		t.Fatal(err)
	}
	if r := back.Rules[0]; r.Disabled || r.DisabledReason != "" {
		t.Fatalf("after enabling: %+v", r)
	}
	if err := back.SetRuleEnabled("nope", false, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing rule: %v", err)
	}
	// An enabled rule has no "disabled" keys in the file.
	raw, _ := os.ReadFile(filepath.Join(dir, routingFile))
	if err := Save(dir, back); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(filepath.Join(dir, routingFile))
	if strings.Contains(string(raw), "disabled") {
		t.Fatalf("routing.json mentions disabled for an enabled rule: %s", raw)
	}
}

func TestSettingsFromBeforeTheRulesSwitchKeepRulesOn(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"proxyPort":7000,"fallback":"proxy","dohDns":"https://1.1.1.1/dns-query","dnsServers":["1.1.1.1"],"tunGateway":"10.10.20.1/24","subIntervalMinutes":60,"xrayLogLevel":"warning"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Settings.RulesEnabled {
		t.Fatal("rules went off for settings saved before the switch existed")
	}
}
