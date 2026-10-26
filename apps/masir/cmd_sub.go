package main

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

func cmdSubAdd(a args) error {
	rawURL := a.arg(0)
	if rawURL == "" {
		if !ui.Interactive() {
			return fail("pass the subscription URL: masir sub add <url>")
		}
		var err error
		rawURL, err = ui.Text("Subscription URL", "", validateURL)
		if err != nil {
			return err
		}
	} else if err := validateURL(rawURL); err != nil {
		return err
	}

	all, err := loadNodes()
	if err != nil {
		return err
	}
	name := a.str("name")
	if !a.has("name") && ui.Interactive() {
		name, err = ui.Text("Name (leave empty to use the provider's)", "", nil)
		if err != nil && err != ui.ErrNoTTY {
			return err
		}
	}
	name = strings.TrimSpace(name)
	for _, s := range all {
		if name != "" && strings.EqualFold(s.sub.Name, name) {
			return advised(fmt.Sprintf("Subscription %q already exists.", name), "Refresh it instead:", "masir sub update --name "+quoteArg(name))
		}
	}
	var interval uint32
	if v := a.str("interval"); v != "" {
		if err := validateMinutes(v); err != nil {
			return fail("--interval: %v", err)
		}
		n, _ := strconv.Atoi(v)
		interval = uint32(n)
	}

	label := name
	if label == "" {
		label = hostOf(rawURL)
	}
	sp := ui.Spin(fmt.Sprintf("Fetching and parsing subscription %q", label))
	res, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.AddSubscriptionResponse, error) {
		return c.AddSubscription(ctx, &pb.AddSubscriptionRequest{Url: rawURL, Name: name, CustomUpdateIntervalMinutes: interval})
	})
	if err != nil {
		failWith(sp, "", err)
		return errReported
	}
	sub := res.Subscription
	sp.Stop(fmt.Sprintf("Subscription %q added successfully (%d config%s parsed).", ui.Visual(sub.Name), sub.ConfigCount, plural(int(sub.ConfigCount))))

	if !ui.Interactive() || sub.ConfigCount == 0 {
		return nil
	}
	active := activeKey()
	msg, def := "Set active outbound now?", true
	if active != "" {
		msg, def = "Change active outbound to one of the newly added configs?", false
	}
	yes, err := ui.Confirm(msg, def)
	if err != nil || !yes {
		return err
	}
	all, err = loadNodes()
	if err != nil {
		return err
	}
	var only []subNodes
	for _, s := range all {
		if s.sub.Id == sub.Id {
			only = append(only, s)
		}
	}
	key, err := pickNode(only, "Select active outbound config", active)
	if err != nil {
		return err
	}
	return selectNode(all, key, "Active outbound configured to", "interactive")
}

