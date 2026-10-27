package main

import (
	"fmt"
	"math"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// view is the panel's content: the page the person is on, over the system's material.
func (a *app) view(c *ui.Context) {
	p := paletteFor(c)
	a.palette = p
	c.Root().Background(ui.Transparent)

	for _, msg := range a.notices {
		c.Toast(msg)
	}
	a.notices = nil

	root := ui.Box(c).Fill().Background(p.rootTint).TextColor(p.text).FontSize(sizeBody).FixedLineHeight(18).Clip()
	if !c.Vibrancy() { // no material under the panel: draw its own
		root.Background(c.Theme().Surface)
		if runtime.GOOS == "darwin" { // the popover's own; a window has the system's frame
			root.Radius(panelRadius).Border(1, p.borderDefault)
		}
	}
	root.Children(func() {
		// The pages slide: the main page leaves to the left as the other arrives from the
		// right, each fading with the move. A page opening is built once before it shows,
		// so its first layout and paint are not a cold frame in the middle of the slide.
		a.aimMotion(c)
		page := a.motion.page.x
		if page < 1 {
			opacity := float32(1 - page)
			if a.warmPage { // the page opening is still under the main one
				opacity = 1
			}
			a.mainLayer(c, p, -page*panelWidth).Opacity(opacity)
		}
		if page > 0 || a.warmPage {
			opacity, x := page, (1-page)*panelWidth
			if a.warmPage {
				opacity, x = 0.002, 0 // paint it, all but unseen, to shape its glyphs too
			}
			a.subLayer(c, p, x).Opacity(opacity)
		}
		a.contextMenu(c, p)
	})
}

func (a *app) mainLayer(c *ui.Context, p palette, x float32) ui.Element {
	layer := ui.Box(c.Key("main-layer")).Absolute().Top(0).Left(0).Width(panelWidth).Translate(x, 0)
	layer.Children(func() {
		col := ui.Column(c.Key("main-col")).Padding(16, 16, 12).Gap(tw(2.5)).Shrink(0)
		col.Children(func() {
			a.header(c, p)
			a.banner(c, p)
			if a.reachable && len(a.servers) == 0 {
				a.emptyState(c, p)
			} else {
				a.modeSection(c, p)
				a.routingCard(c, p)
			}
			a.footer(c, p)
		})
		if h := col.Bounds().H; h > 0 {
			a.mainHeight, a.measuredOpen = h, a.open
			if a.motion.servers.x == 0 && a.motion.rules.x == 0 {
				a.baseHeight = h
			}
		}
	})
	return layer
}

func (a *app) subLayer(c *ui.Context, p palette, x float32) ui.Element {
	if a.screen != screenMain {
		a.lastSub = a.screen
	}
	layer := ui.Column(c.Key("sub-layer")).Absolute().Top(0).Left(0).Width(panelWidth).Height(a.subHeight()).Translate(x, 0)
	layer.Children(func() {
		switch a.lastSub {
		case screenAddSubscription:
			a.addSubscriptionScreen(c, p)
		default:
			a.settingsScreen(c, p)
		}
	})
	return layer
}

func (a *app) subHeight() float32 {
	if a.lastSub == screenAddSubscription {
		return addSubscriptionHeight
	}
	return settingsHeight
}

// ---- header ----

func (a *app) statusText() (title, subtitle string, color ui.Color, pulse bool) {
	p := a.palette
	switch {
	case !a.reachable:
		return "Starting…", "Waiting for the masir daemon", p.degraded, true
	case a.busy != "":
		return a.busy, "Starting core...", p.degraded, true
	case a.connected():
		sub := "Just connected"
		if !a.since.IsZero() {
			sub = duration(time.Since(a.since))
		}
		return "Connected", sub, p.healthy, false
	case a.wantsOn():
		return "Connecting...", "Starting core...", p.degraded, true
	}
	return "Disconnected", "Ready to connect", p.neutral, false
}

func (a *app) header(c *ui.Context, p palette) {
	title, sub, color, pulse := a.statusText()
	connecting := pulse
	ui.Row(c).Padding(0, tw(0.5), tw(0.5)).Gap(tw(2)).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
		ui.Row(c).AlignItems(ui.Start).Gap(tw(2)).Children(func() {
			dot := ui.Box(c).Size(tw(2.5), tw(2.5)).Radius(tw(1.25)).Margin(tw(1.5), 0, 0).Shrink(0)
			dot.Background(dot.AnimateColor("c", color, appMotion(motionDuration)))
			switch {
			case connecting:
				dot.Shadow(0, 0, 8, 0, p.degraded.Alpha(0.7))
				dot.Opacity(1 - 0.5*triangle(dot.Loop("pulse", 2*time.Second, ui.Linear)))
			case a.connected():
				dot.Shadow(0, 0, 8, 0, p.healthy.Alpha(0.7))
				t := triangle(dot.Loop("breathe", 2400*time.Millisecond, ui.Linear))
				dot.Scale(1+0.15*t, 1+0.15*t).Opacity(1 - 0.18*t)
			}
			ui.Column(c).MinHeight(31).Justify(ui.Center).Children(func() {
				label(c, title, sizeTitle, 17, 600, p.text).SingleLine()
				subColor := p.muted
				if connecting {
					subColor = p.degraded
				}
				w := 400
				if connecting {
					w = 500
				}
				label(c, sub, sizeCaption, 14, w, subColor).FontFeatures("tnum").SingleLine()
			})
		})
		ui.Row(c).AlignItems(ui.Center).Gap(tw(2.5)).Children(func() {
			on := a.wantsOn() && a.connected() || a.busy == "Connecting…"
			disabled := a.busy != "" || !a.reachable || len(a.servers) == 0
			if next, changed := nativeSwitch(c, "power", "Connect", on, switchDefault, disabled, a.busy != "", p); changed {
				a.setPower(next)
			}
			if iconButton(c, "add", iconPlus, "Add Subscription", p, iconSM, variantGhost, 18) {
				a.openScreen(screenAddSubscription)
			}
		})
	})
}

