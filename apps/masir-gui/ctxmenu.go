package main

import (
	"time"

	"github.com/egoist/mygo/ui"

	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// ruleMenu is the menu a right-click opens on a rule: which rule, and where in the window.
type ruleMenu struct {
	open bool
	tag  string
	x, y float32
}

const menuWidth = 124

// menuHeight: three items, the divider, the gaps and the padding.
var menuHeight = 3*(2*tw(1.5)+12) + 0.5 + 2*tw(0.5) + 2*tw(0.5) + 2*tw(1) + 2

// contextMenu draws the rule menu above everything: a small panel of the design's look, with
// the items of a macOS menu — the accent color under the pointer, red for removing.
func (a *app) contextMenu(c *ui.Context, p palette) {
	if !a.menu.open {
		return
	}
	var rule = a.ruleByTag(a.menu.tag)
	if rule == nil {
		a.menu.open = false
		return
	}
	ui.Overlay(c, func() {
		// Anything pressed outside closes it.
		scrim := ui.Box(c.Key("menu-scrim")).Absolute().Top(0).Left(0).Right(0).Bottom(0)
		if scrim.Clicked() || scrim.RightClicked() {
			a.menu.open = false
		}

		x := clamp32(a.menu.x, 4, panelWidth-menuWidth-4)
		y := clamp32(a.menu.y, 4, a.windowHeight()-menuHeight-4)
		m := ui.Column(c.Key("menu")).Absolute().Left(x).Top(y).Width(menuWidth).Padding(tw(1)).Gap(tw(0.5)).
			Radius(10).Background(p.elevated).Border(1, p.borderDefault)
		if p.dark {
			m.Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.5)).Shadow(0, 2, 8, 0, ui.RGBA(0, 0, 0, 0.3))
		} else {
			m.Shadow(0, 12, 32, 0, ui.RGBA(0, 0, 0, 0.22)).Shadow(0, 2, 8, 0, ui.RGBA(0, 0, 0, 0.12))
		}
		// It pops in: fades and grows from 95% over 100ms.
		in := m.AnimateWith("in", 1, 100*time.Millisecond, easeApp)
		m.Opacity(in).Scale(0.95+0.05*in, 0.95+0.05*in)
		m.Children(func() {
			toggle := "Disable Rule"
			if rule.Disabled {
				toggle = "Enable Rule"
			}
			if a.menuItem(c, p, "edit", "Edit Rule", iconPencil, false) {
				a.menu.open = false
				a.editRule(rule)
			}
			if a.menuItem(c, p, "toggle", toggle, iconPower, false) {
				a.menu.open = false
				a.setRuleEnabled(rule.RuleTag, rule.Disabled)
			}
			ui.Box(c).Height(0.5).Background(p.borderSubtle).Margin(tw(0.5), tw(1))
			if a.menuItem(c, p, "remove", "Remove Rule", iconTrash, true) {
				a.menu.open = false
				a.removeRule(rule.RuleTag)
			}
		})
	})
}

// menuItem is one row of the menu; it turns the accent color (or red) under the pointer.
func (a *app) menuItem(c *ui.Context, p palette, key, text string, icon *ui.SVG, danger bool) bool {
	b := ui.ButtonBase(c.Key(key)).Label(text).Padding(tw(1.5), tw(2)).Radius(6).Gap(tw(2)).
		AlignItems(ui.Center).Justify(ui.Start).Cursor(ui.CursorPointer).Shrink(0)
	fg, iconFg, bg := p.text, p.muted, ui.Color{}
	if danger {
		fg, iconFg = p.errorC, p.errorC
	}
	if b.Hovered() {
		bg, fg, iconFg = p.accent, ui.Hex("#ffffff"), ui.Hex("#ffffff")
		if danger {
			bg = p.errorC
		}
	}
	b.Background(b.AnimateColor("bg", bg, appMotion(75*time.Millisecond)))
	b.Children(func() {
		ui.Icon(c, icon).FontSize(12).TextColor(iconFg).Shrink(0)
		label(c, text, sizeSubhead, 12, 400, fg).SingleLine()
	})
	return b.Clicked()
}

func (a *app) ruleByTag(tag string) *pb.RoutingRule {
	for _, r := range a.rules {
		if r.RuleTag == tag {
			return r
		}
	}
	return nil
}

// editRule opens a rule for editing. The editor isn't built yet.
func (a *app) editRule(r *pb.RoutingRule) {
	a.notify("Editing rules isn't available yet")
}
