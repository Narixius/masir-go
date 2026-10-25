package main

import (
	"strings"
	"unicode"
)

// Readable names for geo categories, for the ones without a hand-picked label in
// geo.go. They're built from the code: category-ai-chat-!cn reads "AI chat (outside
// China)", google-cn "Google (China)", geoip de "🇩🇪 Germany".

var siteSpecial = map[string]string{
	"cn":                   "🇨🇳 Chinese websites",
	"private":              "Local network names",
	"category-ads-all":     "Ads and trackers",
	"geolocation-cn":       "Websites in China",
	"geolocation-!cn":      "Websites outside China",
	"gfw":                  "Blocked in China (GFW list)",
	"greatfire":            "Blocked in China (GreatFire list)",
	"china-list":           "China list",
	"apple-cn":             "Apple (reachable in China)",
	"google-cn":            "Google (reachable in China)",
	"win-update":           "Windows Update",
	"win-spy":              "Windows telemetry",
	"win-extra":            "Windows extras",
	"category-ads":         "Ads",
	"category-porn":        "Adult websites",
	"category-scholar-!cn": "Scholarly sites (outside China)",
}

// brands keep their own spelling.
var brands = map[string]string{
	"github": "GitHub", "gitlab": "GitLab", "youtube": "YouTube", "openai": "OpenAI", "tiktok": "TikTok",
	"whatsapp": "WhatsApp", "paypal": "PayPal", "linkedin": "LinkedIn", "icloud": "iCloud", "ios": "iOS",
	"macos": "macOS", "xbox": "Xbox", "playstation": "PlayStation", "onedrive": "OneDrive",
	"duckduckgo": "DuckDuckGo", "soundcloud": "SoundCloud", "wechat": "WeChat", "qq": "QQ", "bbc": "BBC",
	"cnn": "CNN", "hbo": "HBO", "nba": "NBA", "imdb": "IMDb", "deepl": "DeepL", "chatgpt": "ChatGPT",
	"anthropic": "Anthropic", "claude": "Claude", "gemini": "Gemini", "jetbrains": "JetBrains",
	"cloudfront": "CloudFront", "wikipedia": "Wikipedia", "speedtest": "Speedtest", "vk": "VK",
}

// acronyms read in capitals.
var acronyms = map[string]bool{
	"ai": true, "vpn": true, "dns": true, "doh": true, "ddns": true, "cdn": true, "ntp": true, "tld": true,
	"api": true, "ip": true, "ipfs": true, "p2p": true, "tv": true, "vr": true, "nsfw": true, "acg": true,
	"gfw": true, "aws": true, "sms": true, "pt": true, "cas": true, "mooc": true, "geo": true, "httpdns": true,
}

// humanizeSite names a geosite code.
func humanizeSite(code string) string {
	if s, ok := siteSpecial[code]; ok {
		return s
	}
	category := strings.HasPrefix(code, "category-")
	base := strings.TrimPrefix(code, "category-")
	if category {
		if _, ok := countryNames[normalizeCountryCode(base)]; ok {
			return flag(base) + " " + countryAdjective(base) + " websites"
		}
	}
	parts := strings.Split(base, "-")
	region := ""
	if n := len(parts); n > 1 {
		last := parts[n-1]
		cc := normalizeCountryCode(last)
		if name, ok := countryNames[cc]; ok {
			if strings.HasPrefix(last, "!") {
				region = "outside " + name
			} else {
				region = flag(cc) + " " + name
			}
			parts = parts[:n-1]
		}
	}
	for i, p := range parts {
		switch {
		case brands[p] != "":
			parts[i] = brands[p]
		case acronyms[p]:
			parts[i] = strings.ToUpper(p)
		case strings.HasPrefix(p, "!"):
			parts[i] = "not " + p[1:]
		case !category || i == 0: // categories read as a phrase, names in title case
			parts[i] = capitalize(p)
		}
	}
	label := strings.Join(parts, " ")
	if region != "" {
		label += " (" + region + ")"
	}
	return label
}

var ipSpecial = map[string]string{
	"private": "Private networks (LAN)", "telegram": "Telegram servers", "twitter": "X (Twitter)",
	"cloudfront": "Amazon CloudFront", "tor": "Tor relays",
}

// humanizeIP names a geoip code: a country by flag and name, or a list.
func humanizeIP(code string) string {
	if s, ok := ipSpecial[code]; ok {
		return s
	}
	code = normalizeCountryCode(code)
	if name, ok := countryNames[code]; ok {
		return flag(code) + " " + name
	}
	return humanizeSite(code)
}

// flag is a country's flag emoji, from its two-letter code.
func flag(code string) string {
	code = normalizeCountryCode(code)
	if len(code) != 2 {
		return ""
	}
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if r < 'A' || r > 'Z' {
			return ""
		}
		b.WriteRune(0x1F1E6 + unicode.ToUpper(r) - 'A')
	}
	return b.String()
}