// triangle turns a rising 0 to 1 into 0 to 1 to 0, for motion that goes and comes back.
func triangle(t float32) float32 { return float32(1 - math.Abs(float64(2*t-1))) }

// banner shows what the daemon says is wrong with what runs.
func (a *app) banner(c *ui.Context, p palette) {
	if a.status == nil || a.status.LastError == "" || a.status.LastError == a.dismissed {
		return
	}
	ui.Row(c).Gap(tw(1.5)).Padding(tw(2.5), tw(3)).Radius(cardRadius).Background(p.errorSurface).Border(1, p.errorBorder).AlignItems(ui.Start).Children(func() {
		ui.Column(c).Grow(1).Gap(tw(1.5)).Children(func() {
			label(c, "Connection Failed", sizeSubhead, 16, 600, p.errorC)
			mono(c, a.status.LastError, sizeCaption, 14, 400, p.text).MaxLines(4).Selectable()
		})
		if iconButton(c, "dismiss", iconX, "Dismiss", p, iconXS, variantGhost, 11) {
			a.dismissed = a.status.LastError
		}
	})
}

func (a *app) emptyState(c *ui.Context, p palette) {
	ui.Column(c).AlignItems(ui.Center).Gap(tw(3.5)).Padding(tw(6)).Radius(cardRadius).Background(p.card).Border(1, p.borderDefault).Children(func() {
		ui.Box(c).Size(tw(12), tw(12)).Radius(tw(4)).Background(p.accentSurface).Center().Children(func() {
			ui.Icon(c, iconRadio).FontSize(24).TextColor(p.accent)
		})
		label(c, "No Subscriptions Added", sizeTitle, 17, 600, p.text)
		label(c, "Add a subscription URL to import proxy outbounds and configure routing rules.", sizeCaption, 15, 400, p.muted).TextAlign(ui.Center)
		btn := ui.ButtonBase(c.Key("empty-add")).Height(36).Padding(0, tw(3.5)).Radius(tw(2)).Background(p.accent).Gap(tw(1.5)).Center().Cursor(ui.CursorPointer).Margin(tw(1), 0, 0)
		btn.Children(func() {
			ui.Icon(c, iconPlus).FontSize(15).TextColor(ui.Hex("#ffffff"))
			label(c, "Add Subscription", sizeBody, 18, 500, ui.Hex("#ffffff"))
		})
		if btn.Clicked() {
			a.openScreen(screenAddSubscription)
		}
	})
}

// ---- mode ----

