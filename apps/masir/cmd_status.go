package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

const probeURL = "https://www.gstatic.com/generate_204"

func cmdStatus(a args) error {
	c, ok := runningDaemon()
	if !ok {
		if a.boolean("json", false) {
			fmt.Println(`{"daemon":{"running":false}}`)
			return nil
		}
		fmt.Printf("\n%s %s\n%s\n\n", ui.Bold("Masir Daemon"), ui.Red("○ Inactive (not running)"),
			ui.Tree([][2]string{{"", ui.Dim("start it with `masir daemon start`, or `masir connect`")}}))
		return nil
	}
	ctx, cancel := rpc()
	defer cancel()
	res, err := c.GetStatus(ctx, &pb.GetStatusRequest{})
	if err != nil {
		return err
	}
	st := res.Status
	check := a.boolean("check", true)

	if a.boolean("json", false) {
		raw, _ := protojson.Marshal(st)
		out := map[string]any{"status": json.RawMessage(raw)}
		if check {
			out["connectivity"] = measureConnectivity(c, st, nil)
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return appCtx.Err()
	}
	printStatus(st)
	if check {
		showConnectivity(c, st)
	}
	return appCtx.Err()
}

func printStatus(st *pb.Status) {
	priv := ui.Dim("unprivileged")
	if st.Privileged {
		priv = ui.Yellow("privileged")
	}
	how := "background"
	if st.ServiceInstalled {
		how = "service"
	}
	fmt.Printf("\n%s %s %s %s\n", ui.Bold("Masir Daemon"), ui.Dim(st.Version), ui.Green("● Active"),
		ui.Dim("(")+priv+ui.Dim(", "+how+")"))
	daemonRows := [][2]string{
		{"Serving", fmt.Sprintf("%s (uid %d)", ui.Visual(st.ServedUser), st.ServedUid)},
		{"Uptime", formatUptime(time.Duration(st.UptimeSeconds)*time.Second) +
			ui.Dim(time.Now().Add(-time.Duration(st.UptimeSeconds)*time.Second).Format(" (since 2006-01-02 15:04:05)"))},
	}
	if r := st.LatestRelease; r.GetAvailable() {
		daemonRows = append(daemonRows, [2]string{"Update", ui.Yellow(r.LatestVersion+" is available") + ui.Dim(" — `masir update`")})
	}
	fmt.Println(ui.Tree(daemonRows))

	set := st.Settings
	fmt.Println()
	head := ui.Bold("Xray Core") + " " + ui.Dim("v"+st.XrayVersion) + " "
	switch {
	case st.LastError != "":
		fmt.Println(head + ui.Red("● Error"))
	case st.XrayRunning:
		fmt.Println(head + ui.Green("● Active"))
	default:
		fmt.Println(head + ui.Yellow("○ Inactive (masir is off)"))
	}

	var rows [][2]string
	if st.XrayRunning {
		mode := ui.Blue("Mixed Proxy")
		if st.TunRunning {
			mode = ui.Green("TUN (system-wide routing)")
		}
		rows = append(rows, [2]string{"Mode", mode})
	}
	rows = append(rows, [2]string{"Routing Mode", fallbackText(set.GetFallback())})
	// The active proxy is named in Connectivity; here only what needs attention.
	switch {
	case st.ActiveConfigId == "":
		rows = append(rows, [2]string{"Active Proxy", ui.Dim("none — pick one with `masir use`")})
	case st.ActiveStale:
		rows = append(rows, [2]string{"Active Proxy", st.ActiveConfigName + ui.Yellow(" (no longer in its subscription; still used)")})
	}
	port := fmt.Sprintf("%d", set.GetProxyPort())
	if st.ProxyRunning {
		port = st.ProxyAddress
	}
	if set.GetAllowLan() {
		port += ui.Dim(" (LAN allowed)")
	}
	rows = append(rows,
		[2]string{"Proxy Port", port},
		[2]string{"DoH DNS", set.GetDohDns()},
		[2]string{"DNS Servers", strings.Join(set.GetDnsServers(), ", ")},
	)
	switch geo := st.GeoUpdatedAt; {
	case geo == "":
		rows = append(rows, [2]string{"Geo Data", ui.Yellow("missing; geosite:/geoip: rules won't work") + ui.Dim(" — `masir update geo`")})
	default:
		if t, err := time.Parse(time.RFC3339, geo); err == nil {
			rows = append(rows, [2]string{"Geo Data", t.Local().Format("2006-01-02") + ui.Dim(" ("+formatAge(time.Since(t))+" ago)")})
		}
	}
	if len(set.GetTunDns()) > 0 {
		rows = append(rows, [2]string{"TUN DNS", strings.Join(set.GetTunDns(), ", ")})
	}
	if !st.TunAvailable && set.GetTun() {
		rows = append(rows, [2]string{"TUN", ui.Yellow("on, but needs " + privilegeName + ": `masir connect` sets it up")})
	}
	if st.FellBackRules > 0 {
		rows = append(rows, [2]string{"Rules", ui.Yellow(fmt.Sprintf("%d rule(s) lost their proxy and use the active one; see `masir rules list`", st.FellBackRules))})
	}
	if set.GetAlwaysOnProxy() && !set.GetPower() {
		rows = append(rows, [2]string{"Always-on proxy", "the proxy stays up while masir is off"})
	}
	if st.LastError != "" {
		rows = append(rows, [2]string{"Error", ui.Red(st.LastError)})
	}
	if !st.XrayRunning && st.LastError == "" {
		rows = append(rows, [2]string{"", ui.Dim("turn it on with `masir connect` (or `masir connect --proxy` for proxy only)")})
	}
	fmt.Println(ui.Visual(ui.Tree(rows))) // names and errors in display order, once
}

func fallbackText(f pb.Fallback) string {
	if f == pb.Fallback_FALLBACK_PROXY {
		return ui.Cyan("proxy") + ui.Dim(" (unmatched traffic goes through the active proxy)")
	}
	return ui.Green("direct") + ui.Dim(" (unmatched traffic skips the proxy)")
}

func formatUptime(d time.Duration) string {
	d = d.Round(time.Second)
	days := int(d.Hours()) / 24
	h := int(d.Hours()) % 24
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh %dm", days, h, m)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

type probeResult struct {
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latencyMs,omitempty"`
	Error     string `json:"error,omitempty"`
	Skipped   string `json:"skipped,omitempty"`
}

type connectivity struct {
	Internet  probeResult `json:"internet"`
	Proxy     probeResult `json:"proxy"`
	ProxyName string      `json:"proxyName,omitempty"`
	// SubscriptionIssue is why the active proxy may have stopped: its subscription
	// expired or used up its traffic.
	SubscriptionIssue string `json:"subscriptionIssue,omitempty"`
}

// showConnectivity prints the Connectivity section: direct internet and the active
// proxy, each with its own spinner, filled in the moment each answers.
func showConnectivity(c pb.DaemonServiceClient, st *pb.Status) *connectivity {
	fmt.Println("\n" + ui.Bold("Connectivity"))
	proxy := ui.LiveRow{Label: "Proxy"}
	if st.ActiveConfigId != "" {
		proxy.Prefix = ui.Cyan(ui.Visual(st.ActiveConfigName))
	}
	live := ui.NewLive(ui.LiveRow{Label: "Internet"}, proxy)
	conn := measureConnectivity(c, st, func(row int, r probeResult) { live.Set(row, r.text()) })
	live.Stop()
	conn.verdict()
	return conn
}

// measureConnectivity probes the internet directly and the active proxy through the
// daemon (the config itself, whatever the routing mode sends through it), in parallel.
// Both stop when the user quits.
func measureConnectivity(c pb.DaemonServiceClient, st *pb.Status, done func(row int, r probeResult)) *connectivity {
	conn := &connectivity{ProxyName: st.ActiveConfigName, SubscriptionIssue: subscriptionIssue(c, st)}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		conn.Internet = probeInternet()
		if done != nil {
			done(0, conn.Internet)
		}
	}()
	go func() {
		defer wg.Done()
		conn.Proxy = probeProxy(c, st)
		if done != nil {
			done(1, conn.Proxy)
		}
	}()
	wg.Wait()
	return conn
}

func probeInternet() probeResult {
	ctx, cancel := context.WithTimeout(appCtx, 8*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return probeResult{Error: shortErr(err)}
	}
	resp.Body.Close()
	return probeResult{OK: true, LatencyMs: time.Since(start).Milliseconds()}
}

func probeProxy(c pb.DaemonServiceClient, st *pb.Status) probeResult {
	switch {
	case st.ActiveConfigId == "":
		return probeResult{Skipped: "no proxy selected"}
	case st.ActiveStale:
		return probeResult{Skipped: "the selected proxy left its subscription"}
	}
	ctx, cancel := context.WithTimeout(appCtx, 15*time.Second)
	defer cancel()
	var r probeResult
	err := probe(ctx, c, st.ActiveSubscriptionId, []string{st.ActiveConfigId}, func(_ string, l *pb.LatencyResult) {
		if l.Error != "" {
			r = probeResult{Error: shortErr(errors.New(l.Error))}
		} else {
			r = probeResult{OK: true, LatencyMs: l.LatencyMs}
		}
	})
	if err != nil && !r.OK && r.Error == "" {
		r.Error = shortErr(err)
	}
	return r
}

// subscriptionIssue names the likeliest reason a proxy stops working: its
// subscription expired or used up its traffic.
func subscriptionIssue(c pb.DaemonServiceClient, st *pb.Status) string {
	if st.ActiveSubscriptionId == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(appCtx, rpcTimeoutShort)
	defer cancel()
	res, err := c.GetSubscription(ctx, &pb.GetSubscriptionRequest{Id: st.ActiveSubscriptionId})
	if err != nil || res.Subscription.GetUsage() == nil {
		return ""
	}
	sub, u := res.Subscription, res.Subscription.GetUsage()
	switch {
	case u.ExpireAtMs > 0 && u.ExpireAtMs < time.Now().UnixMilli():
		return fmt.Sprintf("Subscription %q expired on %s.", sub.Name, time.UnixMilli(u.ExpireAtMs).Format("2006-01-02"))
	case u.TotalBytes > 0 && u.UploadBytes+u.DownloadBytes >= u.TotalBytes:
		return fmt.Sprintf("Subscription %q has used up its traffic.", sub.Name)
	}
	return ""
}

func (r probeResult) text() string {
	switch {
	case r.Skipped != "":
		return ui.Dim("– " + r.Skipped)
	case r.OK:
		return ui.Green("✔") + " " + latencyText(&pb.LatencyResult{LatencyMs: r.LatencyMs})
	default:
		return ui.Red("✖ " + ui.Visual(r.Error))
	}
}

func (c *connectivity) verdict() {
	if appCtx.Err() != nil {
		return
	}
	switch {
	case c.Internet.OK && c.Proxy.OK:
		fmt.Println(ui.Green("✔") + " Online, and the active proxy works.")
	case c.Proxy.OK:
		fmt.Println(ui.Yellow("▲") + " The proxy works, but direct internet doesn't (expected on filtered networks).")
	case c.Internet.OK && c.Proxy.Skipped == "" && c.SubscriptionIssue != "":
		fmt.Println(ui.Red("✖") + " Online, but the active proxy doesn't respond. " + ui.Visual(c.SubscriptionIssue))
	case c.Internet.OK && c.Proxy.Skipped == "":
		fmt.Println(ui.Yellow("▲") + " Online, but the active proxy doesn't respond. Try another: `masir use --fastest`.")
	case c.Internet.OK:
		fmt.Println(ui.Green("✔") + " Online.")
	default:
		fmt.Println(ui.Red("✖") + " No connectivity.")
	}
}

func shortErr(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "context canceled") || strings.Contains(msg, "Canceled"):
		return "cancelled"
	case strings.Contains(msg, "Client.Timeout") || strings.Contains(msg, "deadline"):
		return "timeout"
	}
	if i := strings.LastIndex(msg, ": "); i >= 0 {
		return msg[i+2:]
	}
	return msg
}

// formatAge is a rough duration: minutes, hours or days.
func formatAge(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
