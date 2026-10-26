package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	xgeo "github.com/xtls/xray-core/common/geodata"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	"github.com/narixius/masir-go/libs/masir-daemon/internal/identity"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/store"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/subs"
	"github.com/narixius/masir-go/libs/masir-daemon/internal/xray"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// writeGeo (re)writes geoip.dat and geosite.dat in dir with these codes, then tells Xray
// to reload them.
func writeGeo(t *testing.T, dir string, ips, sites []string) {
	t.Helper()
	var ipl xgeo.GeoIPList
	for _, c := range ips {
		ipl.Entry = append(ipl.Entry, &xgeo.GeoIP{Code: strings.ToUpper(c), Cidr: []*xgeo.CIDR{{Ip: []byte{1, 2, 3, 0}, Prefix: 24}}})
	}
	var sl xgeo.GeoSiteList
	for _, c := range sites {
		sl.Entry = append(sl.Entry, &xgeo.GeoSite{Code: strings.ToUpper(c), Domain: []*xgeo.Domain{{Value: "a.com"}}})
	}
	for name, m := range map[string]proto.Message{"geoip.dat": &ipl, "geosite.dat": &sl} {
		b, _ := proto.Marshal(m)
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_ = xray.ReloadGeodata()
}

// geoServer is a booted server whose Xray reads geo data from a temp dir.
func geoServer(t *testing.T, ips, sites []string) (*Server, string) {
	t.Helper()
	geoDir := t.TempDir()
	t.Setenv("XRAY_LOCATION_ASSET", geoDir)
	writeGeo(t, geoDir, ips, sites)
	xc := xray.New(geoDir)
	s := New(xc, identity.Identity{UID: os.Getuid(), GID: os.Getgid(), Name: "tester"})
	s.dir = t.TempDir()
	t.Cleanup(func() { _ = xc.Close() })
	s.state.Settings.AlwaysOnProxy = false
	s.state.Settings.AllowLAN = false
	s.state.Settings.ProxyPort = freePort(t)
	m, err := subs.NewManager(filepath.Join(s.dir, "subscriptions"), filepath.Join(s.dir, "active.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	s.subs = m
	return s, geoDir
}

func reasonOf(err error) string {
	for _, d := range status.Convert(err).Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok {
			return info.Reason
		}
	}
	return ""
}

func addRule(t *testing.T, s *Server, tag, match, target string) {
	t.Helper()
	if _, err := s.AddRouting(context.Background(), &pb.AddRoutingRequest{RuleTag: tag, RuleJson: match, Target: target}); err != nil {
		t.Fatal(err)
	}
}

func TestStartingIsRefusedWhileARuleCantBeBuilt(t *testing.T) {
	ctx := context.Background()
	s, geoDir := geoServer(t, []string{"ir", "in-ir", "de"}, []string{"google"})
	addRule(t, s, "iran-direct", `{"ip":["geoip:ir","geoip:in-ir"]}`, "direct")
	addRule(t, s, "ssh", `{"port":"22"}`, "direct")

	// A geo update (or a hand-copied file) takes in-ir away.
	writeGeo(t, geoDir, []string{"ir", "de"}, []string{"google"})

	res, err := s.CheckRules(ctx, &pb.CheckRulesRequest{})
	if err != nil || len(res.Problems) != 1 || res.Problems[0].RuleTag != "iran-direct" ||
		len(res.Problems[0].Missing) != 1 || res.Problems[0].Missing[0] != "geoip:in-ir" {
		t.Fatalf("CheckRules = %+v, %v", res, err)
	}

	_, err = s.SetPower(ctx, &pb.SetPowerRequest{On: true})
	if status.Code(err) != codes.FailedPrecondition || reasonOf(err) != "RULES_BROKEN" || !strings.Contains(err.Error(), "iran-direct") {
		t.Fatalf("SetPower = %v (reason %q)", err, reasonOf(err))
	}
	if s.xray.Running() {
		t.Fatal("xray started with a rule it can't build")
	}
	if saved := s.state.Settings; saved.Power {
		t.Fatal("a refused start must not be saved as on")
	}
	st, _ := s.GetStatus(ctx, &pb.GetStatusRequest{})
	if len(st.Status.RuleProblems) != 1 || st.Status.RuleProblems[0].Suspended {
		t.Fatalf("status problems = %+v", st.Status.RuleProblems)
	}

	// The user agrees to turn the rule off; then it starts, and the rule stays listed.
	if _, err := s.SetRuleEnabled(ctx, &pb.SetRuleEnabledRequest{RuleTags: []string{"iran-direct"}, Enabled: false, Reason: "geoip:in-ir is gone"}); err != nil {
		t.Fatal(err)
	}
	on(t, s)
	if !s.xray.Running() {
		t.Fatal("xray should start once the rule is off")
	}
	rules, _ := s.ListRoutingRules(ctx, &pb.ListRoutingRulesRequest{})
	if len(rules.Rules) != 2 || !rules.Rules[0].Disabled || rules.Rules[0].DisabledReason != "geoip:in-ir is gone" || rules.Rules[1].Disabled {
		t.Fatalf("rules = %+v", rules.Rules)
	}

	// It can't be turned back on while the data can't build it, and can once it can.
	_, err = s.SetRuleEnabled(ctx, &pb.SetRuleEnabledRequest{RuleTags: []string{"iran-direct"}, Enabled: true})
	if status.Code(err) != codes.FailedPrecondition || !strings.Contains(err.Error(), "geoip:in-ir isn't in geoip.dat") {
		t.Fatalf("enable while broken: %v", err)
	}
	writeGeo(t, geoDir, []string{"ir", "in-ir", "de"}, []string{"google"})
	if _, err := s.SetRuleEnabled(ctx, &pb.SetRuleEnabledRequest{RuleTags: []string{"iran-direct"}, Enabled: true}); err != nil {
		t.Fatalf("enable once fixed: %v", err)
	}
}

func TestARunningXrayIsLeftAloneWhenARuleBreaks(t *testing.T) {
	ctx := context.Background()
	s, geoDir := geoServer(t, []string{"ir", "in-ir"}, []string{"google"})
	addRule(t, s, "iran-direct", `{"ip":["geoip:in-ir"]}`, "direct")
	on(t, s)
	if !s.xray.Running() {
		t.Fatal("setup: xray should be running")
	}
	before := string(s.config)

	writeGeo(t, geoDir, []string{"ir"}, []string{"google"})

	// A change a user asks for is refused, not applied by restarting Xray without the rule.
	_, err := s.AddRouting(ctx, &pb.AddRoutingRequest{RuleTag: "x", RuleJson: `{"port":"22"}`, Target: "direct"})
	if reasonOf(err) != "RULES_BROKEN" {
		t.Fatalf("AddRouting = %v", err)
	}
	// So is a background apply (a subscription refresh, a retry).
	s.mu.Lock()
	err = s.reconcileLocked(ctx)
	s.mu.Unlock()
	if reasonOf(err) != "RULES_BROKEN" {
		t.Fatalf("reconcile = %v", err)
	}
	if !s.xray.Running() || string(s.config) != before {
		t.Fatal("what was running was changed")
	}
	if rules, _ := s.ListRoutingRules(ctx, &pb.ListRoutingRulesRequest{}); len(rules.Rules) != 1 {
		t.Fatalf("the refused rule was saved: %+v", rules.Rules)
	}
}

// bootWith boots a server over a saved state whose rules the geo data can't all build.
func bootWith(t *testing.T, st store.State, ips []string) (*Server, string) {
	t.Helper()
	geoDir := t.TempDir()
	t.Setenv("XRAY_LOCATION_ASSET", geoDir)
	writeGeo(t, geoDir, ips, []string{"google"})
	dir := t.TempDir()
	st.Settings.Power = true
	st.Settings.AlwaysOnProxy = true
	st.Settings.AllowLAN = false
	st.Settings.ProxyPort = freePort(t)
	if err := store.Save(dir, st); err != nil {
		t.Fatal(err)
	}
	xc := xray.New(geoDir)
	t.Cleanup(func() { _ = xc.Close() })
	s := New(xc, identity.Identity{UID: os.Getuid(), GID: os.Getgid(), Name: "tester"})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := s.Boot(ctx, dir); err != nil {
		t.Fatal(err)
	}
	return s, geoDir
}

// A rule saved while the data had its category, which a restart then finds gone.
func stateWithRule(t *testing.T, geoDir string, tag, match, target string, fallback store.Fallback) store.State {
	t.Helper()
	st := store.State{Settings: store.DefaultSettings()}
	st.Settings.Fallback = fallback
	tg, err := store.ParseTarget(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddRule(tag, []byte(match), tg, false); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestBootSuspendsABrokenRuleOnlyWhenThatCantExposeTraffic(t *testing.T) {
	// The rule is valid when saved (the first data has in-ir) and broken at boot (the second doesn't).
	seed := t.TempDir()
	t.Setenv("XRAY_LOCATION_ASSET", seed)
	writeGeo(t, seed, []string{"ir", "in-ir"}, []string{"google"})

	for name, c := range map[string]struct {
		target   string
		fallback store.Fallback
		starts   bool
	}{
		"a direct rule falls to the proxy":            {"direct", store.FallbackProxy, true},
		"a direct rule with a direct fallback":        {"direct", store.FallbackDirect, true},
		"a proxy rule with no server to fall back to": {"proxy", store.FallbackProxy, false}, // no config is selected, so the default route is direct
		"a proxy rule with a direct fallback":         {"proxy", store.FallbackDirect, false},
		"a block rule would let traffic through":      {"block", store.FallbackProxy, false},
	} {
		t.Run(name, func(t *testing.T) {
			st := stateWithRule(t, seed, "r", `{"ip":["geoip:in-ir"]}`, c.target, c.fallback)
			s, _ := bootWith(t, st, []string{"ir"})
			if got := s.xray.Running(); got != c.starts {
				t.Fatalf("xray running = %v, want %v (lastErr %q)", got, c.starts, s.lastErr)
			}
			ctx := context.Background()
			stat, _ := s.GetStatus(ctx, &pb.GetStatusRequest{})
			if len(stat.Status.RuleProblems) != 1 || stat.Status.RuleProblems[0].Suspended != c.starts {
				t.Fatalf("status problems = %+v", stat.Status.RuleProblems)
			}
			if !c.starts && !strings.Contains(stat.Status.LastError, "RULES") && !strings.Contains(stat.Status.LastError, "1 rule(s) can't be built") {
				t.Errorf("last error = %q", stat.Status.LastError)
			}
			if c.starts {
				// Suspended is not disabled: the saved rule is untouched.
				if rules, _ := s.ListRoutingRules(ctx, &pb.ListRoutingRulesRequest{}); len(rules.Rules) != 1 || rules.Rules[0].Disabled {
					t.Fatalf("rules = %+v", rules.Rules)
				}
			}
		})
	}
}

func TestASuspendedRuleComesBackWhenTheDataCanBuildItAgain(t *testing.T) {
	seed := t.TempDir()
	t.Setenv("XRAY_LOCATION_ASSET", seed)
	writeGeo(t, seed, []string{"ir", "in-ir"}, []string{"google"})
	st := stateWithRule(t, seed, "iran", `{"ip":["geoip:in-ir"]}`, "direct", store.FallbackProxy)

	s, geoDir := bootWith(t, st, []string{"ir"})
	if !s.xray.Running() || len(s.suspended) != 1 {
		t.Fatalf("running=%v suspended=%v", s.xray.Running(), s.suspended)
	}
	writeGeo(t, geoDir, []string{"ir", "in-ir"}, []string{"google"})
	s.mu.Lock()
	err := s.reconcileLocked(context.Background())
	s.mu.Unlock()
	if err != nil || len(s.suspended) != 0 || len(s.ruleProblems) != 0 {
		t.Fatalf("after the data was fixed: err=%v suspended=%v problems=%v", err, s.suspended, s.ruleProblems)
	}
	if len(s.resolved) != 1 {
		t.Fatalf("the rule isn't back in the config: %+v", s.resolved)
	}
}

func TestGeoUpdatePreflightAndDisable(t *testing.T) {
	ctx := context.Background()
	s, geoDir := geoServer(t, []string{"ir", "in-ir"}, []string{"google"})
	addRule(t, s, "iran-direct", `{"ip":["geoip:in-ir"]}`, "direct")
	addRule(t, s, "ssh", `{"port":"22"}`, "direct")
	on(t, s)

	// The new files, staged elsewhere: they lack in-ir.
	staged := t.TempDir()
	writeGeo(t, staged, []string{"ir"}, []string{"google"})
	writeGeo(t, geoDir, []string{"ir", "in-ir"}, []string{"google"}) // what stays installed

	problems := s.geoPreflight(filepath.Join(staged, "geoip.dat"), filepath.Join(staged, "geosite.dat"))
	if len(problems) != 1 || problems[0].RuleTag != "iran-direct" {
		t.Fatalf("pre-flight = %+v", problems)
	}
	if problems := s.geoPreflight(filepath.Join(geoDir, "geoip.dat"), filepath.Join(geoDir, "geosite.dat")); len(problems) != 0 {
		t.Fatalf("the installed data builds every rule, pre-flight said %+v", problems)
	}

	if err := s.disableForGeo(ctx, problems); err != nil {
		t.Fatal(err)
	}
	if !s.xray.Running() {
		t.Fatal("xray should keep running with the other rules")
	}
	rules, _ := s.ListRoutingRules(ctx, &pb.ListRoutingRulesRequest{})
	if len(rules.Rules) != 2 || !rules.Rules[0].Disabled || rules.Rules[0].DisabledReason != "the updated geo data lacks geoip:in-ir" || rules.Rules[1].Disabled {
		t.Fatalf("rules = %+v", rules.Rules)
	}
	saved, err := store.Load(s.dir)
	if err != nil || !saved.Rules[0].Disabled {
		t.Fatalf("the choice must be saved: %+v, %v", saved.Rules, err)
	}
}