func (a *app) modeSection(c *ui.Context, p palette) {
	idx := 0
	if a.fallback() == pb.Fallback_FALLBACK_DIRECT {
		idx = 1
	}
	ui.Column(c).Gap(tw(1.5)).Children(func() {
		if next, changed := segmented(c, "mode", []string{"Tunnel", "Direct"}, idx, p); changed {
			if next == 0 {
				a.setFallback(pb.Fallback_FALLBACK_PROXY)
			} else {
				a.setFallback(pb.Fallback_FALLBACK_DIRECT)
			}
		}
		text := "non-ruled traffic is tunneled"
		if idx == 1 {
			text = "Non-ruled traffic is direct"
		}
		label(c, text, sizeCaption, 15, 400, p.muted)
	})
}

// ---- routing card ----

func (a *app) routingCard(c *ui.Context, p palette) {
	card := ui.Column(c).Radius(cardRadius).Background(p.card).Border(1, p.borderDefault).Clip().Shrink(0)
	if p.dark {
		card.Shadow(0, 1, 4, 0, ui.RGBA(0, 0, 0, 0.2))
	} else {
		card.Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.05))
	}
	card.Children(func() {
		a.serverRow(c, p)
		a.accordion(c, "servers", a.motion.servers.x, a.open == sectionServers, func() { a.serverPicker(c, p) })
		ui.Box(c).Height(0.7).Background(p.borderSubtle).Shrink(0)
		a.rulesHeader(c, p)
		a.accordion(c, "rules", a.motion.rules.x, a.open == sectionRules, func() { a.rulesPanel(c, p) })
	})
}

// accordion shows what fn builds below its header, as far open as p says: the height grows
// and the content fades in with it. It records how tall the content is when fully open.
func (a *app) accordion(c *ui.Context, key string, p float32, open bool, fn func()) {
	pal := a.palette
	if !open && p == 0 {
		return
	}
	host := ui.Column(c.Key("acc-"+key)).Shrink(0).ClipY().Background(pal.subdued).Radius(0, 0, tw(2), tw(2)) // bg-surface-subdued rounded-b-lg
	var inner ui.Element
	host.Children(func() {
		inner = ui.Column(c).Shrink(0)
		inner.Children(fn)
	})
	if h := inner.Bounds().H; h > 0 {
		a.natural[key] = h
	}
	if p < 1 {
		host.Height(p * a.naturalHeight(key)).Opacity(p)
	}
}

func (a *app) serverRow(c *ui.Context, p palette) {
	s, ok := a.active()
	row := ui.ButtonBase(c.Key("server-row")).Padding(tw(2.5), tw(3)).Gap(tw(2)).Cursor(ui.CursorPointer).
		AlignItems(ui.Center).Justify(ui.SpaceBetween).Shrink(0)
	bg := ui.Color{}
	if row.Hovered() {
		bg = p.cardHover
	}
	tint(row, bg)
	row.Children(func() {
		flag, title, detail := serverTitle(s.cfg.GetName())
		if !ok {
			flag, title, detail = "🌐", "Select Outbound", ""
		}
		ui.Column(c).Grow(1).Basis(0).MinWidth(0).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(tw(1)).Children(func() {
				flagElement(c, flag, 17, 12.75)
				ltrLabel(c, title, sizeTitle, 17, 600, p.text)
			})
			if ok {
				sub := s.subName
				extra := detail
				if extra == "" {
					extra = "Default Outbound"
					if a.fallback() == pb.Fallback_FALLBACK_DIRECT {
						extra = "Proxy Outbound"
					}
				}
				ltrLabel(c, sub+" • "+extra, sizeCaption, 14, 400, p.muted).MaxWidth(180)
			}
		})
		ui.Row(c).Gap(tw(1)).AlignItems(ui.Center).Shrink(0).Children(func() {
			if ok {
				if latencyBadge(c, "active-latency", a.latency[s.key], a.testing[s.key], p) {
					a.testServers(s)
				}
			}
			ui.Box(c).Size(tw(4), tw(4)).Center().Shrink(0).Children(func() {
				ui.Icon(c, iconChevronDown).FontSize(15).TextColor(p.tertiary).Rotate(180 * a.motion.servers.x)
			})
		})
	})
	if row.Clicked() {
		a.toggle(sectionServers)
	}
}

