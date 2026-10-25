package main

import (
	"context"
	"fmt"
	"sync"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

func cmdUse(a args) error {
	all, err := loadNodes()
	if err != nil {
		return err
	}
	if len(all) == 0 {
		return fail("no subscriptions yet; add one with `masir sub add <url>`")
	}

	var key, reason string
	switch {
	case a.boolean("fastest", false):
		key, err = fastest(all)
		reason = "fastest"
	case a.arg(0) != "":
		s, cfg, ok := findNode(all, a.arg(0))
		if !ok {
			return fail("outbound node with ID %q not found in any subscription", a.arg(0))
		}
		key = nodeKey(s.sub.Id, cfg.Id)
		reason = "id"
	case !ui.Interactive():
		return fail("pass a proxy id: masir use <id> (ids are in `masir sub list --nodes`), or --fastest")
	default:
		key, err = pickNode(all, "Choose active outbound configuration", activeKey())
		reason = "interactive"
	}
	if err != nil {
		return err
	}
	return selectNode(all, key, "Active outbound switched to", reason)
}

func selectNode(all []subNodes, key, verb, reason string) error {
	subID, cfgID := splitKey(key)
	if _, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.SelectConfigResponse, error) {
		return c.SelectConfig(ctx, &pb.SelectConfigRequest{SubscriptionId: subID, ConfigId: cfgID, Reason: reason})
	}); err != nil {
		return err
	}
	_, cfg, _ := findNode(all, key)
	ui.Success(fmt.Sprintf("%s %q [%s].", verb, ui.Visual(cfg.Name), cfg.Id))

	st, err := currentStatus()
	if err != nil {
		return err
	}
	switch {
	case !st.Settings.GetPower() && !st.ProxyRunning:
		ui.Info("masir is off; turn it on with `masir connect`.")
	case st.Settings.GetFallback() == pb.Fallback_FALLBACK_DIRECT:
		ui.Info("Unmatched traffic goes direct; send it through this proxy with `masir mode proxy`.")
	}
	return nil
}

// fastest tests every config and returns the quickest one's key.
func fastest(all []subNodes) (string, error) {
	c, err := daemon()
	if err != nil {
		return "", err
	}
	total := 0
	for _, s := range all {
		total += len(s.configs)
	}
	sp := ui.Spin(fmt.Sprintf("Testing %d proxies", total))

	var (
		mu       sync.Mutex
		done     int
		best     string
		bestMs   int64 = -1
		bestName string
		wg       sync.WaitGroup
	)
	names := map[string]string{}
	for _, s := range all {
		for _, cfg := range s.configs {
			names[nodeKey(s.sub.Id, cfg.Id)] = cfg.Name
		}
	}
	for _, s := range all {
		ids := make([]string, len(s.configs))
		for i, cfg := range s.configs {
			ids[i] = cfg.Id
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = probe(appCtx, c, s.sub.Id, ids, func(cfgID string, l *pb.LatencyResult) {
				mu.Lock()
				defer mu.Unlock()
				done++
				sp.Message(fmt.Sprintf("Testing proxies... %d/%d", done, total))
				if l.Error == "" && (bestMs < 0 || l.LatencyMs < bestMs) {
					best, bestMs, bestName = nodeKey(s.sub.Id, cfgID), l.LatencyMs, names[nodeKey(s.sub.Id, cfgID)]
				}
			})
		}()
	}
	wg.Wait()
	if appCtx.Err() != nil {
		return "", appCtx.Err()
	}
	if best == "" {
		sp.Fail("No proxy responded.")
		return "", errReported
	}
	sp.Stop(fmt.Sprintf("Fastest: %s (%dms)", ui.Visual(bestName), bestMs))
	return best, nil
}
