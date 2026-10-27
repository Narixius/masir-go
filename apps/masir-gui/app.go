package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo/ui"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/narixius/masir-go/libs/masir-core/client"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// screen is which page the panel shows.
type screen int

const (
	screenMain screen = iota
	screenSettings
	screenAddSubscription
)

// section is which part of the main page is open; the server list and the rules never
// are both.
type section int

const (
	sectionNone section = iota
	sectionServers
	sectionRules
)

// serverKey names a config across subscriptions.
type serverKey struct{ sub, cfg string }

// server is one config of a subscription, as the pickers list it.
type server struct {
	key     serverKey
	subName string
	cfg     *pb.SubscriptionConfig
}

// app is everything the panel shows and does. It is read and changed on the main thread
// only: the daemon's streams and calls hand their results over with post.
type app struct {
	// post runs fn on the main thread and redraws; async runs slow work off it. Tests
	// run both inline.
	post  func(fn func())
	async func(fn func())

	d      pb.DaemonServiceClient
	binary string // the masir executable to start a daemon from

	// What the daemon last said.
	reachable bool
	status    *pb.Status
	subs      []*pb.Subscription
	servers   []server
	rules     []*pb.RoutingRule
	lastRev   uint64
	down, up  uint64 // bytes per second

	// latency is the last result per server, in milliseconds; negative means a failed probe.
	latency map[serverKey]int64
	testing map[serverKey]bool

	// What the person is doing.
	screen    screen
	backTo    screen // where a page's back arrow goes
	open      section
	busy      string // what a running action is called, e.g. "Connecting…"
	query     string // search in the server list
	ruleQuery string
	notices   []string // toasts waiting for the next frame
	dismissed string   // the last_error the person closed

	// The add-subscription form.
	subURL, subName string
	subBusy         bool
	subError        string

	// Settings form.
	portText string

	// What the system says about starting at login, read once rather than on every frame
	// (the OS call is not free).
	loginOn      bool
	loginChecked bool

	// Install actions in Settings, and what the last check of the command-line tool said.
	serviceBusy bool
	cliBusy     bool
	cli         cliStatus
	cliChecked  bool

	trafficCancel context.CancelFunc
	since         time.Time // when masir last turned on, as far as this app saw

	holdID     string    // the subscription whose trash button is held
	holdStart  time.Time // since when
	portEdited bool      // the port field has been typed in

	trayOn bool // the tray shows the connected icon

	// Transitions and the window's height. The springs say how far each part is open; the
	// view steps them from the frame's clock, and the window is resized once per change,
	// never frame by frame.
	motion       motion
	lastMotion   time.Time          // the frame the springs last stepped on
	nearTarget   bool               // the window was already sized for a closing part
	lastSub      screen             // the page that slides over the main one, kept while it slides out
	mainHeight   float32            // the main page's height as last measured
	measuredOpen section            // the section that was open when mainHeight was measured
	baseHeight   float32            // the main page's height with both lists closed
	natural      map[string]float32 // the height each list has when fully open
	pend         pending            // changes shown ahead of the daemon

	// Rules: the swipe of each row, which row is open, the right-click menu, the app names
	// and icons, and where the list was last scrolled to.
	swipes      map[string]*swipe
	openSwipe   string
	menu        ruleMenu
	apps        *appInfos
	rulesScroll ui.ScrollState
	lastScrollY float32
	onHeight    func(float32) // tells the window how tall the content is at rest

	version string
	palette palette // the colors of the frame being built, for helpers that format text
}

func newApp(d pb.DaemonServiceClient, binary string) *app {
	return &app{
		d:       d,
		binary:  binary,
		post:    func(fn func()) { fn() },
		async:   func(fn func()) { go fn() },
		natural: map[string]float32{},
		pend:    newPending(),
		swipes:  map[string]*swipe{},
		apps:    newAppInfos(),
		latency: map[serverKey]int64{},
		testing: map[serverKey]bool{},
		version: version,
	}
}