func (a *app) toggle(s section) {
	if a.open == s {
		a.open = sectionNone
	} else {
		a.open = s
	}
	// The window takes the list's height as it unfolds, and gives it back as it folds: it
	// is one resize with the list's movement, not one after it.
	a.requestHeight()
}

// panelBar is the search row at the top of the open panels: the field and a button.
func panelBar(c *ui.Context, p palette, field func(), button func()) {
	ui.Box(c).Height(1).Background(p.borderSubtle).Shrink(0)
	ui.Row(c).Padding(tw(2)).Gap(tw(1)).AlignItems(ui.Center).Shrink(0).Children(func() {
		field()
		button()
	})
	ui.Box(c).Height(1).Background(p.borderSubtle).Shrink(0)
}

func (a *app) serverPicker(c *ui.Context, p palette) {
	panelBar(c, p, func() { searchInput(c, "server-search", &a.query, "Search country, or remarks...", p) }, func() {
		busy := len(a.testing) > 0
		if iconButton(c, "test-all", iconZap, map[bool]string{true: "Testing latency...", false: "Test Latency (All nodes)"}[busy], p, iconMD, variantSubtle, 12) && !busy {
			a.testServers()
		}
	})
	servers := a.filteredServers()
	if len(servers) == 0 {
		ui.Column(c).AlignItems(ui.Center).Gap(tw(1)).Padding(tw(6), 0).Children(func() {
			label(c, "No matching outbounds", sizeSubhead, 18, 500, p.muted)
			label(c, "Try a different search term", sizeMicro, 14, 400, p.tertiary)
		})
		return
	}
	groups := orderedGroups(servers)
	items := make([]serverListItem, 0, len(servers)+len(groups))
	for i := range groups {
		g := &groups[i]
		if len(groups) > 1 {
			items = append(items, serverListItem{group: g})
		}
		for _, s := range g.servers {
			items = append(items, serverListItem{srv: s})
		}
	}
	a.serverItems = items
	ui.List(c, &a.serversList, len(items), func(i int) {
		it := a.serverItems[i]
		if it.group != nil {
			a.groupHeader(c, p, *it.group)
			return
		}
		a.serverItem(c, p, it.srv)
	}).MaxHeight(300).Padding(0, tw(1.5), 0, tw(1)).Gap(2).Shrink(0)
}

type serverGroup struct {
	name    string
	servers []server
}

// serverListItem is one row of the server picker: a subscription's header, or a server.
type serverListItem struct {
	group *serverGroup
	srv   server
}

// orderedGroups gathers servers by subscription, in the order the subscriptions come.
func orderedGroups(servers []server) []serverGroup {
	var out []serverGroup
	idx := map[string]int{}
	for _, s := range servers {
		name := s.subName
		if name == "" {
			name = "Custom Outbounds"
		}
		i, ok := idx[name]
		if !ok {
			i = len(out)
			idx[name] = i
			out = append(out, serverGroup{name: name})
		}
		out[i].servers = append(out[i].servers, s)
	}
	return out
}

func (a *app) groupHeader(c *ui.Context, p palette, g serverGroup) {
	ui.Row(c).Padding(tw(1), tw(2)).Radius(tw(1)).Background(p.subdued).AlignItems(ui.Center).Justify(ui.SpaceBetween).Shrink(0).Children(func() {
		label(c, strings.ToUpper(ltr(g.name)), sizeMicro, 18, 600, p.muted).LetterSpacing(0.5).SingleLine().Grow(1).MarginEnd(tw(2))
		ui.Row(c).Gap(tw(1.5)).AlignItems(ui.Center).Shrink(0).Children(func() {
			n := fmt.Sprintf("%d node", len(g.servers))
			if len(g.servers) > 1 {
				n += "s"
			}
			label(c, n, sizeCaption, 18, 400, p.muted).Opacity(0.75)
			if iconButton(c, "group-test-"+g.name, iconZap, "Test all "+g.name+" configs", p, iconXS, variantGhost, 10) {
				a.testServers(g.servers...)
			}
		})
	})
}

