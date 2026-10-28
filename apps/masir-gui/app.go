package main

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	search  string // lowercased text the search matches, including the flag's country
}

// textSetting is a settings field being typed in: the text it shows, and whether it
// differs from the daemon's value because the person is editing it.
type textSetting struct {
	text   string
	edited bool
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
	// lastServiceInstalled is what the last status said, so a service installed or
	// removed while the app runs can make binary point at the copy that exists.
	lastServiceInstalled bool
	// serviceChecked is when the app last asked the OS service manager itself. The
	// daemon reports it too, but a stopped service has no daemon to ask.
	serviceChecked time.Time

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
	portText     string // digits only; synced from the daemon when not editing
	portEdited   bool   // the port field has been typed in
	dns          textSetting
	tunDNS       textSetting
	noticeMarked bool // the analytics notice has been shown and marked

	// What the system says about starting at login, read once rather than on every frame
	// (the OS call is not free).
	loginOn            bool
	loginChecked       bool
	loginNeedsApproval bool // the system has the item but needs the person to allow it

	// Install actions in Settings, and what the last check of the command-line tool said.
	serviceBusy bool
	cliBusy     bool
	cli         cliStatus
	cliChecked  bool
	cliFile     string // where the automatic install records done or off

	// Updates: the daemon's release check, the app's signed update, and the job
	// downloading either.
	upd updateState

	trafficCancel context.CancelFunc
	since         time.Time // when masir last turned on, as far as this app saw

	trayOn bool // the tray shows the connected icon

	// Transitions and the window's height. The springs say how far each part is open; the
	// view steps them from the frame's clock, and the window is resized every frame while
	// it moves, following them as a window of its own does.
	motion       motion
	lastMotion   time.Time          // the frame the springs last stepped on
	lastSize     time.Time          // when a window that follows the springs last asked for its height
	frameSize    bool               // the window can follow the springs every frame
	warmPage     bool               // the page opening is built this frame, before it shows
	warmed       map[screen]bool    // and the pages that were built that way
	lastSub      screen             // the page that slides over the main one, kept while it slides out
	mainHeight   float32            // the main page's height as last measured
	measuredOpen section            // the section that was open when mainHeight was measured
	baseHeight   float32            // the main page's height with both lists closed
	savedHeight  float32            // the base height last written to heightFile
	heightFile   string             // where the drawn height is remembered across launches
	heightWrites atomic.Uint64      // the scheduled height write that still stands
	heightMu     sync.Mutex         // orders a scheduled write with one at quit
	askedHeight  float32            // the height last given to the window, so data arriving at rest resizes it once
	natural      map[string]float32 // the height each list has when fully open
	serverListH  float32            // the server list's height with no search, so filtering keeps it
	ruleListH    float32            // the rules list's height with no search, so filtering keeps it
	pend         pending            // changes shown ahead of the daemon

	// Rules: the swipe of each row, which row is open, the right-click menu, the app names
	// and icons, and where the list was last scrolled to.
	swipes      map[string]*swipe
	openSwipe   string
	menu        ruleMenu
	apps        *appInfos
	rulesMetas  map[string]ruleMeta
	rulesList   ui.ListState
	ruleItems   []ruleListItem
	lastRuleTop int
	lastRuleEnd int
	serversList ui.ListState
	serverItems []serverListItem
	onHeight    func(float32) // tells the window how tall the content is at rest

	version string
	palette palette // the colors of the frame being built, for helpers that format text
}

