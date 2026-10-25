package main

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

type subNodes struct {
	sub     *pb.Subscription
	configs []*pb.SubscriptionConfig
}

// node identifies one config: "<subscription id>/<config id>", the picker's value.
func nodeKey(subID, cfgID string) string { return subID + "/" + cfgID }

func splitKey(k string) (subID, cfgID string) {
	subID, cfgID, _ = strings.Cut(k, "/")
	return
}

// loadNodes returns every subscription with its configs, sorted by name.
func loadNodes() ([]subNodes, error) {
	c, err := daemon()
	if err != nil {
		return nil, err
	}
	ctx, cancel := rpc()
	defer cancel()
	list, err := c.ListSubscriptions(ctx, &pb.ListSubscriptionsRequest{})
	if err != nil {
		return nil, err
	}
	var out []subNodes
	for _, s := range list.Subscriptions {
		got, err := c.GetSubscription(ctx, &pb.GetSubscriptionRequest{Id: s.Id})
		if err != nil {
			return nil, err
		}
		out = append(out, subNodes{sub: got.Subscription, configs: got.Configs})
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].sub.Name) < strings.ToLower(out[j].sub.Name) })
	return out, nil
}

// findNode looks a config up by id across subscriptions, or by "<sub>/<cfg>".
func findNode(all []subNodes, id string) (subNodes, *pb.SubscriptionConfig, bool) {
	wantSub, wantCfg := "", id
	if strings.Contains(id, "/") {
		wantSub, wantCfg = splitKey(id)
	}
	for _, s := range all {
		if wantSub != "" && s.sub.Id != wantSub {
			continue
		}
		for _, c := range s.configs {
			if c.Id == wantCfg {
				return s, c, true
			}
		}
	}
	return subNodes{}, nil, false
}

func latencyText(l *pb.LatencyResult) string {
	if l == nil {
		return ""
	}
	if l.Error != "" {
		if strings.Contains(l.Error, "deadline") || strings.Contains(l.Error, "timeout") {
			return ui.Red("timeout")
		}
		return ui.Red("error")
	}
	ms := fmt.Sprintf("%dms", l.LatencyMs)
	switch {
	case l.LatencyMs < 300:
		return ui.Green(ms)
	case l.LatencyMs < 800:
		return ui.Yellow(ms)
	default:
		return ui.Red(ms)
	}
}

// subHint is a subscription's header line: config count, traffic, expiry.
func subHint(s *pb.Subscription) string {
	parts := []string{fmt.Sprintf("%d config%s", s.ConfigCount, plural(int(s.ConfigCount)))}
	if u := s.Usage; u != nil {
		used := u.UploadBytes + u.DownloadBytes
		if u.TotalBytes > 0 {
			parts = append(parts, formatBytes(used)+" / "+formatBytes(u.TotalBytes))
		} else if used > 0 {
			parts = append(parts, formatBytes(used)+" used")
		}
	}
	hint := ui.Dim(strings.Join(parts, " · "))
	if u := s.Usage; u != nil && u.ExpireAtMs > 0 {
		exp := time.UnixMilli(u.ExpireAtMs)
		if time.Now().After(exp) {
			hint += ui.Dim(" · ") + ui.Red("expired "+exp.Format("2006-01-02"))
		} else {
			hint += ui.Dim(" · expires " + exp.Format("2006-01-02"))
		}
	}
	return hint
}

// pickNode shows every subscription's configs grouped. Configs on screen are latency
// tested fresh, each on its own, and a test is cancelled when its row scrolls away.
// l retests the item under the cursor, a retests everything. It returns the chosen
// "<sub>/<cfg>" key.
func pickNode(all []subNodes, msg, activeKey string) (string, error) {
	c, err := daemon()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithCancel(appCtx)
	pr := &prober{c: c, ctx: ctx, inflight: map[string]inflight{}, results: map[string]string{},
		done: map[string]bool{}, visible: map[string]bool{}, sem: make(chan struct{}, probeWorkers)}
	defer func() { cancel(); pr.wg.Wait() }()

	groups := make([]ui.PickGroup, 0, len(all))
	for _, s := range all {
		g := ui.PickGroup{Label: s.sub.Name, Hint: subHint(s.sub)}
		if len(all) == 1 {
			g.Label = "" // one subscription: a flat list, no group header
		}
		for _, cfg := range s.configs {
			key := nodeKey(s.sub.Id, cfg.Id)
			hint := ui.Dim(cfg.Protocol)
			if key == activeKey {
				hint += " " + ui.Green("(active)")
			}
			pr.results[key] = latencyText(cfg.Latency) // shown until its fresh test starts
			g.Items = append(g.Items, ui.PickItem{
				Value:  key,
				Label:  cfg.Name,
				Hint:   hint,
				Search: cfg.Protocol + " " + s.sub.Name + " " + cfg.Address,
				Suffix: pr.results[key],
			})
		}
		groups = append(groups, g)
	}

	pr.picker = ui.NewPicker(ui.PickParams{
		Message:   msg,
		Groups:    groups,
		Initial:   []string{activeKey},
		OnVisible: pr.onVisible,
		Keys: []ui.PickKey{
			{Key: 'l', Help: "l retest", Run: pr.retest},
			{Key: 'a', Help: "a retest all", Run: func(string) { pr.retestAll() }},
		},
	})
	vals, err := pr.picker.Run()
	if err != nil {
		return "", err
	}
	return vals[0], nil
}