func (a *app) serverItem(c *ui.Context, p palette, s server) {
	active, _ := a.activeKey()
	selected := active == s.key
	row := ui.ButtonBase(c.Key(s.key)).Padding(tw(1.5), tw(1), tw(1.5), tw(2)).Radius(tw(2)).MinHeight(38).Cursor(ui.CursorPointer).
		AlignItems(ui.Center).Justify(ui.SpaceBetween).Shrink(0)
	bg := ui.Color{}
	switch {
	case selected:
		bg = p.healthySurface
	case row.Hovered():
		bg = p.cardHover
	}
	tint(row, bg)
	row.Children(func() {
		flag, title, detail := serverTitle(s.cfg.Name)
		ui.Row(c).Gap(tw(2.5)).AlignItems(ui.Center).Grow(1).Basis(0).MinWidth(0).MarginEnd(tw(2)).Children(func() {
			iconTile(c, p, func() { flagElement(c, flag, 18, 13.5) })
			ui.Column(c).Grow(1).Basis(0).MinWidth(0).Children(func() {
				ltrLabel(c, title, sizeBody, 16, 600, p.text)
				if detail != "" {
					ltrLabel(c, detail, sizeCaption, 14, 400, p.muted)
				}
			})
		})
		ui.Row(c).Gap(tw(1.5)).AlignItems(ui.Center).Shrink(0).Children(func() {
			if latencyBadge(c, "lat", a.latency[s.key], a.testing[s.key], p) {
				a.testServers(s)
			}
		})
	})
	if row.Clicked() {
		a.selectServer(s)
	}
}

// ---- rules ----

func (a *app) rulesHeader(c *ui.Context, p palette) {
	enabled := a.rulesOn()
	count := len(a.ruleList())
	row := ui.ButtonBase(c.Key("rules-row")).Padding(0, tw(3)).MinHeight(32).Cursor(ui.CursorPointer).
		AlignItems(ui.Center).Justify(ui.SpaceBetween).Shrink(0)
	bg := ui.Color{}
	if row.Hovered() {
		bg = p.cardHover
	}
	tint(row, bg)
	row.Children(func() {
		ui.Row(c).Gap(tw(1.5)).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(tw(4), tw(4)).Center().Shrink(0).Children(func() {
				ui.Icon(c, iconChevronRight).FontSize(13).TextColor(p.muted).Rotate(90 * a.motion.rules.x)
			})
			label(c, "RULES", sizeMicro, 14, 500, p.muted).LetterSpacing(0.5)
		})
		ui.Row(c).Gap(tw(2)).AlignItems(ui.Center).Children(func() {
			noun := "rules"
			if count == 1 {
				noun = "rule"
			}
			ui.Row(c).Gap(tw(1)).AlignItems(ui.Center).Children(func() {
				rollingNumber(c, "rules-count", float64(count), fmt.Sprint(count), sizeCaption, 15, 400, p.muted, false)
				label(c, noun, sizeCaption, 15, 400, p.muted)
			})
			ui.Row(c).Margin(0, 0, 0, tw(1.5)).Children(func() {
				if next, changed := nativeSwitch(c, "rules-switch", "Use rules", enabled, switchSubdued, false, false, p); changed {
					a.setRulesEnabled(next)
				}
			})
		})
	})
	if row.Clicked() {
		a.toggle(sectionRules)
	}
}

func (a *app) rulesPanel(c *ui.Context, p palette) {
	enabled := a.rulesOn()
	count := len(a.ruleList())
	panelBar(c, p, func() { searchInput(c, "rule-search", &a.ruleQuery, "Search rules...", p) }, func() {
		if iconButton(c, "rule-add", iconPlus, "Add rule", p, iconMD, variantSubtle, 12) {
			a.notify("Adding rules from the panel isn't available yet")
		}
	})
	// The list builds only the rows it shows. Scrolling it closes an open row and menu.
	first, last := a.rulesList.Visible()
	if a.lastRuleTop >= 0 && (first != a.lastRuleTop || last != a.lastRuleEnd) {
		a.menu.open = false
		if sw := a.swipes[a.openSwipe]; sw != nil {
			sw.close(c.Now())
		}
	}
	a.lastRuleTop, a.lastRuleEnd = first, last

	a.ruleItems = a.ruleListItems(a.filteredRules())
	if len(a.ruleItems) == 0 {
		msg := "No rules configured."
		if count > 0 {
			msg = "No matching rules found."
		}
		label(c, msg, sizeSubhead, 18, 400, p.muted).TextAlign(ui.Center).Padding(10, 0)
		return
	}
	list := ui.List(c, &a.rulesList, len(a.ruleItems), func(i int) {
		it := a.ruleItems[i]
		if it.rule == nil {
			a.ruleSectionRow(c, p, it.title, it.count)
			return
		}
		a.ruleRow(c, p, it.rule)
	}).MaxHeight(212-(2*tw(2)+tw(7)+1)).Padding(tw(1), tw(1.5), tw(1), tw(1)).Gap(tw(1.5)).Shrink(0)
	if !enabled {
		list.Opacity(0.5)
	}
}

