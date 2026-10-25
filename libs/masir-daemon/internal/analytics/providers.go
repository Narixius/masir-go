package analytics

import (
	"net"
	"net/url"
	"sort"
	"strings"
)

// Known resolver IPs and hostnames. We send only the provider name, never the raw
// address: a DoH URL can contain a personal profile ID and a bootstrap IP can identify
// a private resolver.
var resolverHosts = map[string]string{
	"1.1.1.1": "cloudflare", "1.0.0.1": "cloudflare", "cloudflare-dns.com": "cloudflare",
	"8.8.8.8": "google", "8.8.4.4": "google", "dns.google": "google",
	"9.9.9.9": "quad9", "149.112.112.112": "quad9", "dns.quad9.net": "quad9",
	"208.67.222.222": "opendns", "208.67.220.220": "opendns", "doh.opendns.com": "opendns",
	"94.140.14.14": "adguard", "94.140.15.15": "adguard", "dns.adguard.com": "adguard",
	"dns.adguard-dns.com": "adguard", "dns.mullvad.net": "mullvad",
	"dns.nextdns.io": "nextdns", "dns.sb": "dns.sb", "dot.pub": "dns.pub",
}

var resolverSuffixes = []struct {
	suffix, provider string
}{
	{".nextdns.io", "nextdns"},
	{".adguard-dns.com", "adguard"},
	{".adguard.com", "adguard"},
	{".quad9.net", "quad9"},
	{".cloudflare-dns.com", "cloudflare"},
	{".dns.google", "google"},
	{".mullvad.net", "mullvad"},
	{".opendns.com", "opendns"},
}

// DoHProvider names the provider behind a DoH URL, or "custom"/"unknown".
func DoHProvider(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return "unknown"
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if p, ok := resolverHosts[host]; ok {
		return p
	}
	for _, s := range resolverSuffixes {
		if strings.HasSuffix(host, s.suffix) {
			return s.provider
		}
	}
	if net.ParseIP(host) != nil {
		return "custom"
	}
	return "custom"
}

// DNSProviders classifies bootstrap DNS addresses without retaining them.
func DNSProviders(ips []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, raw := range ips {
		ip := strings.TrimSpace(raw)
		if ip == "" {
			continue
		}
		provider := "custom"
		if p, ok := resolverHosts[strings.ToLower(ip)]; ok {
			provider = p
		}
		if !seen[provider] {
			seen[provider] = true
			out = append(out, provider)
		}
	}
	sort.Strings(out)
	return out
}

// DefaultDNSProviders are the providers behind masir's default bootstrap DNS.
var DefaultDNSProviders = []string{"cloudflare", "google"}

// IsDefaultDNS reports whether the list is exactly masir's default pair.
func IsDefaultDNS(ips []string) bool {
	got := DNSProviders(ips)
	if len(got) != len(DefaultDNSProviders) {
		return false
	}
	for i := range got {
		if got[i] != DefaultDNSProviders[i] {
			return false
		}
	}
	return true
}
