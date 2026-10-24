package xray

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/xtls/xray-core/app/router"
	routercmd "github.com/xtls/xray-core/app/router/command"
	"github.com/xtls/xray-core/common"
	corenet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/infra/conf"
)

var (
	ErrNotRunning = errors.New("xray is not running")
	ErrInvalid    = errors.New("invalid argument")
)

type RouteQuery struct {
	Domain     string
	IP         string
	Port       uint32
	Network    string // "tcp" (default) or "udp"
	InboundTag string
}

type RouteResult struct {
	OutboundTag string
	Matched     bool // false means no rule matched and Xray falls back to the first outbound
}

func (c *Core) TestRoute(ctx context.Context, q RouteQuery) (RouteResult, error) {
	rc := &routercmd.RoutingContext{
		InboundTag:   q.InboundTag,
		TargetDomain: q.Domain,
		TargetPort:   q.Port,
	}
	switch strings.ToLower(q.Network) {
	case "", "tcp":
		rc.Network = corenet.Network_TCP
	case "udp":
		rc.Network = corenet.Network_UDP
	default:
		return RouteResult{}, fmt.Errorf("network %q: %w", q.Network, ErrInvalid)
	}
	if q.IP != "" {
		ip := net.ParseIP(q.IP)
		if ip == nil {
			return RouteResult{}, fmt.Errorf("ip %q: %w", q.IP, ErrInvalid)
		}
		if v4 := ip.To4(); v4 != nil {
			ip = v4 // Xray's IP matchers treat a 16-byte address as IPv6
		}
		rc.TargetIPs = [][]byte{ip}
	}
	if q.Domain == "" && q.IP == "" {
		return RouteResult{}, fmt.Errorf("domain or ip is required: %w", ErrInvalid)
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.routing == nil {
		return RouteResult{}, ErrNotRunning
	}
	res, err := c.routing.TestRoute(ctx, &routercmd.TestRouteRequest{
		RoutingContext: rc,
		FieldSelectors: []string{"outbound"},
	})
	if err != nil {
		// No rule matched. The error only crosses gRPC as text, so match on the message.
		if strings.Contains(err.Error(), common.ErrNoClue.Error()) {
			return RouteResult{OutboundTag: firstTag(c.cur.outbounds)}, nil
		}
		return RouteResult{}, err
	}
	return RouteResult{OutboundTag: res.OutboundTag, Matched: true}, nil
}

// ValidateOutbound reports whether Xray can build raw as an outbound.
func ValidateOutbound(raw json.RawMessage) error {
	_, err := buildOutbound(raw)
	return err
}

// ValidateInbound reports whether Xray can build raw as an inbound.
func ValidateInbound(raw json.RawMessage) error {
	_, err := buildInbound(raw)
	return err
}

// ValidateRule reports whether Xray can build raw as a routing rule.
func ValidateRule(raw json.RawMessage) error {
	_, err := buildRule(raw)
	return err
}

func buildInbound(raw json.RawMessage) (*core.InboundHandlerConfig, error) {
	var ic conf.InboundDetourConfig
	if err := json.Unmarshal(raw, &ic); err != nil {
		return nil, err
	}
	return ic.Build()
}

func buildOutbound(raw json.RawMessage) (*core.OutboundHandlerConfig, error) {
	var oc conf.OutboundDetourConfig
	if err := json.Unmarshal(raw, &oc); err != nil {
		return nil, err
	}
	return oc.Build()
}

func buildRule(raw json.RawMessage) (*router.Config, error) {
	rc := conf.RouterConfig{RuleList: []json.RawMessage{raw}}
	return rc.Build() // resolves geosite/geoip here, so the daemon needs the .dat files
}
