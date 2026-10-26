package xray

import (
	"context"
	"strings"

	statscmd "github.com/xtls/xray-core/app/stats/command"
)

// Inbounds the daemon adds for itself; their bytes aren't an app's traffic.
var internalInbounds = map[string]bool{
	apiInboundTag:     true,
	"loopback-dns-in": true,
	"dns-bootstrap":   true,
}

// Traffic is the bytes the apps on this machine have sent (Up) and received (Down)
// through Xray's inbounds since Xray started.
type Traffic struct{ Up, Down uint64 }

// Traffic reads the counters. ok is false while Xray isn't running.
func (c *Core) Traffic(ctx context.Context) (t Traffic, ok bool, err error) {
	c.mu.RLock()
	stats := c.stats
	c.mu.RUnlock()
	if stats == nil {
		return Traffic{}, false, nil
	}
	res, err := stats.QueryStats(ctx, &statscmd.QueryStatsRequest{Pattern: "inbound>>>"})
	if err != nil {
		return Traffic{}, false, err
	}
	names := make(map[string]int64, len(res.Stat))
	for _, s := range res.Stat {
		names[s.Name] = s.Value
	}
	return sumTraffic(names), true, nil
}

// sumTraffic adds up the per-inbound counters, named like "inbound>>>tun-in>>>traffic>>>uplink".
// For an inbound, uplink is what came in from the app and downlink what went back to it.
func sumTraffic(counters map[string]int64) Traffic {
	var t Traffic
	for name, v := range counters {
		parts := strings.Split(name, ">>>")
		if len(parts) != 4 || parts[0] != "inbound" || parts[2] != "traffic" || internalInbounds[parts[1]] || v < 0 {
			continue
		}
		switch parts[3] {
		case "uplink":
			t.Up += uint64(v)
		case "downlink":
			t.Down += uint64(v)
		}
	}
	return t
}
