package main

import (
	"strings"
	"testing"
)

func TestHumanize(t *testing.T) {
	for code, want := range map[string]string{
		"category-ai-chat-!cn": "AI chat (outside China)",
		"category-ai-cn":       "AI (🇨🇳 China)",
		"category-bank-ir":     "Bank (🇮🇷 Iran)",
		"category-news-ir":     "News (🇮🇷 Iran)",
		"category-ru":          "🇷🇺 Russian websites",
		"category-tm":          "🇹🇲 Turkmen websites",
		"category-scholar-uk":  "Scholar (🇬🇧 United Kingdom)",
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
	for code, want := range map[string]string{
		"de": "🇩🇪 Germany", "ir": "🇮🇷 Iran", "pk": "🇵🇰 Pakistan", "tm": "🇹🇲 Turkmenistan",
		"uk": "🇬🇧 United Kingdom", "private": "Private networks (LAN)", "google": "Google",
	} {
		if got := humanizeIP(code); got != want {
			t.Errorf("humanizeIP(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestCountrySearch(t *testing.T) {
	for value, words := range map[string][]string{
		"geosite:category-ru":      {"Russia", "Russian"},
		"geoip:ru":                 {"Russia", "Russian"},
		"geosite:category-tm":      {"Turkmenistan", "Turkmen"},
		"geoip:pk":                 {"Pakistan", "Pakistani"},
		"geoip:gb":                 {"United Kingdom", "British", "uk", "britain"},
		"geoip:ae":                 {"United Arab Emirates", "Emirati", "uae"},
		"geosite:category-bank-ru": {"Russia", "Russian"},
	} {
		got := countrySearch(value)
		for _, w := range words {
			if !strings.Contains(strings.ToLower(got), strings.ToLower(w)) {
				t.Errorf("countrySearch(%q) = %q; missing %q", value, got, w)
			}
		}
	}
	if got := countrySearch("geosite:netflix"); got != "" {
		t.Errorf("countrySearch(geosite:netflix) = %q, want empty", got)
	}
}