// rpcTimeout bounds one daemon call; fetching a subscription can take a while.
const rpcTimeout = 90 * time.Second

func rpc() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), rpcTimeout)
}

// openScreen slides another page over the main one. A page opening from the main one gets
// the window's height before it slides; coming back, the window keeps its size until the
// page has gone, and aimMotion shrinks it then, so nothing is resized while it moves.
func (a *app) openScreen(s screen) {
	from := a.screen
	a.lastSub, a.screen = s, s
	if s == screenSettings {
		a.cliChecked = false  // look for the command-line tool again
		a.refreshLoginState() // and for the login item, in the background
	}
	// The back arrow returns to the page this one opened over: the settings page can open
	// its own add-subscription form, which then returns to it.
	if s == screenAddSubscription && from == screenSettings {
		a.backTo = screenSettings
	} else {
		a.backTo = screenMain
	}
	if from == screenMain {
		// A page opening from the main one: size the window before it slides. Coming back,
		// the window stays as it is until the page has gone, and aimMotion shrinks it then.
		a.requestHeight()
	}
}

// notify queues a toast for the next frame.
func (a *app) notify(msg string) {
	a.notices = append(a.notices, msg)
}

// fail tells the person what went wrong with an action.
func (a *app) fail(prefix string, err error) {
	a.notify(prefix + describe(err))
}

// describe turns a daemon error into one line for a toast.
func describe(err error) string {
	st, ok := status.FromError(err)
	if !ok {
		return err.Error()
	}
	switch {
	case st.Code() == codes.Unavailable && strings.Contains(st.Message(), "connection error"):
		return "can't reach the masir daemon"
	case st.Code() == codes.Unimplemented:
		return "the running masir daemon is older than this app; update masir"
	case client.ErrorReason(err) == "ROOT_REQUIRED":
		return "TUN needs the daemon to run as root"
	}
	return st.Message()
}

// ---- state from the daemon ----

// applyStatus takes a status from the stream and refetches the lists when something they
// show changed.
func (a *app) applyStatus(st *pb.Status) {
	wasOn := a.connected()
	a.reachable = true
	a.status = st
	a.pend.caughtUp()
	if a.connected() && !wasOn {
		a.since = time.Now()
	} else if !a.connected() {
		a.since = time.Time{}
	}
	if a.screen != screenSettings || a.portText == "" {
		a.portText = fmt.Sprint(st.GetSettings().GetProxyPort())
	}
	if a.lastRev == 0 || st.Revision != a.lastRev {
		a.lastRev = st.Revision
		a.refreshLists()
	}
}

func (a *app) setUnreachable() {
	a.reachable = false
	a.down, a.up = 0, 0
}

// refreshLists fetches subscriptions with their configs, and the rules.
func (a *app) refreshLists() {
	a.async(func() {
		ctx, cancel := rpc()
		defer cancel()
		subsRes, err := a.d.ListSubscriptions(ctx, &pb.ListSubscriptionsRequest{})
		if err != nil {
			return // the next status tries again
		}
		var servers []server
		for _, s := range subsRes.GetSubscriptions() {
			full, err := a.d.GetSubscription(ctx, &pb.GetSubscriptionRequest{Id: s.Id})
			if err != nil {
				continue
			}
			for _, cfg := range full.GetConfigs() {
				servers = append(servers, server{key: serverKey{s.Id, cfg.Id}, subName: s.Name, cfg: cfg})
			}
		}
		rulesRes, rerr := a.d.ListRoutingRules(ctx, &pb.ListRoutingRulesRequest{})
		a.post(func() {
			a.subs = subsRes.GetSubscriptions()
			a.servers = servers
			for _, s := range servers {
				if r := s.cfg.GetLatency(); r != nil && r.TestedAtMs > 0 && !a.testing[s.key] {
					a.latency[s.key] = latencyOf(r)
				}
			}
			if rerr == nil {
				a.rules = rulesRes.GetRules()
			}
		})
	})
}

