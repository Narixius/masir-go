package analytics

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/posthog/posthog-go"

	coreanalytics "github.com/narixius/masir-go/libs/masir-core/analytics"
)

type fakeSink struct {
	events []posthog.Capture
	closed bool
}

func (f *fakeSink) Enqueue(m posthog.Message) error {
	if c, ok := m.(posthog.Capture); ok {
		f.events = append(f.events, c)
	}
	return nil
}

func (f *fakeSink) Close() error { f.closed = true; return nil }

func TestErrorKind(t *testing.T) {
	for err, want := range map[string]string{
		"open /x: permission denied":               "permission_denied",
		"listen tcp :7890: address already in use": "port_in_use",
		"dial tcp: i/o timeout":                    "timeout",
		"lookup x: no such host":                   "dns",
		"something ordinary":                       "other",
	} {
		if got := ErrorKind(errString(err)); got != want {
			t.Errorf("ErrorKind(%q) = %q, want %q", err, got, want)
		}
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestInstallIDPersists(t *testing.T) {
	dir := t.TempDir()
	a, err := installID(dir)
	if err != nil || a == "" {
		t.Fatal(a, err)
	}
	b, err := installID(dir)
	if err != nil || b != a {
		t.Fatalf("install ID changed: %q -> %q (%v)", a, b, err)
	}
	if _, err := os.Stat(filepath.Join(dir, identityFile)); err != nil {
		t.Fatal(err)
	}
}

func TestTrackSetsPersonless(t *testing.T) {
	fake := &fakeSink{}
	c := &Client{installID: "install", enabled: true, api: fake}
	c.Track("cli_panic", map[string]any{"command": "status"})
	if len(fake.events) != 1 {
		t.Fatalf("events = %d", len(fake.events))
	}
	e := fake.events[0]
	if e.Event != "cli_panic" || e.DistinctId != "install" {
		t.Fatalf("event = %+v", e)
	}
	if e.Properties["$process_person_profile"] != false {
		t.Fatalf("person profile opt-out missing: %+v", e.Properties)
	}
}

func TestDrainSpool(t *testing.T) {
	dir := t.TempDir()
	if err := coreanalytics.WriteSpool(dir, coreanalytics.Event{
		Event:      "cli_panic",
		Properties: map[string]any{"command": "status"},
		CreatedAt:  time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeSink{}
	c := &Client{dir: dir, installID: "install", enabled: true, api: fake}
	c.DrainSpool(t.Context())
	if len(fake.events) != 1 || fake.events[0].Event != "cli_panic" {
		t.Fatalf("events = %+v", fake.events)
	}
	entries, _ := os.ReadDir(coreanalytics.SpoolDir(dir))
	if len(entries) != 0 {
		t.Fatalf("spool not drained: %d files", len(entries))
	}
}

// withFakeSink makes New use fake instead of PostHog, and gives it a project key.
func withFakeSink(t *testing.T, fake *fakeSink) {
	t.Helper()
	old := newPostHog
	newPostHog = func(string, posthog.Config) (enqueuer, error) { return fake, nil }
	t.Cleanup(func() { newPostHog = old })
	t.Setenv("MASIR_POSTHOG_KEY", "phc_test")
	t.Setenv("MASIR_ANALYTICS", "on")
	t.Setenv("DO_NOT_TRACK", "")
}

func TestNothingIsSentBeforeTheNoticeIsShown(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeSink{}
	withFakeSink(t, fake)
	c := New(Options{Dir: dir, Version: "1.0.0", Enabled: true})

	c.Track("daemon_started", nil)
	if c.Enabled() || len(fake.events) != 0 {
		t.Fatalf("sent before the notice: enabled=%v events=%d", c.Enabled(), len(fake.events))
	}

	if err := coreanalytics.MarkNoticeShown(dir); err != nil {
		t.Fatal(err)
	}
	c.Track("daemon_started", nil)
	if !c.Enabled() || len(fake.events) != 1 {
		t.Fatalf("not sent after the notice: enabled=%v events=%d", c.Enabled(), len(fake.events))
	}
}

func TestOptOutWinsAndForgetsTheInstallID(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeSink{}
	withFakeSink(t, fake)
	if err := coreanalytics.MarkNoticeShown(dir); err != nil {
		t.Fatal(err)
	}
	c := New(Options{Dir: dir, Version: "1.0.0", Enabled: true})
	first := c.installID
	c.SetEnabled(false)
	c.Track("cli_panic", nil)
	if len(fake.events) != 0 {
		t.Fatalf("tracked after opt-out: %d events", len(fake.events))
	}
	if _, err := os.Stat(filepath.Join(dir, identityFile)); !os.IsNotExist(err) {
		t.Fatalf("install ID kept after opt-out: %v", err)
	}
	c.SetEnabled(true)
	if c.installID == "" || c.installID == first {
		t.Fatalf("install ID after opting back in = %q, was %q", c.installID, first)
	}
}

func spoolEvent(t *testing.T, dir string, e coreanalytics.Event) {
	t.Helper()
	if err := coreanalytics.WriteSpool(dir, e); err != nil {
		t.Fatal(err)
	}
}

func TestDrainSpoolDropsWhatTheRPCWouldRefuse(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	spoolEvent(t, dir, coreanalytics.Event{Event: "cli_panic", Properties: map[string]any{"command": "status", "$set": "x", "$ip": "1.2.3.4"}, CreatedAt: now})
	spoolEvent(t, dir, coreanalytics.Event{Event: "daemon_stopped", Properties: map[string]any{"forged": true}, CreatedAt: now})
	spoolEvent(t, dir, coreanalytics.Event{Event: "cli_panic", CreatedAt: now.Add(-8 * 24 * time.Hour)})

	fake := &fakeSink{}
	c := &Client{dir: dir, installID: "install", enabled: true, api: fake}
	c.DrainSpool(t.Context())

	if len(fake.events) != 1 {
		t.Fatalf("sent %d events, want only the valid one: %+v", len(fake.events), fake.events)
	}
	props := fake.events[0].Properties
	if props["command"] != "status" || props["source"] != "cli_spool" {
		t.Errorf("props = %+v", props)
	}
	for k := range props {
		if len(k) > 0 && k[0] == '$' && k != "$process_person_profile" {
			t.Errorf("reserved property %q got through", k)
		}
	}
	if entries, _ := os.ReadDir(coreanalytics.SpoolDir(dir)); len(entries) != 0 {
		t.Errorf("%d spool files left", len(entries))
	}
}

func TestDrainSpoolSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	spoolDir := coreanalytics.SpoolDir(dir)
	if err := os.MkdirAll(spoolDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "other.json")
	if err := os.WriteFile(target, []byte(`{"event":"cli_panic","properties":{"leak":"yes"},"created_at":"`+time.Now().Format(time.RFC3339)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(spoolDir, "a.json")); err != nil {
		t.Skip(err)
	}
	fake := &fakeSink{}
	c := &Client{dir: dir, installID: "install", enabled: true, api: fake}
	c.DrainSpool(t.Context())
	if len(fake.events) != 0 {
		t.Fatalf("followed a symlink out of the spool: %+v", fake.events)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("the symlink's target was removed: %v", err)
	}
}

func TestTurningAnalyticsOnCountsAsConsent(t *testing.T) {
	dir := t.TempDir()
	fake := &fakeSink{}
	withFakeSink(t, fake)
	c := New(Options{Dir: dir, Version: "1.0.0", Enabled: false})
	c.SetEnabled(true) // `masir settings set analytics on`, with no notice ever shown
	c.Track("analytics_enabled", nil)
	if !c.Enabled() || len(fake.events) != 1 {
		t.Fatalf("enabled=%v events=%d", c.Enabled(), len(fake.events))
	}
}
