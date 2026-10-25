package main

import "testing"

func TestHumanize(t *testing.T) {
	for code, want := range map[string]string{
		"category-ai-chat-!cn": "AI chat (outside China)",
		"category-ai-cn":       "AI (China)",
		"category-bank-ir":     "Bank (Iran)",
		"category-news-ir":     "News (Iran)",
		"category-ads-all":     "Ads and trackers",
		"google-cn":            "Google (reachable in China)",
		"github":               "GitHub",
		"tld-!cn":              "TLD (outside China)",
		"geolocation-!cn":      "Websites outside China",
		"google-play":          "Google Play",
	} {
		if got := humanizeSite(code); got != want {
			t.Errorf("humanizeSite(%q) = %q, want %q", code, got, want)
		}
	}
	for code, want := range map[string]string{"de": "🇩🇪 Germany", "ir": "🇮🇷 Iran", "private": "Private networks (LAN)", "google": "Google"} {
		if got := humanizeIP(code); got != want {
			t.Errorf("humanizeIP(%q) = %q, want %q", code, got, want)
		}
	}
}