func newApp(d pb.DaemonServiceClient, binary string) *app {
	a := &app{
		d:           d,
		binary:      binary,
		post:        func(fn func()) { fn() },
		async:       func(fn func()) { go fn() },
		natural:     map[string]float32{},
		pend:        newPending(),
		swipes:      map[string]*swipe{},
		apps:        newAppInfos(),
		latency:     map[serverKey]int64{},
		testing:     map[serverKey]bool{},
		rulesMetas:  map[string]ruleMeta{},
		warmed:      map[screen]bool{},
		lastRuleTop: -1,
		lastRuleEnd: -1,
		version:     version,
	}
	a.rulesList.Key = func(i int) any {
		if i < 0 || i >= len(a.ruleItems) {
			return nil
		}
		it := a.ruleItems[i]
		if it.rule == nil {
			return "h:" + it.title
		}
		return it.rule.RuleTag
	}
	a.rulesList.Header = func(i int) bool {
		return i >= 0 && i < len(a.ruleItems) && a.ruleItems[i].rule == nil
	}
	a.rulesList.Label = func(i int) string {
		if i < 0 || i >= len(a.ruleItems) {
			return ""
		}
		it := a.ruleItems[i]
		if it.rule == nil {
			return it.title
		}
		return a.metaFor(it.rule).info.title
	}
	a.serversList.Key = func(i int) any {
		if i < 0 || i >= len(a.serverItems) {
			return nil
		}
		it := a.serverItems[i]
		if it.group != nil {
			return "g:" + it.group.name
		}
		return it.srv.key
	}
	a.serversList.Header = func(i int) bool {
		return i >= 0 && i < len(a.serverItems) && a.serverItems[i].group != nil
	}
	a.serversList.Label = func(i int) string {
		if i < 0 || i >= len(a.serverItems) {
			return ""
		}
		it := a.serverItems[i]
		if it.group != nil {
			return it.group.name
		}
		_, title, _ := serverTitle(it.srv.cfg.GetName())
		return title
	}
	return a
}

// metaFor returns what a rule shows, parsing its JSON once for as long as the rule is the
// same. The rules list calls it for every rule in a frame; parsing there was the cost of
// the first open.
func (a *app) metaFor(r *pb.RoutingRule) ruleMeta {
	if m, ok := a.rulesMetas[r.RuleTag]; ok && m.json == r.RuleJson {
		return m
	}
	m := ruleMeta{
		json:   r.RuleJson,
		info:   ruleInfo(r),
		search: strings.ToLower(r.RuleTag + " " + r.Target + " " + r.RuleJson),
	}
	a.rulesMetas[r.RuleTag] = m
	return m
}

// indexRules parses every rule now, off the frames that show the list.
func (a *app) indexRules() {
	for _, r := range a.rules {
		a.metaFor(r)
	}
}

// prefetchAppIcons reads the icons of application rules in the background, before the
// rules list is opened, one at a time.
func (a *app) prefetchAppIcons() {
	for _, r := range a.rules {
		if p := a.metaFor(r).info.appPath; p != "" {
			a.apps.lookup(p, a.async, func() { a.post(func() {}) })
		}
	}
}

// rpcTimeout bounds one daemon call; fetching a subscription can take a while.
const rpcTimeout = 90 * time.Second

func rpc() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), rpcTimeout)
}

// quitTimeout bounds the disconnect a quit sends: the daemon answers on a local socket in
// milliseconds, and a quit must never hang on one that doesn't answer at all.
const quitTimeout = 2 * time.Second

// openScreen slides another page over the main one. A page opening from the main one gets
// the window's height before it slides; coming back, the window keeps its size until the
// page has gone, and aimMotion shrinks it then, so nothing is resized while it moves.
func (a *app) openScreen(s screen) {
	a.lastSub, a.screen = s, s
	if s == screenSettings {
		a.refreshCLIState()     // check the command-line tool in the background
		a.refreshLoginState()   // the login item
		a.refreshServiceState() // and the service, which may be stopped
		a.syncTextSettings(a.status.GetSettings())
		a.markAnalyticsNotice()
	}
	// The back arrow returns to the page this one opened over: the add-subscription form
	// opens from the main page and returns there.
	a.backTo = screenMain
	// The window takes the new page's height as the pages move, from the first frame, so
	// the two animations are one.
	a.requestHeight()
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
	if st.GetServiceInstalled() != a.lastServiceInstalled {
		a.lastServiceInstalled = st.GetServiceInstalled()
		a.binary = daemonBinary()
	}
	a.pend.caughtUp()
	if a.connected() && !wasOn {
		a.since = time.Now()
	} else if !a.connected() {
		a.since = time.Time{}
	}
	// The fields on the settings page are only filled while the page isn't being
	// edited: a status arriving mid-typing must not overwrite what was typed.
	if a.screen != screenSettings {
		a.syncTextSettings(st.GetSettings())
	}
	if a.lastRev == 0 || st.Revision != a.lastRev {
		a.lastRev = st.Revision
		a.refreshLists()
	}
	a.observeUpdates(st)
}