// latencyOf is a result as one number: milliseconds, or -1 when the probe failed.
func latencyOf(r *pb.LatencyResult) int64 {
	if r.GetError() != "" {
		return -1
	}
	return r.GetLatencyMs()
}

// active is the selected server, if the lists have it.
func (a *app) active() (server, bool) {
	st := a.status
	k, ok := a.activeKey()
	if !ok {
		return server{}, false
	}
	for _, s := range a.servers {
		if s.key == k {
			return s, true
		}
	}
	// The selection is gone from its subscription: the daemon keeps running a copy.
	return server{key: k, subName: "", cfg: &pb.SubscriptionConfig{Id: k.cfg, Name: st.GetActiveConfigName()}}, true
}

// filteredServers are the servers matching the search, in the order of their subscriptions.
func (a *app) filteredServers() []server {
	q := strings.ToLower(strings.TrimSpace(a.query))
	if q == "" {
		return a.servers
	}
	var out []server
	for _, s := range a.servers {
		hay := strings.ToLower(s.cfg.Name + " " + s.subName + " " + s.cfg.Protocol + " " + s.cfg.Address)
		if strings.Contains(hay, q) {
			out = append(out, s)
		}
	}
	return out
}

// filteredRules are the rules matching the search.
func (a *app) filteredRules() []*pb.RoutingRule {
	q := strings.ToLower(strings.TrimSpace(a.ruleQuery))
	rules := a.ruleList()
	if q == "" {
		return rules
	}
	var out []*pb.RoutingRule
	for _, r := range rules {
		if strings.Contains(strings.ToLower(r.RuleTag+" "+r.Target+" "+r.RuleJson), q) {
			out = append(out, r)
		}
	}
	return out
}

// ---- traffic ----

// watchTraffic streams speeds while the panel is open.
func (a *app) watchTraffic() {
	a.stopTraffic()
	ctx, cancel := context.WithCancel(context.Background())
	a.trafficCancel = cancel
	a.async(func() {
		for ctx.Err() == nil {
			stream, err := a.d.WatchTraffic(ctx, &pb.WatchTrafficRequest{IntervalMs: 1000}, grpc.WaitForReady(false))
			if err == nil {
				for {
					res, err := stream.Recv()
					if err != nil {
						break
					}
					a.post(func() { a.down, a.up = res.DownlinkBps, res.UplinkBps })
				}
			}
			a.post(func() { a.down, a.up = 0, 0 })
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
	})
}

func (a *app) stopTraffic() {
	if a.trafficCancel != nil {
		a.trafficCancel()
		a.trafficCancel = nil
	}
}

// ---- actions ----

// connected reports whether masir is on and the daemon agrees.
func (a *app) connected() bool {
	return a.status != nil && a.status.Settings.GetPower() && a.status.XrayRunning
}

// running reports whether the person asked for masir on, whatever Xray says yet.
func (a *app) wantsOn() bool { return a.status != nil && a.status.Settings.GetPower() }

func (a *app) setPower(on bool) {
	if a.busy != "" {
		return
	}
	a.busy = map[bool]string{true: "Connecting…", false: "Disconnecting…"}[on]
	a.async(func() {
		err := a.power(on)
		a.post(func() {
			a.busy = ""
			if err != nil {
				a.fail("Couldn't "+map[bool]string{true: "connect", false: "disconnect"}[on]+": ", err)
			}
		})
	})
}

func (a *app) power(on bool) error {
	if on && !a.privileged() {
		if err := a.becomePrivileged(); err != nil {
			return err
		}
	}
	ctx, cancel := rpc()
	defer cancel()
	if _, err := a.d.SetPower(ctx, &pb.SetPowerRequest{On: on}); err != nil {
		return err
	}
	if _, err := a.d.SetTunEnabled(ctx, &pb.SetTunEnabledRequest{Enabled: on}); err != nil && on {
		return fmt.Errorf("masir is on in proxy mode only: %s", describe(err))
	}
	return nil
}

func (a *app) privileged() bool { return a.status != nil && a.status.Privileged }

// becomePrivileged restarts the daemon as root for this session (see elevate), and waits
// for it to take over.
func (a *app) becomePrivileged() error {
	if err := elevateDaemon(a.binary); err != nil {
		return err
	}
	return client.WaitFor(context.Background(), func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		res, err := a.d.GetStatus(ctx, &pb.GetStatusRequest{}, grpc.WaitForReady(true))
		return err == nil && res.Status.Privileged
	}, 30*time.Second, "the daemon didn't start with root permission")
}

