package analytics

import (
	"strings"
	"testing"
)

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

// Rule, config and subscription names are the user's own words; Go wraps them in quotes.
func TestSanitizeMessageHidesQuotedNames(t *testing.T) {
	got := SanitizeMessage(`rule "my-bank-vpn": invalid port; config ` + "`Frankfurt 2`" + ` not found`)
	for _, leak := range []string{"bank", "Frankfurt"} {
		if strings.Contains(got, leak) {
			t.Errorf("SanitizeMessage leaked %q: %q", leak, got)
		}
	}
	if !strings.Contains(got, "invalid port") {
		t.Errorf("SanitizeMessage dropped the useful part: %q", got)
	}
}

func TestFingerprintGroupsSameErrorsDespiteDifferentNames(t *testing.T) {
	a := Fingerprint(`rule "one": invalid port`)
	b := Fingerprint(`rule "two": invalid port`)
	if a != b {
		t.Errorf("fingerprints differ: %s vs %s", a, b)
	}
}