func (a *app) setUnreachable() {
	a.reachable = false
	a.down, a.up = 0, 0
	a.refreshServiceState()
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
				servers = append(servers, server{
					key:     serverKey{s.Id, cfg.Id},
					subName: s.Name,
					cfg:     cfg,
					search:  serverSearchText(cfg.GetName(), s.Name, cfg),
				})
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
				a.indexRules()
				a.prefetchAppIcons()
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

// syncTextSettings puts the daemon's values in the settings fields. It is called when the
// page opens and while it is closed, never in the middle of typing.
func (a *app) syncTextSettings(s *pb.Settings) {
	if s == nil {
		return
	}
	a.portText = fmt.Sprint(s.GetProxyPort())
	a.dns = textSetting{text: strings.Join(s.GetDnsServers(), ", ")}
	a.tunDNS = textSetting{text: strings.Join(s.GetTunDns(), ", ")}
}

// markAnalyticsNotice records that the app has shown the analytics notice, as the
// command line does. The daemon sends nothing before this marker exists.
func (a *app) markAnalyticsNotice() {
	if a.noticeMarked {
		return
	}
	a.noticeMarked = true
	a.async(markNoticeShown)
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

// serverSearchText is everything the picker's search matches: the provider's own name,
// the country its flag stands for, the subscription, protocol, address and country code.
// It is built when the lists load, not on every frame of typing.
func serverSearchText(name, sub string, cfg *pb.SubscriptionConfig) string {
	flag, title, detail := serverTitle(name)
	parts := []string{name, title, detail, sub, cfg.GetProtocol(), cfg.GetAddress()}
	if code := emojiCode(flag); code != "" {
		parts = append(parts, code)
	}
	return strings.ToLower(strings.Join(parts, " "))
}

// filteredServers are the servers matching the search, in the order of their subscriptions.
func (a *app) filteredServers() []server {
	q := strings.ToLower(strings.TrimSpace(a.query))
	if q == "" {
		return a.servers
	}
	var out []server
	for _, s := range a.servers {
		hay := s.search
		if hay == "" { // a server made without the list, as in a test
			hay = serverSearchText(s.cfg.GetName(), s.subName, s.cfg)
		}
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
		if strings.Contains(a.metaFor(r).search, q) {
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
	if a.busy != "" || a.serviceBusy {
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

// The socket check and the permission-prompting commands, replaceable in tests so they
// never touch the machine they run on.
var (
	daemonAlive     = client.Alive
	startRootDaemon = elevateDaemon
	startServiceNow = startService
)

func (a *app) power(on bool) error {
	if on && !a.connectable() {
		if err := a.startDaemon(); err != nil {
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

// disconnectOnQuit turns masir off as `masir disconnect` does when the app is really
// going away. Hiding the window in the tray is not a quit, and an update's relaunch is
// not one either: the app comes back, so neither turns masir off.
func (a *app) disconnectOnQuit() {
	if a.upd.relaunching || !a.wantsOn() {
		return
	}
	// Only power, so Always-on Proxy keeps its port up. A daemon failure here must not
	// hold the quit (it may already be gone).
	ctx, cancel := context.WithTimeout(context.Background(), quitTimeout)
	defer cancel()
	if _, err := a.d.SetPower(ctx, &pb.SetPowerRequest{On: false}); err != nil {
		log.Print("disconnect on quit: ", err)
	}
}

func (a *app) privileged() bool { return a.status != nil && a.status.Privileged }

// connectable reports whether a privileged daemon answers right now. The socket check
// catches a daemon that stopped since the last status; the status itself is main-thread
// only, hence the hop.
func (a *app) connectable() bool {
	if !daemonAlive() {
		return false
	}
	var ok bool
	a.post(func() { ok = a.privileged() })
	return ok
}

// startDaemon brings up a privileged daemon to connect through. An installed service is
// the supervisor, so it is started (asking for permission) instead of a second,
// unsupervised daemon. Without a service, this session gets a root daemon that takes over
// from the one running as the user.
func (a *app) startDaemon() error {
	if readService().Installed {
		// Ask the service manager rather than trust its cached state: the service may
		// have stopped a moment ago, and a kickstart is harmless when it is up.
		if !daemonAlive() {
			if err := a.runServiceAction(); err != nil {
				return err
			}
		}
		return a.waitForRoot("the background service didn't start with root permission")
	}
	return a.becomePrivileged()
}

// runServiceAction starts the installed service, keeping the daemon watch from starting
// one of its own while the service comes up.
func (a *app) runServiceAction() error {
	a.post(func() { a.serviceBusy = true })
	err := startServiceNow(a.daemonPath())
	a.post(func() {
		a.serviceBusy = false
		a.serviceChecked = time.Time{}
		a.refreshServiceState()
	})
	return err
}

// becomePrivileged restarts the daemon as root for this session (see elevate), and waits
// for it to take over.
func (a *app) becomePrivileged() error {
	if err := startRootDaemon(a.daemonPath()); err != nil {
		return err
	}
	return a.waitForRoot("the daemon didn't start with root permission")
}

// waitForRoot waits until the daemon answers and reports that it runs as root.
func (a *app) waitForRoot(failure string) error {
	return client.WaitFor(context.Background(), func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		res, err := a.d.GetStatus(ctx, &pb.GetStatusRequest{}, grpc.WaitForReady(true))
		return err == nil && res.Status.Privileged
	}, 30*time.Second, failure)
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

// daemonStep is what the daemon watch does when nothing answers.
type daemonStep int

const (
	// daemonWait: an update or a service action is in flight. Its daemon is expected to
	// be down for a moment, and starting another would race it for the socket.
	daemonWait daemonStep = iota
	// daemonLeaveToService: a service definition exists. It is the supervisor, not this
	// app, so the daemon is left to it; connecting starts a stopped service.
	daemonLeaveToService
	// daemonStart: no service and no daemon, so the app starts one.
	daemonStart
)

// daemonStep answers the watch from another goroutine. The update and service state is
// main-thread only, so the question is asked there and the answer carried back; the
// service definition is read where it stands, so no OS query runs in a frame.
func (a *app) daemonStep() daemonStep {
	var busy bool
	a.post(func() { busy = a.busyUpdate() || a.upd.restarting || a.serviceBusy })
	switch {
	case busy:
		return daemonWait
	case serviceDefined():
		return daemonLeaveToService
	}
	return daemonStart
}

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
			switch a.daemonStep() {
			case daemonWait:
				// An update or a service action is restarting the daemon. Starting
				// another one here would race it for the socket, and an idle start
				// would look like the person turned masir off.
				sleep(ctx, time.Second)
				continue
			case daemonLeaveToService:
				// A service exists: it is the supervisor, not this app. Starting a
				// second, unsupervised daemon would fight it for the socket.
				sleep(ctx, 3*time.Second)
				continue
			}
			bin := a.daemonPath()
			if bin == "" {
				warn("Couldn't start the masir daemon: the app ships no masir executable")
				sleep(ctx, 5*time.Second)
				continue
			}
			// Normal mode, not idle: the daemon honors the saved power and tunnel
			// settings, so an app restart doesn't silently turn masir off.
			if err := client.Start(ctx, bin, false); err != nil {
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
	pageTarget := b2f(a.screen != screenMain)
	changed := a.motion.aim(b2f(a.open == sectionServers), b2f(a.open == sectionRules), pageTarget)
	now := c.Now()
	first := a.lastMotion.IsZero() || now.After(a.lastMotion)
	if first {
		// A page opening shows for the first time: hold its spring for this frame and build
		// it unseen instead, so its first layout and paint are not the frame that starts
		// the slide.
		if !a.motion.instant && pageTarget == 1 && a.motion.page.x == 0 && !a.warmed[a.screen] {
			a.warmed[a.screen] = true
			a.warmPage = true
			a.lastMotion = now
			c.AnimationFrame()
			return
		}
		a.warmPage = false
	}
	moving := !a.motion.settled()
	switch {
	case a.lastMotion.IsZero():
		a.lastMotion = now
	case now.After(a.lastMotion):
		dt := float32(min(now.Sub(a.lastMotion).Seconds(), 0.05))
		a.lastMotion = now
		a.motion.step(dt)
		switch {
		case a.frameSize && !a.motion.settled():
			// The window follows the springs as they move: the height grows and shrinks
			// with the page or list instead of jumping to the end. Resizing the window makes
			// MyGo draw a frame at once, which builds the view again; ask for a height at
			// most every 6 ms, under a 120 Hz frame, so those draws don't chase each other
			// but every frame of the springs still moves the window.
			if a.onHeight != nil && (a.lastSize.IsZero() || now.Sub(a.lastSize) >= 6*time.Millisecond) {
				a.lastSize = now
				a.onHeight(a.windowHeight())
			}
		case moving && a.motion.settled():
			// The content has come to rest: its exact height may differ from the estimate
			// it grew to, so settle the window on it once, now that nothing moves.
			a.requestHeight()
		}
	}
	if a.frameSize && changed && a.motion.settled() && a.onHeight != nil {
		// An instant change (tests) has nothing to follow; ask for the exact height.
		a.requestHeight()
	}
	if !a.motion.settled() {
		c.AnimationFrame()
	}
}

// settleHeight gives the window the height the page just drew, when nothing is moving.
// Data arrives while the panel is open (a status, subscriptions, a banner, the update
// control) and nothing springs, so without this the window would keep the height from
// the frame that opened it. Layers call it after they have recorded their bounds.
func (a *app) settleHeight() {
	if a.onHeight == nil || !a.motion.settled() {
		return
	}
	if a.willMove() {
		// The click that just changed the page or a list is drawn in this frame with
		// the old springs; the height it measured belongs to the state that is leaving.
		// The transition asks for the target height, and the frame that settles asks
		// for the exact one.
		return
	}
	if h := a.restHeight(); h > 0 && abs32(h-a.askedHeight) > 0.5 {
		a.askedHeight = h
		a.onHeight(h)
	}
}

// requestHeight tells the window how tall the content will be once at rest. On a window
// that follows the springs, a transition is sized frame by frame instead, so the window and
// the content move together; only a change with no movement is asked for here.
func (a *app) requestHeight() {
	if a.onHeight == nil {
		return
	}
	if a.frameSize && a.willMove() {
		return
	}
	h := a.restHeight()
	a.askedHeight = h
	a.onHeight(h)
}

// willMove reports whether the state asks for a spring to move that is not there yet.
func (a *app) willMove() bool {
	return a.motion.servers.x != b2f(a.open == sectionServers) ||
		a.motion.rules.x != b2f(a.open == sectionRules) ||
		a.motion.page.x != b2f(a.screen != screenMain)
}

// windowHeight is the window's height for the springs as they are: the main page with its
// lists as far open as they are, blended with the page sliding over it. It is what a
// window that follows the animation every frame is set to, so the window grows and shrinks
// with the content instead of jumping to the end.
func (a *app) windowHeight() float32 {
	base := a.baseHeight
	if base == 0 {
		base = defaultPanelHeight
	}
	main := base + a.motion.servers.x*a.naturalHeight("servers") + a.motion.rules.x*a.naturalHeight("rules")
	if a.motion.settled() && a.mainHeight > 0 {
		main = a.mainHeight
	}
	return main + (a.subHeight()-main)*a.motion.page.x
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
		base = defaultPanelHeight
	}
	if a.open == sectionServers {
		base += a.naturalHeight("servers")
	}
	if a.open == sectionRules {
		base += a.naturalHeight("rules")
	}
	return base
}