func (a *app) updateSettings(req *pb.UpdateSettingsRequest) {
	a.run("Couldn't save the setting: ", func(ctx context.Context) error {
		_, err := a.d.UpdateSettings(ctx, req)
		return err
	})
}

func (a *app) removeSubscription(id string) {
	a.run("Couldn't remove the subscription: ", func(ctx context.Context) error {
		_, err := a.d.RemoveSubscription(ctx, &pb.RemoveSubscriptionRequest{Id: id})
		return err
	})
}

func (a *app) refreshSubscription(id string) {
	a.run("Couldn't refresh the subscription: ", func(ctx context.Context) error {
		_, err := a.d.UpdateSubscription(ctx, &pb.UpdateSubscriptionRequest{Id: id})
		return err
	})
}

// run does a daemon call off the main thread; the status stream shows its effect, and a
// failure becomes a toast.
func (a *app) run(failPrefix string, fn func(ctx context.Context) error) {
	a.async(func() {
		ctx, cancel := rpc()
		defer cancel()
		if err := fn(ctx); err != nil {
			a.post(func() { a.fail(failPrefix, err) })
			return
		}
		a.post(a.refreshLists) // the lists may have changed before the status says so
	})
}

func (a *app) addSubscription() {
	url, name := strings.TrimSpace(a.subURL), strings.TrimSpace(a.subName)
	if url == "" || a.subBusy {
		return
	}
	a.subBusy, a.subError = true, ""
	a.async(func() {
		ctx, cancel := rpc()
		defer cancel()
		_, err := a.d.AddSubscription(ctx, &pb.AddSubscriptionRequest{Url: url, Name: name})
		a.post(func() {
			a.subBusy = false
			if err != nil {
				a.subError = describe(err)
				return
			}
			a.subURL, a.subName = "", ""
			a.screen = screenMain
			a.open = sectionServers
			a.requestHeight()
			a.refreshLists()
		})
	})
}

// ---- latency ----

// testServers measures the given servers, or all of them when none are given; results
// arrive as they finish.
func (a *app) testServers(only ...server) {
	targets := only
	if len(targets) == 0 {
		targets = a.servers
	}
	bySub := map[string][]string{}
	for _, s := range targets {
		if a.testing[s.key] {
			continue
		}
		a.testing[s.key] = true
		bySub[s.key.sub] = append(bySub[s.key.sub], s.key.cfg)
	}
	subIDs := make([]string, 0, len(bySub))
	for id := range bySub {
		subIDs = append(subIDs, id)
	}
	sort.Strings(subIDs)
	for _, id := range subIDs {
		cfgs := bySub[id]
		a.async(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			stream, err := a.d.TestSubscriptionLatency(ctx, &pb.TestSubscriptionLatencyRequest{SubscriptionId: id, ConfigIds: cfgs})
			done := map[string]bool{}
			if err == nil {
				for {
					res, err := stream.Recv()
					if err != nil {
						break
					}
					k := serverKey{id, res.ConfigId}
					done[res.ConfigId] = true
					a.post(func() {
						a.latency[k] = latencyOf(res.Result)
						delete(a.testing, k)
					})
				}
			}
			a.post(func() { // whatever didn't answer is no longer being measured
				for _, cfg := range cfgs {
					if !done[cfg] {
						delete(a.testing, serverKey{id, cfg})
					}
				}
			})
		})
	}
}

// ---- the daemon's lifecycle ----

