package main

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"

	"github.com/narixius/masir-go/libs/masir-core/service"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// fakeDaemon answers the calls the app makes, from a status and lists the test sets.
type fakeDaemon struct {
	pb.DaemonServiceClient // anything else panics: the test must not need it

	mu                                   sync.Mutex
	status                               *pb.Status
	subs                                 []*pb.Subscription
	configs                              map[string][]*pb.SubscriptionConfig
	rules                                []*pb.RoutingRule
	calls                                []string
	failPow                              error
	failFallback, failRemove, failEnable error
	release                              *pb.ReleaseInfo
	releaseErr                           error
	updateJob                            *pb.UpdateJob
	updateStream                         *fakeUpdateStream
	restartVersion                       string
	checkAge                             time.Duration // the last check's max age
}

func (f *fakeDaemon) log(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

// count reports how many calls like s arrived.
func (f *fakeDaemon) count(s string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == s {
			n++
		}
	}
	return n
}

// ageAsked is the max age the last release check asked for.
func (f *fakeDaemon) ageAsked() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.checkAge
}

func (f *fakeDaemon) called(s string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == s {
			return true
		}
	}
	return false
}

func (f *fakeDaemon) ListSubscriptions(context.Context, *pb.ListSubscriptionsRequest, ...grpc.CallOption) (*pb.ListSubscriptionsResponse, error) {
	return &pb.ListSubscriptionsResponse{Subscriptions: f.subs}, nil
}
func (f *fakeDaemon) GetSubscription(_ context.Context, r *pb.GetSubscriptionRequest, _ ...grpc.CallOption) (*pb.GetSubscriptionResponse, error) {
	return &pb.GetSubscriptionResponse{Configs: f.configs[r.Id]}, nil
}
func (f *fakeDaemon) ListRoutingRules(context.Context, *pb.ListRoutingRulesRequest, ...grpc.CallOption) (*pb.ListRoutingRulesResponse, error) {
	return &pb.ListRoutingRulesResponse{Rules: f.rules}, nil
}
func (f *fakeDaemon) SetPower(_ context.Context, r *pb.SetPowerRequest, _ ...grpc.CallOption) (*pb.SetPowerResponse, error) {
	if r.On {
		f.log("power on")
	} else {
		f.log("power off")
	}
	return &pb.SetPowerResponse{}, f.failPow
}
func (f *fakeDaemon) SetTunEnabled(_ context.Context, r *pb.SetTunEnabledRequest, _ ...grpc.CallOption) (*pb.SetTunEnabledResponse, error) {
	f.log("tun")
	return &pb.SetTunEnabledResponse{}, nil
}
func (f *fakeDaemon) SetFallback(_ context.Context, r *pb.SetFallbackRequest, _ ...grpc.CallOption) (*pb.SetFallbackResponse, error) {
	f.log("fallback " + r.Fallback.String())
	return &pb.SetFallbackResponse{}, f.failFallback
}
func (f *fakeDaemon) SelectConfig(_ context.Context, r *pb.SelectConfigRequest, _ ...grpc.CallOption) (*pb.SelectConfigResponse, error) {
	f.log("select " + r.SubscriptionId + "/" + r.ConfigId)
	return &pb.SelectConfigResponse{}, nil
}
func (f *fakeDaemon) UpdateSettings(_ context.Context, r *pb.UpdateSettingsRequest, _ ...grpc.CallOption) (*pb.UpdateSettingsResponse, error) {
	if r.RulesEnabled != nil {
		f.log("rules enabled " + map[bool]string{true: "on", false: "off"}[*r.RulesEnabled])
	}
	return &pb.UpdateSettingsResponse{}, nil
}
func (f *fakeDaemon) SetRuleEnabled(_ context.Context, r *pb.SetRuleEnabledRequest, _ ...grpc.CallOption) (*pb.SetRuleEnabledResponse, error) {
	f.log("rule " + r.RuleTags[0] + map[bool]string{true: " on", false: " off"}[r.Enabled])
	return &pb.SetRuleEnabledResponse{}, f.failEnable
}

func (f *fakeDaemon) RemoveRouting(_ context.Context, r *pb.RemoveRoutingRequest, _ ...grpc.CallOption) (*pb.RemoveRoutingResponse, error) {
	f.log("remove " + r.RuleTag)
	return &pb.RemoveRoutingResponse{}, f.failRemove
}

