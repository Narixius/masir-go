package main

import (
	"context"
	"errors"
	"fmt"
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
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

type geoEntry struct{ value, label string }

// Common categories in the geo data masir installs from Loyalsoldier/v2ray-rules-dat:
// geosite is the extended v2fly domain list, geoip has every country by its two-letter
// code plus the service lists below. Anything else can still be typed.
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
		{"geoip:ir", "🇮🇷 Iran"}, {"geoip:cn", "🇨🇳 China"}, {"geoip:ru", "🇷🇺 Russia"}, {"geoip:us", "🇺🇸 United States"},
		{"geoip:de", "🇩🇪 Germany"}, {"geoip:nl", "🇳🇱 Netherlands"}, {"geoip:gb", "🇬🇧 United Kingdom"},
		{"geoip:fr", "🇫🇷 France"}, {"geoip:fi", "🇫🇮 Finland"}, {"geoip:se", "🇸🇪 Sweden"}, {"geoip:tr", "🇹🇷 Turkey"},
		{"geoip:ae", "🇦🇪 United Arab Emirates"}, {"geoip:ca", "🇨🇦 Canada"}, {"geoip:jp", "🇯🇵 Japan"},
		{"geoip:sg", "🇸🇬 Singapore"},
	}
	ipOther = []geoEntry{
		{"geoip:private", "Private networks (LAN)"},
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
	if code, ok := strings.CutPrefix(v, "geosite:"); ok {
		return humanizeSite(code)
	}
	if code, ok := strings.CutPrefix(v, "geoip:"); ok {
		return humanizeIP(code)
	}
	return v
}

// geoSource searches every category in the installed geosite.dat or geoip.dat, fetched
// from the daemon the first time the user types.
func geoSource(kind pb.GeoKind) *ui.PickSource {
	prefix, file, unit, name := "geosite:", "geosite.dat", "domain", humanizeSite
	if kind == pb.GeoKind_GEO_KIND_IP {
		prefix, file, unit, name = "geoip:", "geoip.dat", "range", humanizeIP
	}
	return &ui.PickSource{Label: "More in " + file, Load: func() ([]ui.PickItem, error) {
		res, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.ListGeoCategoriesResponse, error) {
			return c.ListGeoCategories(ctx, &pb.ListGeoCategoriesRequest{Kind: kind})
		})
		if err != nil {
			return nil, errors.New(message(err))
		}
		items := make([]ui.PickItem, len(res.Categories))
		for i, cat := range res.Categories {
			n := int(cat.Entries)
			items[i] = ui.PickItem{
				Value:  prefix + cat.Code,
				Label:  name(cat.Code),
				Hint:   ui.Dim(fmt.Sprintf("%s%s · %d %s%s", prefix, cat.Code, n, unit, plural(n))),
				Search: cat.Code,
			}
		}
		return items, nil
	}}
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