// probeWorkers bounds how many configs are tested at once; each test runs a
// temporary Xray in the daemon.
const probeWorkers = 8

type inflight struct {
	cancel context.CancelFunc
	token  *int // tells a finishing test whether it's still the current one
}

// prober runs the picker's latency tests. onVisible and the key handlers run on the
// picker's loop; tests finish on their own goroutines, so state sits behind mu.
type prober struct {
	c      pb.DaemonServiceClient
	ctx    context.Context
	picker *ui.Picker
	sem    chan struct{}
	wg     sync.WaitGroup

	mu       sync.Mutex
	inflight map[string]inflight
	results  map[string]string // last result shown per row
	done     map[string]bool   // tested this run
	visible  map[string]bool
}

func (p *prober) onVisible(values []string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.visible = map[string]bool{}
	for _, v := range values {
		p.visible[v] = true
	}
	for v := range p.inflight {
		if !p.visible[v] {
			p.stopLocked(v)
		}
	}
	for _, v := range values {
		if !p.done[v] {
			p.startLocked(v)
		}
	}
}

func (p *prober) retest(v string) {
	if v == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stopLocked(v)
	p.done[v] = false
	p.startLocked(v)
}

func (p *prober) retestAll() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for v := range p.inflight {
		p.stopLocked(v)
	}
	p.done = map[string]bool{} // rows off screen get tested when they scroll in
	for v := range p.visible {
		p.startLocked(v)
	}
}

// startLocked expects p.mu held.
func (p *prober) startLocked(v string) {
	if _, running := p.inflight[v]; running {
		return
	}
	ctx, cancel := context.WithCancel(p.ctx)
	token := new(int)
	p.inflight[v] = inflight{cancel: cancel, token: token}
	p.picker.SetSuffix(v, "", true)
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer cancel()
		select {
		case p.sem <- struct{}{}:
			defer func() { <-p.sem }()
		case <-ctx.Done():
			return
		}
		sub, cfg := splitKey(v)
		var text string
		err := probe(ctx, p.c, sub, []string{cfg}, func(_ string, l *pb.LatencyResult) { text = latencyText(l) })
		if ctx.Err() != nil {
			return // scrolled away, retested, or the app is quitting
		}
		if err != nil && text == "" {
			text = ui.Red("error")
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if cur, ok := p.inflight[v]; !ok || cur.token != token {
			return
		}
		delete(p.inflight, v)
		p.done[v] = true
		p.results[v] = text
		p.picker.SetSuffix(v, text, false)
	}()
}

// stopLocked cancels v's test and puts its last result back. Expects p.mu held.
func (p *prober) stopLocked(v string) {
	f, ok := p.inflight[v]
	if !ok {
		return
	}
	f.cancel()
	delete(p.inflight, v)
	p.picker.SetSuffix(v, p.results[v], false)
}

// probe latency-tests configs of one subscription, reporting each as it finishes.
func probe(ctx context.Context, c pb.DaemonServiceClient, subID string, cfgIDs []string, report func(cfgID string, l *pb.LatencyResult)) error {
	stream, err := c.TestSubscriptionLatency(ctx, &pb.TestSubscriptionLatencyRequest{SubscriptionId: subID, ConfigIds: cfgIDs})
	if err != nil {
		return err
	}
	for {
		res, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		report(res.ConfigId, res.Result)
	}
}

func formatBytes(n int64) string { return ui.Bytes(n) }

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// activeKey is the current selection's "<sub>/<cfg>", or "".
func activeKey() string {
	res, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.GetActiveConfigResponse, error) {
		return c.GetActiveConfig(ctx, &pb.GetActiveConfigRequest{})
	})
	if err != nil || res.ConfigId == "" {
		return ""
	}
	return nodeKey(res.SubscriptionId, res.ConfigId)
}