func (f *fakeDaemon) CheckUpdate(_ context.Context, r *pb.CheckUpdateRequest, _ ...grpc.CallOption) (*pb.CheckUpdateResponse, error) {
	f.mu.Lock()
	f.checkAge = time.Duration(r.GetMaxAgeMs()) * time.Millisecond
	f.mu.Unlock()
	if r.GetForce() {
		f.log("check update force")
	} else {
		f.log("check update")
	}
	return &pb.CheckUpdateResponse{Release: f.release}, f.releaseErr
}

func (f *fakeDaemon) IgnoreUpdate(_ context.Context, r *pb.IgnoreUpdateRequest, _ ...grpc.CallOption) (*pb.IgnoreUpdateResponse, error) {
	f.log("ignore " + r.Version)
	if f.release == nil {
		return nil, nil
	}
	rel := proto.Clone(f.release).(*pb.ReleaseInfo)
	rel.IgnoredVersion = r.Version
	f.release = rel
	return &pb.IgnoreUpdateResponse{Release: f.release}, nil
}

func (f *fakeDaemon) GetStatus(context.Context, *pb.GetStatusRequest, ...grpc.CallOption) (*pb.GetStatusResponse, error) {
	return &pb.GetStatusResponse{Status: f.status}, nil
}

func (f *fakeDaemon) Restart(context.Context, *pb.RestartRequest, ...grpc.CallOption) (*pb.RestartResponse, error) {
	f.log("restart")
	if f.restartVersion != "" {
		f.status.Version = f.restartVersion
	}
	return &pb.RestartResponse{}, nil
}

func (f *fakeDaemon) Shutdown(context.Context, *pb.ShutdownRequest, ...grpc.CallOption) (*pb.ShutdownResponse, error) {
	f.log("shutdown")
	return &pb.ShutdownResponse{}, nil
}

func (f *fakeDaemon) StartUpdate(context.Context, *pb.StartUpdateRequest, ...grpc.CallOption) (*pb.StartUpdateResponse, error) {
	f.log("start update")
	return &pb.StartUpdateResponse{Job: f.updateJob}, f.releaseErr
}

func (f *fakeDaemon) WatchUpdate(context.Context, *pb.WatchUpdateRequest, ...grpc.CallOption) (grpc.ServerStreamingClient[pb.WatchUpdateResponse], error) {
	f.log("watch update")
	return f.updateStream, nil
}

func (f *fakeDaemon) PauseUpdate(context.Context, *pb.PauseUpdateRequest, ...grpc.CallOption) (*pb.PauseUpdateResponse, error) {
	f.log("pause update")
	return &pb.PauseUpdateResponse{Job: f.updateJob}, nil
}

func (f *fakeDaemon) ResumeUpdate(context.Context, *pb.ResumeUpdateRequest, ...grpc.CallOption) (*pb.ResumeUpdateResponse, error) {
	f.log("resume update")
	return &pb.ResumeUpdateResponse{Job: f.updateJob}, nil
}

func (f *fakeDaemon) StopUpdate(context.Context, *pb.StopUpdateRequest, ...grpc.CallOption) (*pb.StopUpdateResponse, error) {
	f.log("stop update")
	return &pb.StopUpdateResponse{Job: f.updateJob}, nil
}

// fakeUpdateStream is a WatchUpdate stream that sends its jobs and then ends.
type fakeUpdateStream struct {
	jobs []*pb.UpdateJob
	at   int
	err  error
}

func (s *fakeUpdateStream) Recv() (*pb.WatchUpdateResponse, error) {
	if s.at >= len(s.jobs) {
		if s.err != nil {
			return nil, s.err
		}
		return nil, io.EOF
	}
	job := s.jobs[s.at]
	s.at++
	return &pb.WatchUpdateResponse{Job: job}, nil
}
func (s *fakeUpdateStream) Header() (metadata.MD, error) { return nil, nil }
func (s *fakeUpdateStream) Trailer() metadata.MD         { return nil }
func (s *fakeUpdateStream) CloseSend() error             { return nil }
func (s *fakeUpdateStream) Context() context.Context     { return context.Background() }
func (s *fakeUpdateStream) SendMsg(any) error            { return nil }
func (s *fakeUpdateStream) RecvMsg(any) error            { return nil }

