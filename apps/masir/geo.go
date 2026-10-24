package main

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
)

type geoEntry struct{ value, label string }

// Categories in the geo data masir installs from XTLS/Xray-rules-dat: geosite is the
// v2fly domain list; geoip is the lite file, which has only the regional sets below
// plus service and block lists (no other countries). Anything else can still be typed.
var (
	siteCountries = []geoEntry{
		{"geosite:category-ir", "🇮🇷 Iranian websites"},
		{"geosite:cn", "🇨🇳 Chinese websites"},
	}
	siteServices = []geoEntry{
		{"geosite:google", "Google"}, {"geosite:youtube", "YouTube"}, {"geosite:netflix", "Netflix"},
		{"geosite:telegram", "Telegram"}, {"geosite:twitter", "X (Twitter)"}, {"geosite:facebook", "Facebook"},
		{"geosite:instagram", "Instagram"}, {"geosite:whatsapp", "WhatsApp"}, {"geosite:openai", "OpenAI / ChatGPT"},
		{"geosite:github", "GitHub"}, {"geosite:apple", "Apple"}, {"geosite:microsoft", "Microsoft"},
		{"geosite:spotify", "Spotify"}, {"geosite:discord", "Discord"}, {"geosite:amazon", "Amazon"},
		{"geosite:cloudflare", "Cloudflare"}, {"geosite:tiktok", "TikTok"},
		{"geosite:category-ads-all", "Ads and trackers"}, {"geosite:private", "Local network names"},
	}
	ipCountries = []geoEntry{
		{"geoip:ir", "🇮🇷 Iran"}, {"geoip:cn", "🇨🇳 China"}, {"geoip:ru", "🇷🇺 Russia"},
	}
	ipOther = []geoEntry{
		{"geoip:private", "Private networks (LAN)"},
		{"geoip:blacklist-ir", "Addresses blocked in Iran"}, {"geoip:blacklist-ru", "Addresses blocked in Russia"},
		{"geoip:telegram", "Telegram servers"}, {"geoip:google", "Google"}, {"geoip:netflix", "Netflix"},
		{"geoip:twitter", "X (Twitter)"}, {"geoip:facebook", "Facebook"}, {"geoip:cloudflare", "Cloudflare"},
		{"geoip:cloudfront", "Amazon CloudFront"}, {"geoip:fastly", "Fastly"}, {"geoip:tor", "Tor relays"},
	}
)

// friendly names a matcher value for display: a catalog label, an app's name, or itself.
func friendly(v string) string {
	for _, list := range [][]geoEntry{siteCountries, siteServices, ipCountries, ipOther} {
		for _, e := range list {
			if e.value == v {
				return e.label
			}
		}
	}
	if strings.HasSuffix(v, ".app/") {
		return strings.TrimSuffix(filepath.Base(strings.TrimSuffix(v, "/")), ".app") + ui.Dim(" (app)")
	}
	return v
}

func geoGroup(label string, entries []geoEntry) ui.PickGroup {
	g := ui.PickGroup{Label: label}
	for _, e := range entries {
		g.Items = append(g.Items, ui.PickItem{Value: e.value, Label: e.label, Hint: ui.Dim(e.value), Search: e.value})
	}
	return g
}

var domainRe = regexp.MustCompile(`^(\*\.)?([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z0-9-]{2,}$`)

func validateDomain(v string) error {
	for _, p := range []string{"geosite:", "domain:", "full:", "keyword:", "regexp:", "ext:"} {
		if strings.HasPrefix(v, p) && len(v) > len(p) {
			return nil
		}
	}
	if domainRe.MatchString(strings.ToLower(v)) {
		return nil
	}
	return fail("not a domain (or geosite:, domain:, full:, keyword:, regexp:)")
}

func validateIP(v string) error {
	if strings.HasPrefix(v, "geoip:") || strings.HasPrefix(v, "ext:") {
		return nil
	}
	if net.ParseIP(v) != nil {
		return nil
	}
	if _, _, err := net.ParseCIDR(v); err == nil {
		return nil
	}
	return fail("not an IP, CIDR or geoip:")
}

func validatePorts(v string) error {
	for _, part := range strings.Split(v, ",") {
		lo, hi, isRange := strings.Cut(strings.TrimSpace(part), "-")
		for _, p := range []string{lo, hi} {
			if p == "" && !isRange {
				continue
			}
			n, err := strconv.Atoi(p)
			if err != nil || n < 1 || n > 65535 {
				return fail("ports are 1-65535, like 443 or 1000-2000, separated by commas")
			}
		}
	}
	return nil
}

func validateIPs(v string) error {
	for _, part := range strings.Split(v, ",") {
		if err := validateIP(strings.TrimSpace(part)); err != nil {
			return err
		}
	}
	return nil
}

// appGroups lists installed apps (macOS: a folder match on the bundle covers its helper
// processes too) and running processes.
func appGroups() []ui.PickGroup {
	var groups []ui.PickGroup
	if runtime.GOOS == "darwin" {
		home, _ := os.UserHomeDir()
		var installed []ui.PickItem
		seen := map[string]bool{}
		for _, dir := range []string{"/Applications", "/System/Applications", filepath.Join(home, "Applications")} {
			matches, _ := filepath.Glob(filepath.Join(dir, "*.app"))
			for _, m := range matches {
				name := strings.TrimSuffix(filepath.Base(m), ".app")
				if seen[name] {
					continue
				}
				seen[name] = true
				installed = append(installed, ui.PickItem{Value: m + "/", Label: name, Hint: ui.Dim("app and its helpers"), Search: m})
			}
		}
		sort.Slice(installed, func(i, j int) bool { return strings.ToLower(installed[i].Label) < strings.ToLower(installed[j].Label) })
		groups = append(groups, ui.PickGroup{Label: "Installed", Items: installed})
	}
	var running []ui.PickItem
	for _, name := range runningProcesses() {
		running = append(running, ui.PickItem{Value: name, Label: name, Hint: ui.Dim("process")})
	}
	groups = append(groups, ui.PickGroup{Label: "Running", Items: running})
	return groups
}

func runningProcesses() []string {
	args := []string{"-eo", "comm="}
	if runtime.GOOS == "darwin" {
		args = []string{"-axco", "comm="}
	}
	out, err := exec.Command("ps", args...).Output()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var names []string
	for _, l := range strings.Split(string(out), "\n") {
		n := strings.TrimSpace(l)
		if n == "" || seen[n] || strings.HasPrefix(n, "(") {
			continue
		}
		seen[n] = true
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return strings.ToLower(names[i]) < strings.ToLower(names[j]) })
	return names
}