// watch keeps a status stream open for as long as ctx lives: it starts the daemon when
// none answers, and dials again when the daemon goes away or is replaced (an update, or
// a root daemon taking over).
func (a *app) watch(ctx context.Context) {
	var once sync.Once
	warn := func(msg string) {
		once.Do(func() { a.post(func() { a.notify(msg) }) })
	}
	for ctx.Err() == nil {
		if !client.Alive() {
			a.post(a.setUnreachable)
			if a.binary == "" {
				warn("Couldn't start the masir daemon: the app ships no masir executable")
				sleep(ctx, 5*time.Second)
				continue
			}
			if err := client.Start(ctx, a.binary, true); err != nil {
				warn("Couldn't start the masir daemon: " + err.Error())
				sleep(ctx, 3*time.Second)
				continue
			}
		}
		stream, err := a.d.WatchStatus(ctx, &pb.WatchStatusRequest{}, grpc.WaitForReady(true))
		if err != nil {
			sleep(ctx, time.Second)
			continue
		}
		for {
			res, err := stream.Recv()
			if err != nil {
				break
			}
			a.post(func() { a.applyStatus(res.Status) })
		}
		a.post(a.setUnreachable)
		sleep(ctx, time.Second)
	}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// ---- motion ----

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

// aimMotion points the springs at where the state says things should be, steps them by
// the frame's own time, and asks for the next frame while they move. The frame clock is
// the display's, so the view and the window change together and only as often as they
// can be seen. A frame may build the view more than once; only its first pass steps the
// springs.
func (a *app) aimMotion(c *ui.Context) {
	changed := a.motion.aim(b2f(a.open == sectionServers), b2f(a.open == sectionRules), b2f(a.screen != screenMain))
	if changed {
		a.nearTarget = false
	}
	now := c.Now()
	moving := !a.motion.settled()
	switch {
	case a.lastMotion.IsZero():
		a.lastMotion = now
	case now.After(a.lastMotion):
		dt := float32(min(now.Sub(a.lastMotion).Seconds(), 0.05))
		a.lastMotion = now
		a.motion.step(dt)
		// The page over the main one gives the window its height back as soon as it is
		// nearly gone, instead of after the spring's last invisible millimetres: the
		// window would otherwise shrink a moment after the movement the person saw. A
		// folding list is sized as it starts (see toggle), so it needs no wait.
		if a.motion.pageClosing() && !a.nearTarget && abs32(a.motion.page.x) <= 0.05 {
			a.nearTarget = true
			a.requestHeight()
		}
		if moving && a.motion.settled() {
			// The content has come to rest: its exact height may differ from the estimate
			// it grew to, so settle the window on it once, now that nothing moves.
			a.requestHeight()
		}
	}
	if !a.motion.settled() {
		c.AnimationFrame()
	}
}

// requestHeight tells the window how tall the content will be once at rest. Call it when
// the state changes: the shell coalesces it and applies it after the frame, never inside
// one.
func (a *app) requestHeight() {
	if a.onHeight != nil {
		a.onHeight(a.restHeight())
	}
}

// naturalHeight is how tall a list is when fully open: as measured the last time it was
// shown, else an estimate from what it holds.
func (a *app) naturalHeight(key string) float32 {
	if h := a.natural[key]; h > 0 {
		return h
	}
	switch key {
	case "servers":
		rows := float32(len(a.filteredServers()))
		return 46 + min(300, rows*42+8)
	default:
		rows := float32(len(a.rules))
		return 46 + min(167, rows*44+60)
	}
}

// restHeight is how tall the panel is with everything at rest: the page shown, with an
// open list as tall as it measured (or is estimated to be just before it opens).
func (a *app) restHeight() float32 {
	if a.screen != screenMain {
		return a.subHeight()
	}
	if a.mainHeight > 0 && a.open == a.measuredOpen && a.motion.listsSettled() {
		return a.mainHeight // the content itself says how tall it is
	}
	base := a.baseHeight
	if base == 0 {
		base = 262
	}
	if a.open == sectionServers {
		base += a.naturalHeight("servers")
	}
	if a.open == sectionRules {
		base += a.naturalHeight("rules")
	}
	return base
}