// ruleListItems turns the grouped rules into the flat rows the list builds: a header for
// each section, then its rules.
func (a *app) ruleListItems(rules []*pb.RoutingRule) []ruleListItem {
	sections := a.groupRules(rules)
	items := make([]ruleListItem, 0, len(rules)+len(sections))
	for _, s := range sections {
		items = append(items, ruleListItem{title: s.title, count: len(s.rules)})
		for _, r := range s.rules {
			items = append(items, ruleListItem{rule: r})
		}
	}
	return items
}

// ruleSectionRow is a section's title in the rules list; the list keeps it at the top
// while its rows scroll under it.
func (a *app) ruleSectionRow(c *ui.Context, p palette, title string, count int) {
	ui.Row(c).Padding(tw(1.5), tw(2), tw(0.5)).Background(p.subdued).Children(func() {
		label(c, fmt.Sprintf("%s (%d)", strings.ToUpper(title), count), sizeMicro, 18, 600, p.muted).LetterSpacing(0.5)
	})
}

func (a *app) swipeFor(tag string) *swipe {
	s := a.swipes[tag]
	if s == nil {
		s = &swipe{}
		a.swipes[tag] = s
	}
	return s
}

// ruleRow is a rule of the list: the row itself, which a two-finger swipe left slides over
// the buttons behind it, and a right-click menu.
func (a *app) ruleRow(c *ui.Context, p palette, r *pb.RoutingRule) {
	now := c.Now()
	sw := a.swipeFor(r.RuleTag)
	switch {
	case sw.open && a.openSwipe != r.RuleTag && sw.mode != swipeTracking && a.openSwipe != "":
		sw.close(now) // only one row is open
	case sw.open:
		a.openSwipe = r.RuleTag
	case a.openSwipe == r.RuleTag && sw.mode == swipeIdle:
		a.openSwipe = ""
	}
	if sw.advance(now) {
		c.Invalidate()
	}

	box := ui.Box(c.Key("rule-" + r.RuleTag)).MinHeight(tw(10)).Radius(tw(2)).Clip().Shrink(0)
	box.Children(func() {
		if sw.x != 0 {
			a.swipeButtons(c, p, r, sw, now)
		}
		row := ui.Row(c.Key("rule-row")).Padding(tw(1.5), tw(1), tw(1.5), tw(1.5)).
			AlignItems(ui.Center).Justify(ui.SpaceBetween).Shrink(0).Translate(sw.x, 0)
		bg := ui.Color{}
		if row.Hovered() || (a.menu.open && a.menu.tag == r.RuleTag) {
			bg = p.cardHover
			if a.menu.open && a.menu.tag == r.RuleTag {
				bg = p.cardActive
			}
		}
		tint(row, bg)
		if r.Disabled {
			row.Opacity(0.5)
		}
		// Two fingers on the touchpad swipe the row; a mouse wheel and the vertical scrolling
		// of the list are left alone.
		row.HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind != ui.InputScroll || !ev.Precise {
				return false
			}
			return sw.scroll(ev.DX, ev.DY, ev.Time)
		})
		row.Children(func() {
			a.ruleContent(c, p, r)
			// A touchpad swipe is macOS's gesture; elsewhere the row's actions have a
			// button of their own.
			if runtime.GOOS != "darwin" && iconButton(c, "more-"+r.RuleTag, iconMore, "Rule actions", p, iconXS, variantGhost, 12) {
				b := row.Bounds()
				a.menu = ruleMenu{open: true, tag: r.RuleTag, x: b.X + b.W - menuWidth - tw(2), y: b.Y + b.H}
				sw.close(now)
			}
		})
		if row.Clicked() && sw.open {
			sw.close(now) // a click on an open row closes it
		}
		if row.RightClicked() {
			px, py, _ := row.PointerPosition()
			b := row.Bounds()
			a.menu = ruleMenu{open: true, tag: r.RuleTag, x: b.X + px, y: b.Y + py}
			sw.close(now)
		}
	})
}