func ukServers() *fakeDaemon {
	return &fakeDaemon{
		status: &pb.Status{
			Version: "0.1.0", Privileged: true, Revision: 1,
			Settings:             &pb.Settings{Fallback: pb.Fallback_FALLBACK_PROXY, RulesEnabled: true, ProxyPort: 7890},
			ActiveSubscriptionId: "s1", ActiveConfigId: "c1", ActiveConfigName: "🇬🇧 England",
		},
		subs: []*pb.Subscription{{Id: "s1", Name: "xrouter", ConfigCount: 2}},
		configs: map[string][]*pb.SubscriptionConfig{"s1": {
			{Id: "c1", Name: "🇬🇧 England", Protocol: "vless", Latency: &pb.LatencyResult{LatencyMs: 2048, TestedAtMs: 1}},
			{Id: "c2", Name: "🇩🇪 Deutschland", Protocol: "trojan", Latency: &pb.LatencyResult{LatencyMs: 80, TestedAtMs: 1}},
		}},
		rules: []*pb.RoutingRule{
			{RuleTag: "r1", RuleJson: `{"domain":["geosite:google","full:a.test"]}`, Target: "proxy"},
			{RuleTag: "r2", RuleJson: `{"domain":["full:bank.test"]}`, Target: "direct", Disabled: true},
			{RuleTag: "r3", RuleJson: `{"process":["/Applications/Spotify.app/"]}`, Target: "proxy"},
		},
		updateStream: &fakeUpdateStream{},
	}
}

// newTestApp is an app on a fake daemon, with everything inline.
func newTestApp(f *fakeDaemon) *app {
	openAtLogin = func() bool { return false } // there is no running app to ask
	setOpenAtLogin = func(bool) error { return nil }
	quit = func() {}
	readClipboard = func() string { return "" }
	readService = func() service.Info { return service.Info{} }
	a := newApp(f, "")
	a.motion.instant = true // no waiting for animations
	a.async = func(fn func()) { fn() }
	a.version = "0.1.0"
	a.applyStatus(f.status)
	return a
}

func tester(a *app) *ui.Tester { return ui.NewTester(a.view, panelWidth, 520) }

func TestMainPanelShowsTheServerAndRules(t *testing.T) {
	a := newTestApp(ukServers())
	tt := tester(a)
	for _, want := range []string{"Disconnected", "Ready to connect", "Tunnel", "Direct", "non-ruled traffic is tunneled", ltr("United Kingdom"), ltr("xrouter • England"), "2,048", "ms", "RULES", "rules", "Masir v0.1.0"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
}

func TestPowerSwitchConnects(t *testing.T) {
	f := ukServers()
	a := newTestApp(f)
	tt := tester(a)
	if err := tt.Click("Connect"); err != nil {
		t.Fatal(err)
	}
	if !f.called("power on") || !f.called("tun") {
		t.Fatalf("calls = %v", f.calls)
	}
}

func TestPowerSwitchIsOffWithoutServers(t *testing.T) {
	f := ukServers()
	f.subs, f.configs = nil, nil
	a := newTestApp(f)
	tt := tester(a)
	if !tt.HasText("No Subscriptions Added") {
		t.Fatalf("texts = %q", tt.Texts())
	}
	_ = tt.Click("Connect")
	if f.called("power on") {
		t.Fatal("connected with no server to connect to")
	}
}

func TestModeSwitchesFallback(t *testing.T) {
	f := ukServers()
	a := newTestApp(f)
	tt := tester(a)
	if err := tt.Click("Direct"); err != nil {
		t.Fatal(err)
	}
	if !f.called("fallback FALLBACK_DIRECT") {
		t.Fatalf("calls = %v", f.calls)
	}
}

func TestPickingAServer(t *testing.T) {
	f := ukServers()
	a := newTestApp(f)
	tt := tester(a)
	_ = tt.Click(ltr("United Kingdom")) // opens the list
	tt.Advance(time.Second)             // it grows into view
	if a.open != sectionServers || !tt.HasText("Germany") {
		t.Fatalf("open = %v, texts = %q", a.open, tt.Texts())
	}
	if err := tt.Click(ltr("Germany")); err != nil {
		t.Fatal(err)
	}
	if !f.called("select s1/c2") || a.open != sectionNone {
		t.Fatalf("calls = %v, open = %v", f.calls, a.open)
	}
}

func TestRulesListAndSwitch(t *testing.T) {
	f := ukServers()
	a := newTestApp(f)
	tt := tester(a)
	_ = tt.Click("RULES")
	tt.Advance(time.Second)
	if !tt.HasText("google +1") || !tt.HasText("bank.test") {
		t.Fatalf("texts = %q", tt.Texts())
	}
	_ = tt.Click("Use rules")
	if !f.called("rules enabled off") {
		t.Fatalf("calls = %v", f.calls)
	}
}

func TestFailedConnectIsToldToThePerson(t *testing.T) {
	f := ukServers()
	f.failPow = errFake
	a := newTestApp(f)
	tt := tester(a)
	_ = tt.Click("Connect")
	tt.Frame()
	if len(a.notices) != 0 { // the frame showed it
		t.Fatalf("notices left: %v", a.notices)
	}
	if a.busy != "" {
		t.Fatalf("still busy: %q", a.busy)
	}
}

func TestSettingsAndBack(t *testing.T) {
	a := newTestApp(ukServers())
	tt := tester(a)
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second) // the page slides in
	for _, want := range []string{"Settings", "xrouter", "Always-on Proxy Port", "Port Number", "Quit Masir"} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Back"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if !tt.HasText("RULES") {
		t.Fatalf("not back on the main page: %q", tt.Texts())
	}
}

