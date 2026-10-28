package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// openSettings clicks into the settings page and waits for it to slide in.
func openSettings(t *testing.T, a *app) *ui.Tester {
	t.Helper()
	tt := tester(a)
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	return tt
}

// clickInSettings scrolls the settings page until s is on screen, then clicks it. The
// page is taller than the window, so a field near the bottom has to be scrolled to.
func clickInSettings(t *testing.T, tt *ui.Tester, s string) {
	t.Helper()
	for i := 0; i < 24; i++ {
		// The label's rect is where its text starts and has no size, so aim for the
		// middle of the window rather than its edges, where the click could be clipped.
		if r, ok := tt.Find(s); ok && r.Y >= settingsHeight/6 && r.Y <= settingsHeight*2/3 {
			if err := tt.Click(s); err != nil {
				t.Fatal(err)
			}
			tt.Advance(300 * time.Millisecond)
			return
		}
		tt.Scroll(panelWidth/2, settingsHeight/2, 0, 100)
		tt.Advance(200 * time.Millisecond)
	}
	t.Fatalf("could not scroll %q into the window", s)
}

// Settings holds only settings: the daemon's fields, no subscription list.
func TestSettingsShowsTheSettingsFields(t *testing.T) {
	a := newTestApp(ukServers())
	tt := openSettings(t, a)
	for _, want := range []string{
		"ADVANCED", "Always-on Proxy Port", "Port Number", "Share on the Network", "Open at Login",
		"Refresh Subscriptions",
		"DNS & GATEWAY", "TUN Gateway", "DNS over HTTPS", "Bootstrap DNS", "TUN DNS",
		"LOGGING", "Xray Log Level", "Follow the Log", logFollowCommand, "Xray Config", "Open",
		"PRIVACY", "Anonymous Analytics",
		"SYSTEM", "Restore Defaults",
	} {
		if !tt.HasText(want) {
			t.Errorf("no %q in %q", want, tt.Texts())
		}
	}
	if tt.HasText("outbounds") || tt.HasText("xrouter") || tt.HasText("England") {
		t.Errorf("settings still lists subscriptions: %q", tt.Texts())
	}
}

