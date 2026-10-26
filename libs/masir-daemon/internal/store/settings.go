// Package store holds the user's masird state and saves each part to its own file.
package store

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

type Fallback string

const (
	FallbackDirect Fallback = "direct"
	FallbackProxy  Fallback = "proxy"
)

type Settings struct {
	Power         bool     `json:"power"`
	Tun           bool     `json:"tun"`
	AlwaysOnProxy bool     `json:"alwaysOnProxy"`
	Fallback      Fallback `json:"fallback"`
	AllowLAN      bool     `json:"allowLan"`
	ProxyPort     int      `json:"proxyPort"`
	DoHDNS        string   `json:"dohDns"`
	DNSServers    []string `json:"dnsServers"`
	TunDNS        []string `json:"tunDns"`
	TunGateway    string   `json:"tunGateway"`
	// SubIntervalMinutes is how often subscriptions refresh in the background, unless a
	// subscription sets its own interval.
	SubIntervalMinutes int `json:"subIntervalMinutes"`
	// XrayLogLevel is Xray's own log level: debug, info, warning, error, or none.
	XrayLogLevel string `json:"xrayLogLevel"`
	// Analytics sends anonymous usage and error events to PostHog.
	Analytics bool `json:"analytics"`
	// RulesEnabled false runs without the user's rules, which stay saved with their own
	// disabled flags.
	RulesEnabled bool `json:"rulesEnabled"`
}

// Bounds for subscription refresh intervals, in minutes: the daemon looks for due
// subscriptions every 5 minutes, so less couldn't run sooner; 30 days at most.
const (
	MinSubIntervalMinutes = 5
	MaxSubIntervalMinutes = 30 * 24 * 60
)

// ValidSubInterval checks a refresh interval in minutes.
func ValidSubInterval(minutes int) error {
	if minutes < MinSubIntervalMinutes || minutes > MaxSubIntervalMinutes {
		return fmt.Errorf("subscription refresh interval must be %d to %d minutes, not %d: %w",
			MinSubIntervalMinutes, MaxSubIntervalMinutes, minutes, ErrInvalid)
	}
	return nil
}

// DefaultSettings are the out-of-the-box settings. The fallback is proxy once a config
// is selected; with no selection the composer still has to use direct. The proxy only
// listens on this machine, and only while masir is on: sharing it with the network or
// keeping it up when masir is off are choices the user makes.
func DefaultSettings() Settings {
	return Settings{
		Fallback:   FallbackProxy,
		ProxyPort:  7890,
		DoHDNS:     "https://1.1.1.1/dns-query",
		DNSServers: []string{"1.1.1.1", "8.8.8.8"},
		TunGateway: "10.10.20.1/24",

		SubIntervalMinutes: 12 * 60,
		XrayLogLevel:       "warning",
		Analytics:          true,
		RulesEnabled:       true,
	}
}

// NormalizeXrayLogLevel lowercases a level and turns Xray's "warn" shorthand into
// "warning", the spelling Xray itself accepts.
func NormalizeXrayLogLevel(level string) string {
	level = strings.ToLower(strings.TrimSpace(level))
	if level == "warn" {
		return "warning"
	}
	return level
}

// ValidXrayLogLevel checks Xray's log level.
func ValidXrayLogLevel(level string) error {
	switch NormalizeXrayLogLevel(level) {
	case "debug", "info", "warning", "error", "none":
		return nil
	}
	return fmt.Errorf("xray log level must be debug, info, warning, error, or none, not %q: %w", level, ErrInvalid)
}

// XrayRuns reports whether settings call for a running Xray.
func (s Settings) XrayRuns() bool { return s.Power || s.AlwaysOnProxy }

func (s Settings) Validate() error {
	if s.Fallback != FallbackDirect && s.Fallback != FallbackProxy {
		return fmt.Errorf("fallback %q: %w", s.Fallback, ErrInvalid)
	}
	if s.ProxyPort < 1 || s.ProxyPort > 65535 {
		return fmt.Errorf("proxy port %d: %w", s.ProxyPort, ErrInvalid)
	}
	if u, err := url.Parse(s.DoHDNS); err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("doh dns must be an https:// URL: %w", ErrInvalid)
	}
	if len(s.DNSServers) == 0 {
		return fmt.Errorf("at least one dns server is required: %w", ErrInvalid)
	}
	for _, list := range [][]string{s.DNSServers, s.TunDNS} {
		for _, ip := range list {
			if net.ParseIP(ip) == nil {
				return fmt.Errorf("dns server %q must be an IP: %w", ip, ErrInvalid)
			}
		}
	}
	if ip, _, err := net.ParseCIDR(s.TunGateway); err != nil || ip.To4() == nil {
		return fmt.Errorf("tun gateway %q must be an IPv4 CIDR: %w", s.TunGateway, ErrInvalid)
	}
	if err := ValidSubInterval(s.SubIntervalMinutes); err != nil {
		return err
	}
	return ValidXrayLogLevel(s.XrayLogLevel)
}