// iconTile is the rounded square that holds an app's icon, a flag or a rule's icon. Its
// drawing fills a box of the tile's own content, so it sits in the middle whatever kind of
// drawing it is, and the border and corners stay clear of it.
func iconTile(c *ui.Context, p palette, draw func()) {
	ui.Box(c).Size(tw(7), tw(7)).Radius(7).Background(p.cardHover).Border(1, p.borderDefault).Shrink(0).Clip().Children(func() {
		ui.Box(c).Fill().Center().Children(draw)
	})
}

// ruleContent is what a rule's row shows: the tile, the title, and where it goes.
func (a *app) ruleContent(c *ui.Context, p palette, r *pb.RoutingRule) {
	info := a.metaFor(r).info
	var icon *ui.Bitmap
	if info.appPath != "" {
		meta := a.apps.lookup(info.appPath, a.async, func() { a.post(func() {}) })
		info.title, icon = a.apps.read(meta)
	}
	ui.Row(c).Gap(tw(2.5)).AlignItems(ui.Center).Grow(1).Basis(0).MinWidth(0).MarginEnd(tw(1.5)).Children(func() {
		iconTile(c, p, func() {
			switch {
			case icon != nil:
				ui.Image(c, icon).Size(18, 18).Radius(4)
			case info.flag != "":
				flagElement(c, info.flag, 18, 13.5)
			default:
				ui.Icon(c, info.icon).FontSize(14).TextColor(p.muted)
			}
		})
		ui.Row(c).Gap(tw(1.5)).AlignItems(ui.Center).Grow(1).Basis(0).MinWidth(0).Children(func() {
			ltrLabel(c, info.title, sizeBody, 18, 500, p.text)
			if info.summary != "" {
				mono(c, "· "+info.summary, sizeCaption, 14, 400, p.muted).SingleLine().Shrink(0)
			}
		})
	})
	a.destinationBadge(c, p, r.Target)
}

// swipeButtons are the buttons behind a row: they slide in from the right as the row slides
// away, fading in over the first few units.
func (a *app) swipeButtons(c *ui.Context, p palette, r *pb.RoutingRule, sw *swipe, now time.Time) {
	bar := ui.Row(c.Key("swipe-buttons")).Absolute().Top(0).Bottom(0).Right(0).Padding(0, tw(1.5), 0, 0).
		Gap(tw(1.5)).AlignItems(ui.Center).Translate(sw.buttonsX(), 0).Opacity(sw.buttonsOpacity())
	bar.Children(func() {
		title := "Disable Rule"
		if r.Disabled {
			title = "Enable Rule"
		}
		if a.swipeButton(c, p, "power", title, iconPower, false, !r.Disabled) {
			sw.close(now)
			a.setRuleEnabled(r.RuleTag, r.Disabled)
		}
		if a.swipeButton(c, p, "edit", "Edit Rule", iconPencil, false, true) {
			sw.close(now)
			a.editRule(r)
		}
		if a.swipeButton(c, p, "delete", "Remove Rule", iconTrash, true, true) {
			sw.close(now)
			a.removeRule(r.RuleTag)
		}
	})
}

// swipeButton is one of the square buttons: quiet, and red under the pointer for removing.
func (a *app) swipeButton(c *ui.Context, p palette, key, title string, icon *ui.SVG, danger, active bool) bool {
	b := ui.ButtonBase(c.Key(key)).Label(title).Tooltip(title).Size(tw(7), tw(7)).Radius(6).Center().Shrink(0).
		Cursor(ui.CursorPointer)
	hover := b.Hovered()
	bg, border, fg := p.subdued, p.borderDefault, p.muted
	switch {
	case danger:
		bg, border = p.cardHover, p.borderSubtle
		if hover {
			bg, border, fg = p.errorSurface, p.errorBorder, p.errorC
		}
	case !active:
		bg, border, fg = p.card, p.borderSubtle, p.tertiary
	case hover:
		bg, fg = p.cardActive, p.text
	}
	b.Background(bg).Border(1, border)
	b.Children(func() { ui.Icon(c, icon).FontSize(13).TextColor(fg) })
	return b.Clicked()
}

