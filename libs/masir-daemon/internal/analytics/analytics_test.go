package analytics

import (
	"os"
	"path/filepath"
	"strings"
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

func TestCountryFromName(t *testing.T) {
	for name, want := range map[string]string{
		"🇬🇧 England":        "GB",
		"Fast 🇩🇪 Germany":   "DE",
		"🇺🇰 London":         "GB",
		"no flag here":      "",
		"🇷🇺 Russia 1":       "RU",
		"🇹🇲 Turkmenistan 2": "TM",
	} {
		if got := CountryFromName(name); got != want {
			t.Errorf("CountryFromName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestSanitizeMessage(t *testing.T) {
	in := `failed https://example.com/path?token=abc at 1.2.3.4 /Users/me/config.json ` +
		`for user@example.com id 123e4567-e89b-12d3-a456-426614174000 token ` + strings.Repeat("a", 40)
	got := SanitizeMessage(in)
	for _, leak := range []string{"example.com", "1.2.3.4", "/Users/me", "user@example.com", "123e4567", strings.Repeat("a", 40)} {
		if strings.Contains(got, leak) {
			t.Errorf("SanitizeMessage leaked %q: %q", leak, got)
		}
	}
	if !strings.Contains(got, "<url>") || !strings.Contains(got, "<ip>") {
		t.Errorf("SanitizeMessage = %q", got)
	}
	if len(got) > 201 {
		t.Errorf("message too long: %d", len(got))
	}
}

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

func TestProviderClassification(t *testing.T) {
	for raw, want := range map[string]string{
		"https://1.1.1.1/dns-query":              "cloudflare",
		"https://dns.google/dns-query":           "google",
		"https://dns.quad9.net/dns-query":        "quad9",
		"https://dns.nextdns.io/abc123":          "nextdns",
		"https://resolver.example.com/dns-query": "custom",
	} {
		if got := DoHProvider(raw); got != want {
			t.Errorf("DoHProvider(%q) = %q, want %q", raw, got, want)
		}
	}
	if got := DNSProviders([]string{"1.1.1.1", "8.8.8.8", "192.0.2.1"}); strings.Join(got, ",") != "cloudflare,custom,google" {
		t.Errorf("DNSProviders = %v", got)
	}
}

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
	c.Track("cli_command", map[string]any{"command": "status"})
	if len(fake.events) != 1 {
		t.Fatalf("events = %d", len(fake.events))
	}
	e := fake.events[0]
	if e.Event != "cli_command" || e.DistinctId != "install" {
		t.Fatalf("event = %+v", e)
	}
	if e.Properties["$process_person_profile"] != false {
		t.Fatalf("person profile opt-out missing: %+v", e.Properties)
	}
}

func TestDrainSpool(t *testing.T) {
	dir := t.TempDir()
	if err := coreanalytics.WriteSpool(dir, coreanalytics.Event{
		Event:      "cli_command",
		Properties: map[string]any{"command": "status"},
		CreatedAt:  time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	fake := &fakeSink{}
	c := &Client{dir: dir, installID: "install", enabled: true, api: fake}
	c.DrainSpool(t.Context())
	if len(fake.events) != 1 || fake.events[0].Event != "cli_command" {
		t.Fatalf("events = %+v", fake.events)
	}
	entries, _ := os.ReadDir(coreanalytics.SpoolDir(dir))
	if len(entries) != 0 {
		t.Fatalf("spool not drained: %d files", len(entries))
	}
}