// TestAddSubscriptionReturnsToSettings checks that the add form opened from settings goes
// back there, not to the main page.
func TestAddSubscriptionReturnsToSettings(t *testing.T) {
	a := newTestApp(ukServers())
	tt := tester(a)
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if err := tt.Click("Add"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if !tt.HasText("Add Subscription") {
		t.Fatalf("not on the add page: %q", tt.Texts())
	}
	if err := tt.Click("Back"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if !tt.HasText("Always-on Proxy Port") || !tt.HasText("Settings") {
		t.Fatalf("not back in settings: %q", tt.Texts())
	}
}

func TestSpeedAndDurationFormats(t *testing.T) {
	for in, want := range map[uint64]string{0: "0KB/s", 2048: "2KB/s", 3 << 20: "3.0MB/s", 50 << 20: "50MB/s"} {
		if got := speed(in); got != want {
			t.Errorf("speed(%d) = %q, want %q", in, got, want)
		}
	}
	if got := formatMs(2048); got != "2,048ms" {
		t.Errorf("formatMs = %q", got)
	}
}

func TestServerTitleUsesTheCountryOfTheFlag(t *testing.T) {
	flag, title, detail := serverTitle("🇬🇧 انگلیس")
	if flag != "🇬🇧" || title != "United Kingdom" || detail != "انگلیس" {
		t.Fatalf("got %q %q %q", flag, title, detail)
	}
	if _, title, detail := serverTitle("Frankfurt 1"); title != "Frankfurt 1" || detail != "" {
		t.Fatalf("got %q %q", title, detail)
	}
}

func TestServerSearchMatchesTheFlagCountry(t *testing.T) {
	a := newTestApp(ukServers())
	a.query = "Germany"
	got := a.filteredServers()
	if len(got) != 1 || got[0].key.cfg != "c2" {
		t.Fatalf("searching Germany found %+v", got)
	}
	a.query = "United Kingdom"
	if got := a.filteredServers(); len(got) != 1 || got[0].key.cfg != "c1" {
		t.Fatalf("searching United Kingdom found %+v", got)
	}
}

func TestClosingAListClearsItsSearch(t *testing.T) {
	a := newTestApp(ukServers())
	a.open, a.query = sectionServers, "Germany"
	a.toggle(sectionServers)
	if a.query != "" {
		t.Fatalf("the server search stayed: %q", a.query)
	}
	a.open, a.ruleQuery = sectionRules, "google"
	a.toggle(sectionRules)
	if a.ruleQuery != "" {
		t.Fatalf("the rules search stayed: %q", a.ruleQuery)
	}
}

func TestServerListKeepsItsHeightWhenSearching(t *testing.T) {
	a := newTestApp(ukServers())
	tt := tester(a)
	if err := tt.Click(ltr("United Kingdom")); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	before, ok := tt.Find("RULES")
	if !ok {
		t.Fatalf("no rules header: %q", tt.Texts())
	}
	if a.serverListH <= 0 {
		t.Fatalf("the open list was not measured: %v", a.serverListH)
	}
	tt.Type("no-such-outbound")
	tt.Frame()
	after, ok := tt.Find("RULES")
	if !ok {
		t.Fatalf("no rules header after searching: %q", tt.Texts())
	}
	if d := before.Y - after.Y; d > 1 || d < -1 {
		t.Fatalf("the list changed height while searching: rules at %v then %v", before.Y, after.Y)
	}
}

func TestRulesListKeepsItsHeightWhenSearching(t *testing.T) {
	a, _, tt := openRules(t)
	if a.ruleListH <= 0 {
		t.Fatalf("the open rules list was not measured: %v", a.ruleListH)
	}
	before, ok := tt.Find("Masir v0.1.0")
	if !ok {
		t.Fatalf("no footer: %q", tt.Texts())
	}
	tt.Type("no-such-rule")
	tt.Frame()
	after, ok := tt.Find("Masir v0.1.0")
	if !ok {
		t.Fatalf("no footer after searching: %q", tt.Texts())
	}
	if d := before.Y - after.Y; d > 1 || d < -1 {
		t.Fatalf("the rules list changed height while searching: footer at %v then %v", before.Y, after.Y)
	}
}

func TestFlagOf(t *testing.T) {
	flag, rest := flagOf("🇬🇧 United Kingdom")
	if flag != "🇬🇧" || rest != "United Kingdom" {
		t.Fatalf("got %q %q", flag, rest)
	}
	if flag, rest := flagOf("Plain"); flag != "" || rest != "Plain" {
		t.Fatalf("got %q %q", flag, rest)
	}
}

type fakeError string

func (e fakeError) Error() string { return string(e) }

const errFake = fakeError("boom")

func TestRollingNumbersKeepTheirLabelAndRoll(t *testing.T) {
	a := newTestApp(ukServers())
	tt := tester(a)
	if !tt.HasText("2,048") {
		t.Fatalf("the number isn't labelled: %q", tt.Texts())
	}
	// A new measurement: the digits that change roll, and then it reads the new number.
	k := serverKey{"s1", "c1"}
	a.latency[k] = 2075
	tt.Frame()
	tt.Advance(60 * time.Millisecond)
	if !tt.FramePending() {
		t.Fatal("the digits don't roll: no frames are asked for")
	}
	tt.Advance(time.Second)
	if !tt.HasText("2,075") || tt.FramePending() {
		t.Fatalf("did not settle on the new number: %q", tt.Texts())
	}
}

func TestLatencyRollsFromTheOldNumberAfterATest(t *testing.T) {
	a := newTestApp(ukServers())
	tt := tester(a)
	k := serverKey{"s1", "c1"}

	a.testing[k] = true // a test starts: the old number stays
	tt.Frame()
	tt.Advance(50 * time.Millisecond)
	if !tt.HasText("2,048") {
		t.Fatalf("the old number went away during the test: %q", tt.Texts())
	}

	delete(a.testing, k) // and ends with a new one
	a.latency[k] = 176
	tt.Frame()
	tt.Advance(60 * time.Millisecond)
	if !tt.FramePending() {
		t.Fatal("the result didn't roll in: nothing is moving")
	}
	tt.Advance(time.Second)
	if !tt.HasText("176") || tt.FramePending() {
		t.Fatalf("did not settle on the result: %q", tt.Texts())
	}
}

// openRules opens the rules list and lets it settle.
func openRules(t *testing.T) (*app, *fakeDaemon, *ui.Tester) {
	t.Helper()
	f := ukServers()
	a := newTestApp(f)
	tt := tester(a)
	if err := tt.Click("RULES"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	return a, f, tt
}

func TestSearchFieldsTakeTheKeyboardAsTheirListsOpen(t *testing.T) {
	a := newTestApp(ukServers())
	tt := tester(a)
	_ = tt.Click(ltr("United Kingdom"))
	tt.Advance(time.Second)
	if !tt.Focused("Search country, or remarks...") {
		t.Fatal("the server search didn't get the keyboard")
	}
	_ = tt.Click(ltr("United Kingdom")) // closes it
	tt.Advance(time.Second)
	_ = tt.Click("RULES")
	tt.Advance(time.Second)
	if !tt.Focused("Search rules...") {
		t.Fatal("the rules search didn't get the keyboard")
	}
}

func TestApplicationRulesShowTheAppsName(t *testing.T) {
	_, _, tt := openRules(t)
	if !tt.HasText(ltr("Spotify")) || !tt.HasText("APPLICATIONS (1)") {
		t.Fatalf("texts = %q", tt.Texts())
	}
}

func TestSwipingARowLeftShowsItsButtons(t *testing.T) {
	_, f, tt := openRules(t)
	if tt.HasText("Remove Rule") {
		t.Fatal("the buttons are showing on a closed row")
	}
	box, ok := tt.Find(ltr("google +1"))
	if !ok {
		t.Fatalf("no row: %q", tt.Texts())
	}
	for i := 0; i < 6; i++ { // fingers moving left
		tt.Scroll(box.X+box.W/2, box.Y+box.H/2, -24, 0)
		tt.Advance(12 * time.Millisecond)
	}
	tt.Advance(time.Second)
	if err := tt.Click("Remove Rule"); err != nil {
		t.Fatalf("the buttons didn't come in: %v", err)
	}
	if !f.called("remove r1") {
		t.Fatalf("calls = %v", f.calls)
	}
}

func TestRightClickOpensTheRuleMenu(t *testing.T) {
	a, f, tt := openRules(t)
	if err := tt.RightClick(ltr("bank.test")); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	for _, want := range []string{"Edit Rule", "Enable Rule", "Remove Rule"} { // r2 is disabled
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	if err := tt.Click("Remove Rule"); err != nil {
		t.Fatal(err)
	}
	if !f.called("remove r2") || a.menu.open {
		t.Fatalf("calls = %v, menu open = %v", f.calls, a.menu.open)
	}
}

func TestMenuClosesOnAPressOutsideIt(t *testing.T) {
	a, f, tt := openRules(t)
	_ = tt.RightClick(ltr("bank.test"))
	tt.Advance(time.Second)
	tt.ClickAt(5, 5)
	tt.Advance(time.Second)
	if a.menu.open || tt.HasText("Remove Rule") {
		t.Fatal("the menu stayed open")
	}
	if f.called("remove r2") {
		t.Fatal("the press outside chose something")
	}
}

// held makes the daemon's answers wait until the test lets them through, so what the screen
// shows in the meantime is what the person sees before the daemon has said anything.
func held(a *app) (release func()) {
	var queue []func()
	a.async = func(fn func()) { queue = append(queue, fn) }
	return func() {
		for len(queue) > 0 {
			fn := queue[0]
			queue = queue[1:]
			fn()
		}
	}
}

func TestModeChangeShowsBeforeTheDaemonAnswers(t *testing.T) {
	f := ukServers()
	a := newTestApp(f)
	release := held(a)
	tt := tester(a)
	if err := tt.Click("Direct"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Non-ruled traffic is direct") {
		t.Fatalf("the change isn't showing: %q", tt.Texts())
	}
	if f.called("fallback FALLBACK_DIRECT") {
		t.Fatal("the daemon was asked before the screen changed")
	}
	release()
	tt.Frame()
	if !f.called("fallback FALLBACK_DIRECT") || !tt.HasText("Non-ruled traffic is direct") {
		t.Fatalf("calls = %v, texts = %q", f.calls, tt.Texts())
	}
}

func TestRefusedModeChangeGoesBackAndSaysWhy(t *testing.T) {
	f := ukServers()
	f.failFallback = errFake
	a := newTestApp(f)
	release := held(a)
	tt := tester(a)
	_ = tt.Click("Direct")
	if !tt.HasText("Non-ruled traffic is direct") {
		t.Fatal("the change isn't showing first")
	}
	release()
	tt.Frame()
	if !tt.HasText("non-ruled traffic is tunneled") || tt.HasText("Non-ruled traffic is direct") {
		t.Fatalf("it didn't go back: %q", tt.Texts())
	}
	if len(a.notices) != 0 { // shown as a toast in the frame
		t.Fatalf("the toast wasn't shown: %v", a.notices)
	}
}

func TestSelectingAServerShowsItAtOnce(t *testing.T) {
	f := ukServers()
	a := newTestApp(f)
	release := held(a)
	tt := tester(a)
	_ = tt.Click(ltr("United Kingdom"))
	tt.Advance(time.Second)
	_ = tt.Click(ltr("Germany"))
	tt.Advance(time.Second)
	if !tt.HasText(ltr("Germany")) {
		t.Fatalf("the server row doesn't show the choice: %q", tt.Texts())
	}
	release()
}

func TestRuleSwitchedOffShowsAtOnceAndGoesBackIfRefused(t *testing.T) {
	f := ukServers()
	f.failEnable = errFake
	a := newTestApp(f)
	release := held(a)
	tt := tester(a)
	_ = tt.Click("RULES")
	tt.Advance(time.Second)
	_ = tt.RightClick(ltr("google +1"))
	tt.Advance(time.Second)
	_ = tt.Click("Disable Rule")
	tt.Advance(time.Second)
	if r := a.ruleByTag("r1"); r != nil && r.Disabled {
		t.Fatal("the daemon's rule was changed")
	}
	if shown := a.ruleList(); !shownDisabled(shown, "r1") {
		t.Fatal("the rule doesn't show as off")
	}
	release()
	tt.Frame()
	if shownDisabled(a.ruleList(), "r1") {
		t.Fatal("it stayed off after the daemon refused")
	}
}

func shownDisabled(rules []*pb.RoutingRule, tag string) bool {
	for _, r := range rules {
		if r.RuleTag == tag {
			return r.Disabled
		}
	}
	return false
}

func TestRemovedRuleLeavesAtOnceAndReturnsIfRefused(t *testing.T) {
	f := ukServers()
	f.failRemove = errFake
	a := newTestApp(f)
	release := held(a)
	tt := tester(a)
	_ = tt.Click("RULES")
	tt.Advance(time.Second)
	_ = tt.RightClick(ltr("bank.test"))
	tt.Advance(time.Second)
	_ = tt.Click("Remove Rule")
	tt.Advance(time.Second)
	if tt.HasText(ltr("bank.test")) || !tt.HasText("rules") {
		t.Fatalf("the rule is still listed: %q", tt.Texts())
	}
	release()
	tt.Advance(time.Second)
	if !tt.HasText(ltr("bank.test")) {
		t.Fatalf("the rule didn't come back: %q", tt.Texts())
	}
}

// A login item left by an earlier copy can make register fail. The toggle clears it and
// registers again instead of just reporting "Couldn't change it".
func TestOpenAtLoginClearsAStaleRegistration(t *testing.T) {
	a := newTestApp(ukServers())
	calls := []bool{}
	old := setOpenAtLogin
	setOpenAtLogin = func(on bool) error {
		calls = append(calls, on)
		if len(calls) == 1 && on {
			return errors.New("mygo: already registered")
		}
		return nil
	}
	t.Cleanup(func() { setOpenAtLogin = old })

	a.setLoginEnabled(true)
	if !a.loginOn || !a.loginChecked || a.loginNeedsApproval {
		t.Fatalf("on=%v checked=%v approval=%v", a.loginOn, a.loginChecked, a.loginNeedsApproval)
	}
	if len(calls) != 3 || calls[0] != true || calls[1] != false || calls[2] != true {
		t.Fatalf("calls = %v, want [true false true]", calls)
	}
}

// When macOS still needs the person to allow the item, the Settings page explains it.
func TestOpenAtLoginShowsTheApprovalHint(t *testing.T) {
	a := newTestApp(ukServers())
	old := setOpenAtLogin
	setOpenAtLogin = func(bool) error {
		return errors.New("mygo: allow Masir to open at login in System Settings > General > Login Items")
	}
	t.Cleanup(func() { setOpenAtLogin = old })

	a.setLoginEnabled(true)
	if !a.loginNeedsApproval {
		t.Fatal("the approval state was not recorded")
	}
	if a.loginOn {
		t.Fatal("a failed registration left the switch on")
	}
}