func cmdSubList(a args) error {
	all, err := loadNodes()
	if err != nil {
		return err
	}
	if len(all) == 0 {
		ui.Info("No subscriptions yet. Add one with `masir sub add <url>`.")
		return nil
	}
	active := activeKey()
	activeSub, activeCfg := splitKey(active)

	var b strings.Builder
	fmt.Fprintf(&b, "\n%s\n", ui.Bold(fmt.Sprintf("Subscriptions (%d)", len(all))))
	for _, s := range all {
		dot := ui.Gray("○")
		head := ui.Bold(ui.Visual(s.sub.Name))
		if s.sub.Id == activeSub {
			dot = ui.Green("●")
			for _, c := range s.configs {
				if c.Id == activeCfg {
					head += ui.Cyan(fmt.Sprintf(" [Active: %s (%s)]", ui.Visual(c.Name), c.Id))
				}
			}
		}
		fmt.Fprintf(&b, "\n%s %s %s\n", dot, head, ui.Dim(s.sub.Id))
		rows := [][2]string{
			{"URL", ui.Dim(ui.Visual(s.sub.Url))},
			{"Configs", fmt.Sprintf("%d node%s", s.sub.ConfigCount, plural(int(s.sub.ConfigCount)))},
		}
		updated := "never"
		if s.sub.UpdatedAtMs > 0 {
			updated = time.UnixMilli(s.sub.UpdatedAtMs).UTC().Format("2006-01-02 15:04 UTC")
		}
		rows = append(rows, [2]string{"Updated", updated + ui.Dim(fmt.Sprintf(" · every %s", minutes(s.sub.UpdateIntervalMinutes)))})
		if s.sub.LastError != "" {
			rows = append(rows, [2]string{"Last fetch", ui.Red(ui.Visual(s.sub.LastError))})
		}
		if u := s.sub.Usage; u != nil {
			traffic := formatBytes(u.UploadBytes+u.DownloadBytes) + " used"
			if u.TotalBytes > 0 {
				traffic = formatBytes(u.UploadBytes+u.DownloadBytes) + " / " + formatBytes(u.TotalBytes)
			}
			rows = append(rows, [2]string{"Traffic", traffic})
			expires := "No expiration"
			if u.ExpireAtMs > 0 {
				exp := time.UnixMilli(u.ExpireAtMs)
				expires = exp.Format("2006-01-02")
				if time.Now().After(exp) {
					expires = ui.Red("expired " + expires)
				}
			}
			rows = append(rows, [2]string{"Expires", expires})
		}
		b.WriteString(indent(ui.Tree(rows), "  ") + "\n")
		if a.boolean("nodes", false) {
			b.WriteString(nodeTable(s, activeCfg) + "\n")
		}
	}
	fmt.Println(b.String())
	return nil
}

func cmdSubUpdate(a args) error {
	all, err := loadNodes()
	if err != nil {
		return err
	}
	name := a.str("name")
	if name == "" {
		name = a.arg(0)
	}
	targets := all
	if name != "" {
		s, ok := findSub(all, name)
		if !ok {
			return fail("no subscription named %q", name)
		}
		targets = []subNodes{s}
	}
	if len(targets) == 0 {
		ui.Info("No subscriptions to update. Add one with `masir sub add <url>`.")
		return nil
	}
	c, err := daemon()
	if err != nil {
		return err
	}
	update := func(s subNodes) (*pb.Subscription, error) {
		ctx, cancel := context.WithTimeout(appCtx, subUpdateTimeout)
		defer cancel()
		res, err := c.UpdateSubscription(ctx, &pb.UpdateSubscriptionRequest{Id: s.sub.Id})
		if err != nil {
			return nil, err
		}
		return res.Subscription, nil
	}

	if len(targets) == 1 {
		s := targets[0]
		sp := ui.Spin(fmt.Sprintf("Updating %q", ui.Visual(s.sub.Name)))
		sub, err := update(s)
		if err != nil {
			sp.Fail(fmt.Sprintf("%s: %s (kept the %d configs it had)", s.sub.Name, message(err), s.sub.ConfigCount))
			return errReported
		}
		sp.Stop(fmt.Sprintf("%q updated (%d config%s).", ui.Visual(sub.Name), sub.ConfigCount, plural(int(sub.ConfigCount))))
		return nil
	}

	// All at once, each with its own timeout and row: a slow provider holds up no one.
	fmt.Println(ui.Bold(fmt.Sprintf("Updating %d subscriptions", len(targets))))
	rows := make([]ui.LiveRow, len(targets))
	for i, s := range targets {
		rows[i] = ui.LiveRow{Label: ui.Visual(s.sub.Name)}
	}
	live := ui.NewLive(rows...)
	var failed atomic.Int32
	var wg sync.WaitGroup
	for i, s := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub, err := update(s)
			if err != nil {
				failed.Add(1)
				live.Set(i, ui.Red("✖ "+ui.Visual(shortErr(err)))+ui.Dim(fmt.Sprintf(" (kept its %d configs)", s.sub.ConfigCount)))
				return
			}
			live.Set(i, ui.Green("✔")+fmt.Sprintf(" %d config%s", sub.ConfigCount, plural(int(sub.ConfigCount))))
		}()
	}
	wg.Wait()
	live.Stop()
	if appCtx.Err() != nil {
		return appCtx.Err()
	}
	if failed.Load() > 0 {
		return errReported
	}
	return nil
}

