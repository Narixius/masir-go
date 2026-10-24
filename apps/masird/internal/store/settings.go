// Package store holds the user's masird state and saves each part to its own file.
package store

import (
	"fmt"
	"net"
	"net/url"
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
}

// DefaultSettings are the out-of-the-box settings. Fallback starts direct because proxy
// needs a selected config.
func DefaultSettings() Settings {
	return Settings{
		Fallback:   FallbackDirect,
		AllowLAN:   true,
		ProxyPort:  7890,
		DoHDNS:     "https://1.1.1.1/dns-query",
		DNSServers: []string{"1.1.1.1", "8.8.8.8"},
		TunGateway: "10.10.20.1/24",
	}
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
	return nil
}