// Choosing a log level from the drop-down saves it.
func TestLogLevelSelectPicks(t *testing.T) {
	f := ukServers()
	a := newTestApp(f)
	tt := openSettings(t, a)
	clickInSettings(t, tt, "Warning") // the drop-down's trigger shows the current level
	tt.Advance(100 * time.Millisecond)
	if err := tt.Click("Debug"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(100 * time.Millisecond)
	if !f.called("log level debug") {
		t.Fatalf("the level wasn't saved: %v", f.calls)
	}
	if a.logLevel() != "debug" {
		t.Fatalf("shown level = %q, want debug", a.logLevel())
	}
}

// The port is a plain field that only takes digits, and a value out of range goes back.
func TestPortFieldTakesANumber(t *testing.T) {
	f := ukServers()
	a := newTestApp(f)
	if a.portText != "7890" {
		t.Fatalf("the port field holds %q, want 7890", a.portText)
	}

	a.portText, a.portEdited = "7891", true
	a.applyPort()
	if !f.called("port 7891") {
		t.Fatalf("the port wasn't saved: %v", f.calls)
	}

	a.portText, a.portEdited = "80", true
	a.applyPort()
	if f.count("port 80") != 0 {
		t.Fatal("a port below 1024 was sent")
	}
	if a.portText != "7890" {
		t.Fatalf("the field shows %q, want the daemon's port back", a.portText)
	}
}

// The port field drops anything that isn't a digit, and stops at five.
func TestPortFieldKeepsOnlyDigits(t *testing.T) {
	for in, want := range map[string]string{
		"7890": "7890", "78a90": "7890", "abc": "", "12 34": "1234", "1234567": "12345",
	} {
		if got := digitsOnly(in); got != want {
			t.Errorf("digitsOnly(%q) = %q, want %q", in, got, want)
		}
	}
}

// The log row shows the command, copies it, and can open it in a terminal.
func TestLogCommandCopiesAndOpensTerminal(t *testing.T) {
	a := newTestApp(ukServers())
	opened := ""
	openTerminalIn = func(cmd string) error {
		opened = cmd
		return nil
	}
	t.Cleanup(func() { openTerminalIn = openTerminalCommand })

	tt := openSettings(t, a)
	clickInSettings(t, tt, "Copy the log command")
	if got := tt.Clipboard(); got != logFollowCommand {
		t.Fatalf("clipboard = %q, want %q", got, logFollowCommand)
	}
	clickInSettings(t, tt, "Open in Terminal")
	if opened != logFollowCommand {
		t.Fatalf("terminal opened with %q, want %q", opened, logFollowCommand)
	}
}

// Choosing a DNS-over-HTTPS resolver saves its URL.
func TestDoHSelectPicks(t *testing.T) {
	f := ukServers()
	f.status.Settings.DohDns = "https://1.1.1.1/dns-query"
	a := newTestApp(f)
	tt := openSettings(t, a)
	clickInSettings(t, tt, "https://1.1.1.1/dns-query")
	tt.Advance(100 * time.Millisecond)
	if err := tt.Click("https://dns.quad9.net/dns-query"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(100 * time.Millisecond)
	if !f.called("doh https://dns.quad9.net/dns-query") {
		t.Fatalf("the resolver wasn't saved: %v", f.calls)
	}
}

// A resolver set from the command line stays in the list.
func TestDoHChoicesKeepACustomValue(t *testing.T) {
	if got := dohChoices("https://1.1.1.1/dns-query"); len(got) != 4 || got[0].label != "https://1.1.1.1/dns-query" {
		t.Fatalf("default choices = %+v", got)
	}
	custom := dohChoices("https://dns.example/dns-query")
	if custom[0].value != "https://dns.example/dns-query" || custom[0].label != custom[0].value {
		t.Fatalf("custom choices = %+v", custom)
	}
}

// The gateway choices are the ones v1 offered, and a value set from the command line is
// kept as an option so opening the row doesn't lose it.
func TestGatewayChoicesKeepACustomValue(t *testing.T) {
	got := gatewayChoices("10.10.20.1/24")
	if len(got) != 6 || got[0].value != "10.10.20.1/24" {
		t.Fatalf("default choices = %+v", got)
	}
	custom := gatewayChoices("10.55.0.1/24")
	if custom[0].value != "10.55.0.1/24" {
		t.Fatalf("custom choices = %+v", custom)
	}
	for _, c := range custom[1:] {
		if c.value == "10.55.0.1/24" {
			t.Fatal("the custom value is listed twice")
		}
	}
}

func TestIntervalChoicesKeepACustomValue(t *testing.T) {
	if got := intervalChoiceValue(intervalChoices(720), "720"); got != "Every 12 hours" {
		t.Fatalf("720 = %q", got)
	}
	custom := intervalChoices(90)
	if custom[0].value != "90" || custom[0].label != "Every 90 minutes" {
		t.Fatalf("custom choices = %+v", custom)
	}
	if intervalLabel(2880) != "Every 2 day(s)" {
		t.Fatalf("2880 = %q", intervalLabel(2880))
	}
}

// intervalChoiceValue is the label of the option with value; the empty string when none.
func intervalChoiceValue(opts []choice, value string) string {
	for _, o := range opts {
		if o.value == value {
			return o.label
		}
	}
	return ""
}

// Each choice row saves the field behind it.
func TestSettingChoicesSave(t *testing.T) {
	f := ukServers()
	a := newTestApp(f)

	a.setGateway("10.10.10.1/24")
	if !f.called("gateway 10.10.10.1/24") || a.gateway() != "10.10.10.1/24" {
		t.Fatalf("gateway: calls=%v shown=%q", f.calls, a.gateway())
	}

	a.setSubInterval(60)
	if !f.called("interval 60") || a.subInterval() != 60 {
		t.Fatalf("interval: calls=%v shown=%d", f.calls, a.subInterval())
	}

	a.setAnalytics(false)
	if !f.called("analytics off") || a.analyticsOn() {
		t.Fatalf("analytics: calls=%v shown=%v", f.calls, a.analyticsOn())
	}
}

// The text fields save what was typed, and a value the daemon couldn't accept goes back.
func TestSettingsFieldsSaveAndRecover(t *testing.T) {
	f := ukServers()
	f.status.Settings.DnsServers = []string{"1.1.1.1", "8.8.8.8"}
	a := newTestApp(f)
	a.syncTextSettings(f.status.Settings)

	a.saveBootstrapDNS("nope")
	if f.called("dns nope") {
		t.Fatal("a non-IP was sent")
	}
	if a.dns.text != "1.1.1.1, 8.8.8.8" {
		t.Fatalf("dns text = %q", a.dns.text)
	}

	a.saveBootstrapDNS("9.9.9.9, 1.1.1.1")
	if !f.called("dns 9.9.9.9,1.1.1.1") {
		t.Fatalf("calls = %v", f.calls)
	}

	f.status.Settings.TunDns = []string{"9.9.9.9"}
	a.syncTextSettings(f.status.Settings)
	a.saveTunDNS("")
	if !f.called("tun dns ") {
		t.Fatalf("clearing the tunnel DNS wasn't sent: %v", f.calls)
	}
}

// Restore Defaults puts the daemon's defaults back, and leaves power and the privacy
// choice alone.
func TestRestoreDefaultsKeepsPowerAndPrivacy(t *testing.T) {
	f := ukServers()
	f.status.Settings.Analytics = false
	f.status.Settings.ProxyPort = 9999
	a := newTestApp(f)

	a.restoreDefaults()
	for _, want := range []string{
		"always on off", "lan off", "port 7890",
		"doh https://1.1.1.1/dns-query", "dns 1.1.1.1,8.8.8.8", "tun dns ",
		"gateway 10.10.20.1/24", "interval 720", "log level warning",
	} {
		if !f.called(want) {
			t.Errorf("no %q in %v", want, f.calls)
		}
	}
	if f.called("analytics on") || f.called("analytics off") {
		t.Errorf("restore changed the privacy choice: %v", f.calls)
	}
	if f.called("power off") || f.called("tun") {
		t.Errorf("restore touched the connection: %v", f.calls)
	}
}

// Open Xray Config writes the daemon's config, indented, and opens it.
func TestOpenXrayConfigWritesAndOpens(t *testing.T) {
	f := ukServers()
	f.xrayConfig = `{"log":{"loglevel":"warning"},"inbounds":[]}`
	a := newTestApp(f)
	opened := ""
	openFileIn = func(path string) error {
		opened = path
		return nil
	}
	t.Cleanup(func() { openFileIn = openFile })
	t.Cleanup(func() {
		if opened != "" {
			_ = os.Remove(opened)
		}
	})

	a.openXrayConfig()
	if !f.called("get config") {
		t.Fatalf("the config was never asked for: %v", f.calls)
	}
	if opened == "" {
		t.Fatal("the config was not opened")
	}
	b, err := os.ReadFile(opened)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "\"loglevel\": \"warning\"") {
		t.Fatalf("the file isn't indented JSON: %q", b)
	}
}

// Without a running Xray there is no config, and the person is told.
func TestOpenXrayConfigSaysWhenThereIsNone(t *testing.T) {
	a := newTestApp(ukServers())
	opened := false
	openFileIn = func(string) error {
		opened = true
		return nil
	}
	t.Cleanup(func() { openFileIn = openFile })
	a.openXrayConfig()
	if opened {
		t.Fatal("an empty config was opened")
	}
	if len(a.notices) == 0 || !strings.Contains(a.notices[0], "no config") {
		t.Fatalf("notices = %v", a.notices)
	}
}

// The analytics notice is marked once, the first time settings shows it, so the daemon
// may send anything at all.
func TestAnalyticsNoticeIsMarkedOnce(t *testing.T) {
	a := newTestApp(ukServers())
	calls := 0
	markNoticeShown = func() { calls++ }
	t.Cleanup(func() { markNoticeShown = func() {} })
	tt := openSettings(t, a)
	if calls != 1 {
		t.Fatalf("notice marked %d times, want 1", calls)
	}
	if err := tt.Click("Back"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if calls != 1 {
		t.Fatalf("notice marked %d times after reopening, want 1", calls)
	}
}