// subUpdateTimeout bounds each subscription's refresh, as the daemon's background
// refresh does: a direct fetch, a retry through the proxy, and parsing.
const subUpdateTimeout = 2 * time.Minute

func cmdSubRemove(a args) error {
	all, err := loadNodes()
	if err != nil {
		return err
	}
	var targets []subNodes
	for _, n := range a.pos {
		s, ok := findSub(all, n)
		if !ok {
			return fail("no subscription named %q", n)
		}
		targets = append(targets, s)
	}
	if len(targets) == 0 {
		if len(all) == 0 {
			ui.Info("No subscriptions to remove.")
			return nil
		}
		if !ui.Interactive() {
			return fail("name the subscriptions to remove: masir sub rm <name...>")
		}
		opts := make([]ui.Option[string], len(all))
		for i, s := range all {
			updated := "never updated"
			if s.sub.UpdatedAtMs > 0 {
				updated = "updated " + time.UnixMilli(s.sub.UpdatedAtMs).Format("2006-01-02")
			}
			opts[i] = ui.Option[string]{Label: s.sub.Name, Hint: fmt.Sprintf("%d configs · %s", s.sub.ConfigCount, updated), Value: s.sub.Id}
		}
		ids, err := ui.MultiSelect("Select subscriptions to remove", opts, nil, true)
		if err != nil {
			return err
		}
		for _, id := range ids {
			s, _ := findSub(all, id)
			targets = append(targets, s)
		}
		ok, err := ui.Confirm(fmt.Sprintf("Remove %d subscription%s? Rules pinned to their proxies fall back to the active one.", len(targets), plural(len(targets))), false)
		if err != nil || !ok {
			return err
		}
	}
	for _, s := range targets {
		if _, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.RemoveSubscriptionResponse, error) {
			return c.RemoveSubscription(ctx, &pb.RemoveSubscriptionRequest{Id: s.sub.Id})
		}); err != nil {
			return err
		}
		ui.Success(fmt.Sprintf("Removed %q.", ui.Visual(s.sub.Name)))
	}
	return nil
}

// findSub matches a subscription by name (case-insensitive) or id.
func findSub(all []subNodes, nameOrID string) (subNodes, bool) {
	for _, s := range all {
		if s.sub.Id == nameOrID || strings.EqualFold(s.sub.Name, nameOrID) {
			return s, true
		}
	}
	return subNodes{}, false
}

func validateURL(s string) error {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fail("enter an http:// or https:// URL")
	}
	return nil
}

func hostOf(s string) string {
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	return u.Hostname()
}

func minutes(m uint32) string {
	switch {
	case m%(60*24) == 0:
		return fmt.Sprintf("%dd", m/(60*24))
	case m%60 == 0:
		return fmt.Sprintf("%dh", m/60)
	default:
		return fmt.Sprintf("%dm", m)
	}
}

func indent(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}

// nodeTable lists a subscription's configs with the ids `masir use <id>` takes.
func nodeTable(s subNodes, activeCfg string) string {
	w := 0
	for _, c := range s.configs {
		w = max(w, ui.Width(ui.Visual(c.Name)))
	}
	var lines []string
	for _, c := range s.configs {
		mark := " "
		if c.Id == activeCfg {
			mark = ui.Green("●")
		}
		lines = append(lines, fmt.Sprintf("    %s %s  %s  %s  %s", mark, ui.Dim(c.Id), ui.Pad(ui.Visual(c.Name), w), ui.Dim(c.Protocol), latencyText(c.Latency)))
	}
	return strings.Join(lines, "\n")
}