// destinationBadge says where a rule's traffic goes.
func (a *app) destinationBadge(c *ui.Context, p palette, target string) {
	switch {
	case target == "block":
		badge(c, "Block", iconBan, p.errorC, p.errorSurface, tw(7), 12)
	case target == "proxy":
		badge(c, "Proxy", iconShuffle, p.healthy, p.healthySurface, tw(7), 12)
	case target == "direct":
		badge(c, "Direct", iconDirect, p.direct, p.directSurface, tw(7), 12)
	default:
		flag, name := "🌐", strings.TrimPrefix(strings.TrimPrefix(target, "outbound:"), "config:")
		if strings.HasPrefix(target, "config:") {
			flag, name = a.pinnedServer(strings.TrimPrefix(target, "config:"))
		}
		ui.Row(c).Gap(tw(1)).Padding(tw(0.5), tw(1.5)).Radius(6).Background(p.pinnedSurface).Height(tw(7)).AlignItems(ui.Center).Shrink(0).Children(func() {
			ui.Icon(c, iconPin).FontSize(10).TextColor(p.pinned)
			flagElement(c, flag, 14, 10.5)
			label(c, ltr(name), sizeSubhead, 14, 500, p.pinned).SingleLine().MaxWidth(120)
		})
	}
}

// pinnedServer is the flag and country of the server a "sub/cfg" target points at.
func (a *app) pinnedServer(ref string) (flag, name string) {
	sub, cfg, _ := strings.Cut(ref, "/")
	for _, s := range a.servers {
		if s.key.sub == sub && s.key.cfg == cfg {
			f, t, _ := serverTitle(s.cfg.Name)
			return f, t
		}
	}
	return "🌐", ref
}

// ---- footer ----

func (a *app) footer(c *ui.Context, p palette) {
	ui.Column(c).Shrink(0).Children(func() {
		ui.Box(c).Height(1).Background(p.borderSubtle).Shrink(0)
		ui.Row(c).Padding(tw(2.5), tw(0.5), 0).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			label(c, "Masir "+versionLabel(a.version), sizeCaption, 15, 400, p.muted).LetterSpacing(0.275)
			ui.Row(c).Gap(tw(2)).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Gap(tw(2)).AlignItems(ui.Center).Children(func() {
					a.speedLabel(c, p, "down", a.down, "↓")
					label(c, "•", sizeMicro, 14, 400, p.quaternary)
					a.speedLabel(c, p, "up", a.up, "↑")
				})
				if iconButton(c, "settings", iconSettings, "Settings", p, iconXS, variantGhost, 14) {
					a.openScreen(screenSettings)
				}
			})
		})
	})
}

func versionLabel(v string) string {
	if v == "" || v == "dev" {
		return "dev"
	}
	if !strings.HasPrefix(v, "v") {
		return "v" + v
	}
	return v
}

// measure is how tall the panel will be when it opens, from the state as it is now: the
// view is built once in memory, so the window can open at its real height instead of
// opening at a guess and resizing a moment later, which shows as the content jumping.
func (a *app) measure() float32 {
	onHeight, notices := a.onHeight, a.notices
	a.onHeight, a.notices = nil, nil
	wasScreen, wasOpen, wasSub, wasMotion, wasLast := a.screen, a.open, a.lastSub, a.motion, a.lastMotion
	a.screen, a.open = screenMain, sectionNone
	a.motion = motion{instant: a.motion.instant}
	a.lastMotion = time.Time{}
	tt := ui.NewTester(a.view, panelWidth, 900) // tall enough that nothing is cut off
	tt.Frame()
	tt.Frame() // an element knows its size from the frame before
	h := a.restHeight()
	a.screen, a.open, a.lastSub, a.motion, a.lastMotion = wasScreen, wasOpen, wasSub, wasMotion, wasLast
	a.onHeight, a.notices = onHeight, notices
	return h
}

// speedLabel shows a speed as the footer does: the number rolls, the unit and arrow stay.
func (a *app) speedLabel(c *ui.Context, p palette, key string, bps uint64, arrow string) {
	num, unit := speedParts(bps)
	ui.Row(c).AlignItems(ui.Center).Shrink(0).Children(func() {
		rollingNumber(c, key, float64(bps), num, sizeMicro, 14, 400, p.muted, true)
		mono(c, unit+arrow, sizeMicro, 14, 400, p.muted)
	})
}
