package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"google.golang.org/protobuf/proto"

	"github.com/narixius/masir-go/libs/masir-core/analytics"
	"github.com/narixius/masir-go/libs/masir-core/paths"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// System hooks the pages use; tests replace them.
var (
	readClipboard = func() string {
		b, err := mygo.Clipboard.ReadFormat(transfer.Text)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(b))
	}
	openAtLogin    = func() bool { return mygo.App.OpenAtLogin() }
	setOpenAtLogin = func(on bool) error { return mygo.App.SetOpenAtLogin(on) }
	quit           = func() { mygo.App.Quit() }
	openTerminalIn = openTerminalCommand
	openFileIn     = openFile
	// markNoticeShown leaves the marker the daemon waits for before sending anonymous
	// analytics, once the app has shown the notice.
	markNoticeShown = func() {
		dir, err := paths.ConfigDir()
		if err != nil || analytics.NoticeShown(dir) {
			return
		}
		_ = analytics.MarkNoticeShown(dir)
	}
)

// ---- building blocks ----

// cardBox is the surface settings rows sit on.
func cardBox(c *ui.Context, p palette, fn func()) {
	card := ui.Column(c).Radius(cardRadius).Background(p.card).Border(1, p.borderDefault).Clip().Shrink(0)
	if p.dark {
		card.Shadow(0, 1, 4, 0, ui.RGBA(0, 0, 0, 0.2))
	} else {
		card.Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.04))
	}
	card.Children(fn)
}

// cardRow is a row of a card: 10 and 12 of padding, the two ends apart.
func cardRow(c *ui.Context, key string, fn func()) {
	ui.Row(c.Key(key)).Padding(tw(2.5), tw(3)).Gap(tw(3)).AlignItems(ui.Center).Justify(ui.SpaceBetween).Shrink(0).Children(fn)
}

func cardDivider(c *ui.Context, p palette) {
	ui.Box(c).Height(0.5).Background(p.borderSubtle).Shrink(0)
}

// titled is a titled group, with an action at the end of the title.
func titled(c *ui.Context, p palette, title string, action func(), fn func()) {
	ui.Column(c.Key("section-" + title)).Shrink(0).Children(func() {
		ui.Row(c).Padding(0, tw(1)).Margin(0, 0, tw(1.5)).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
			label(c, title, sizeCaption, 14, 600, p.muted).LetterSpacing(0.5)
			if action != nil {
				action()
			}
		})
		fn()
	})
}

// screenHeader is the back arrow and the centered title of a page.
func (a *app) screenHeader(c *ui.Context, p palette, title string) {
	ui.Row(c).Padding(0, 0, tw(3)).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Row(c).Width(tw(7)).Children(func() {
			if iconButton(c, "back", iconBack, "Back", p, iconSM, variantGhost, 16) {
				a.screen = a.backTo
				a.requestHeight() // the window takes the new page's height from the first frame
			}
		})
		label(c, title, sizeTitle, 18, 600, p.text).TextAlign(ui.Center).Grow(1).SingleLine()
		ui.Box(c).Width(tw(7))
	})
}

// button is the button of the design: primary is the blue one, full width.
func button(c *ui.Context, key, text string, primary, disabled, loading bool, p palette) bool {
	b := ui.ButtonBase(c.Key(key)).Height(36).Padding(0, tw(3.5)).Radius(tw(2)).Gap(tw(1.5)).Center().Shrink(0).
		Cursor(ui.CursorPointer)
	bg, fg := p.cardHover, p.text
	if primary {
		bg, fg = p.accent, ui.Hex("#ffffff")
		if b.Hovered() && !disabled {
			bg = p.accentHover
		}
		b.Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.05))
	} else if b.Hovered() {
		bg = p.cardActive
	}
	tint(b, bg)
	if disabled || loading {
		b.Opacity(0.5)
	}
	b.Children(func() {
		if loading {
			ui.Spinner(c).Size(14, 14)
		}
		label(c, text, sizeBody, 18, 500, fg)
	})
	return b.Clicked() && !disabled && !loading
}

// thinButton is a card's action across its width, as tall as a field or a select.
func thinButton(c *ui.Context, key, text string, primary, disabled, loading bool, p palette) bool {
	b := ui.ButtonBase(c.Key(key)).Height(26).Padding(0, tw(2.5)).Radius(tw(1.5)).Gap(tw(1.5)).
		Center().Shrink(0).Cursor(ui.CursorPointer)
	bg, fg := p.cardHover, p.text
	if primary {
		bg, fg = p.accent, ui.Hex("#ffffff")
		if b.Hovered() && !disabled {
			bg = p.accentHover
		}
	} else if b.Hovered() {
		bg = p.cardActive
	}
	tint(b, bg)
	if disabled || loading {
		b.Opacity(0.5)
	}
	b.Children(func() {
		if loading {
			ui.Spinner(c).Size(12, 12)
		}
		label(c, text, sizeCaption, 16, 500, fg)
	})
	return b.Clicked() && !disabled && !loading
}

// field is a text field with its label above, a leading icon, and an action at the end.
func field(c *ui.Context, p palette, key, title string, value *string, placeholder string, icon *ui.SVG, iconColor ui.Color, trailing func()) bool {
	submitted := false
	ui.Column(c.Key(key)).Gap(tw(1)).Shrink(0).Children(func() {
		label(c, strings.ToUpper(title), sizeCaption, 14, 600, p.muted).LetterSpacing(0.4).Padding(0, tw(0.5))
		box := ui.Row(c).Gap(tw(1.5)).Padding(tw(0.5), tw(2.5)).Radius(tw(2)).AlignItems(ui.Center).Shrink(0)
		var in ui.Element
		box.Children(func() {
			ui.Icon(c, icon).FontSize(15).TextColor(iconColor).Shrink(0)
			in = ui.TextInputBase(c.Key(key+"-input"), value).Placeholder(placeholder).Grow(1).
				FontSize(sizeSubhead).TextColor(p.text).Padding(tw(1.5), 0)
			if key == "sub-url" {
				in.AutoFocus()
			}
			in.Valid()
			if trailing != nil {
				trailing()
			}
		})
		bg, border := p.subdued, p.borderDefault
		if in.Focused() {
			bg, border = p.card, p.borderFocus
		}
		box.Background(bg).Border(1, border).Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.02))
		submitted = in.Submitted()
	})
	return submitted
}

// ---- add subscription ----

func (a *app) addSubscriptionScreen(c *ui.Context, p palette) {
	ui.Column(c).Padding(tw(4)).Fill().Children(func() {
		a.screenHeader(c, p, "Add Subscription")
		ui.Column(c).Gap(tw(2.5)).Padding(tw(1), 0, 0).Shrink(0).Children(func() {
			cardBox(c, p, func() {
				ui.Column(c).Padding(tw(2.5), tw(3)).Gap(tw(2.5)).Children(func() {
					enter := field(c, p, "sub-url", "Subscription URL", &a.subURL, "https://example.com/sub/...", iconLink, p.accent, func() {
						if iconButton(c, "paste", iconClipboard, "Paste from clipboard", p, iconXS, variantSubtle, 14) {
							if s := readClipboard(); s != "" {
								a.subURL = s
							}
						}
					})
					field(c, p, "sub-name", "Label (Optional)", &a.subName, "e.g. My Server Group", iconTag, p.muted, nil)
					if enter {
						a.addSubscription()
					}
				})
			})
			if a.subError != "" {
				ui.Row(c).Gap(tw(1.5)).Padding(tw(2.5), tw(3)).Radius(cardRadius).Background(p.errorSurface).Border(1, p.errorBorder).Children(func() {
					label(c, a.subError, sizeCaption, 15, 400, p.errorC).MaxLines(5).Selectable()
				})
			}
			label(c, "Supports standard V2Ray/Xray, Clash, and Sing-box subscription links.", sizeCaption, 15, 400, p.muted).Padding(0, tw(1))
			ui.Column(c).Margin(tw(1), 0, 0).Children(func() {
				text := "Import Subscription"
				if a.subBusy {
					text = "Importing & Parsing..."
				}
				if button(c, "import", text, true, strings.TrimSpace(a.subURL) == "", a.subBusy, p) {
					a.addSubscription()
				}
			})
		})
	})
}

// ---- settings ----

func (a *app) settingsScreen(c *ui.Context, p palette) {
	ui.Column(c).Fill().Children(func() {
		ui.Column(c).Padding(tw(4), tw(4), 0, tw(4)).Shrink(0).Children(func() {
			a.screenHeader(c, p, "Settings")
		})
		// The scroll itself runs to the panel's edge, so its bar sits beside the cards
		// instead of over them; the sections keep the same margins inside it.
		ui.Scroll(c.Key("settings-scroll")).Grow(1).Basis(0).MinHeight(0).
			ScrollbarInsets(0, 2, 0).Children(func() {
			ui.Column(c).Gap(tw(3)).Padding(tw(1), tw(4), tw(4), tw(4)).Shrink(0).Children(func() {
				a.advancedSection(c, p)
				a.dnsSection(c, p)
				a.loggingSection(c, p)
				a.privacySection(c, p)
				a.systemSection(c, p)
				if button(c, "quit", "Quit Masir", false, false, false, p) {
					quit()
				}
			})
		})
	})
}

func (a *app) advancedSection(c *ui.Context, p palette) {
	titled(c, p, "ADVANCED", nil, func() {
		cardBox(c, p, func() {
			always := a.alwaysOnProxy()
			a.settingToggle(c, p, "always", "Always-on Proxy Port", "A local proxy port always available for apps you configure manually, using your active outbound.", always, func(on bool) {
				a.setAlwaysOnProxy(on)
			})
			cardDivider(c, p)
			ui.Column(c.Key("port-row")).Padding(tw(2.5), tw(3)).Gap(tw(1)).Shrink(0).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(tw(2)).Children(func() {
					label(c, "Port Number", sizeSubhead, 18, 500, p.text)
					box := ui.Row(c).Width(76).Padding(tw(1), tw(2)).Radius(tw(1.5)).Shrink(0).Clip()
					var in ui.Element
					box.Children(func() {
						in = ui.TextInputBase(c.Key("port"), &a.portText).Grow(1).MinWidth(0).
							FontSize(sizeSubhead).TextAlign(ui.End).Font("monospace").FontWeight(500).
							TextColor(p.text).Label("Port Number")
						in.Valid()
						if in.Changed() {
							if v := digitsOnly(a.portText); v != a.portText {
								a.portText = v
							}
						}
					})
					bg, border := p.subdued, p.borderDefault
					if in.Focused() {
						bg, border = p.card, p.borderFocus
					}
					box.Background(bg).Border(1, border)
					if in.Submitted() || (!in.Focused() && a.portEdited) {
						a.applyPort()
					}
					if in.Focused() {
						a.portEdited = true
					}
				})
				label(c, "Mixed SOCKS5 / HTTP", sizeCaption, 14.85, 400, p.muted)
			})
			cardDivider(c, p)
			lan := a.allowLAN()
			a.settingToggle(c, p, "lan", "Share on the Network", "Let other devices on your network use the local proxy.", lan, func(on bool) {
				a.setAllowLAN(on)
			})
			cardDivider(c, p)
			login := a.loginEnabled()
			hint := "Start Masir in the menu bar when you sign in."
			if a.loginNeedsApproval {
				hint = "Allow Masir in Login Items to start at login."
			}
			a.settingToggle(c, p, "login", "Open at Login", hint, login, func(on bool) {
				a.setLoginEnabled(on)
			})
			if a.loginNeedsApproval && runtime.GOOS == "darwin" {
				ui.Row(c).Padding(0, tw(3), tw(2.5)).Children(func() {
					if button(c, "login-items", "Open Login Items", false, false, false, p) {
						if err := openLoginItems(); err != nil {
							a.fail("Couldn't open Login Items: ", err)
						}
					}
				})
			}
			cardDivider(c, p)
			minutes := a.subInterval()
			a.selectRow(c, p, "interval", "Refresh Subscriptions",
				"How often masir checks each subscription for new servers.",
				strconv.Itoa(int(minutes)), intervalChoices(minutes), false, func(v string) {
					n, err := strconv.Atoi(v)
					if err != nil {
						return
					}
					a.setSubInterval(uint32(n))
				})
		})
	})
}

// settingToggle is a card row with a title, a subdued switch and a hint under them.
func (a *app) settingToggle(c *ui.Context, p palette, key, title, hint string, on bool, set func(bool)) {
	ui.Row(c.Key("toggle-"+key)).Padding(tw(2.5), tw(3)).Gap(tw(3)).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Column(c).Gap(tw(0.5)).Grow(1).Basis(0).MinWidth(0).Children(func() {
			ui.Row(c).Gap(tw(2)).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
				label(c, title, sizeSubhead, 18, 500, p.text)
				if next, changed := nativeSwitch(c, key, title, on, switchSubdued, false, false, p); changed {
					set(next)
				}
			})
			label(c, hint, sizeCaption, 14.85, 400, p.muted)
		})
	})
}

// ---- dns, logging and privacy ----

// defaultGateway is the tunnel's out-of-the-box address, as the daemon defines it.
const defaultGateway = "10.10.20.1/24"

// choice is one option of a drop-down: the value saved, and how it reads.
type choice struct{ value, label string }

// gatewayChoices is the addresses v1 offered, plus this version's default. The current
// value is kept as an option of its own when it came from the command line.
func gatewayChoices(current string) []choice {
	out := []choice{
		{"10.10.20.1/24", "10.10.20.1/24"},
		{"10.10.10.1/24", "10.10.10.1/24"},
		{"10.0.0.1/24", "10.0.0.1/24"},
		{"172.19.0.1/24", "172.19.0.1/24"},
		{"192.168.127.1/24", "192.168.127.1/24"},
		{"198.18.0.1/24", "198.18.0.1/24"},
	}
	for _, o := range out {
		if o.value == current {
			return out
		}
	}
	if current != "" {
		out = append([]choice{{current, current}}, out...)
	}
	return out
}

// intervalChoices is the subscription refresh interval, in minutes.
func intervalChoices(current uint32) []choice {
	out := []choice{
		{"15", "Every 15 minutes"},
		{"30", "Every 30 minutes"},
		{"60", "Every hour"},
		{"180", "Every 3 hours"},
		{"720", "Every 12 hours"},
		{"1440", "Every 24 hours"},
	}
	for _, o := range out {
		if o.value == strconv.Itoa(int(current)) {
			return out
		}
	}
	return append([]choice{{strconv.Itoa(int(current)), intervalLabel(current)}}, out...)
}

// intervalLabel reads a refresh interval as the choice row shows it.
func intervalLabel(minutes uint32) string {
	switch {
	case minutes == 720:
		return "Every 12 hours"
	case minutes%1440 == 0:
		return fmt.Sprintf("Every %d day(s)", minutes/1440)
	case minutes%60 == 0:
		return fmt.Sprintf("Every %d hour(s)", minutes/60)
	default:
		return fmt.Sprintf("Every %d minutes", minutes)
	}
}

// logLevelChoices is Xray's log level, quietest first.
func logLevelChoices() []choice {
	return []choice{
		{"none", "Off"},
		{"error", "Error"},
		{"warning", "Warning"},
		{"info", "Info"},
		{"debug", "Debug"},
	}
}

// dohChoices is the DNS-over-HTTPS resolvers the app offers; a resolver set from the
// command line is kept as an option of its own.
func dohChoices(current string) []choice {
	out := []choice{
		{"https://1.1.1.1/dns-query", "https://1.1.1.1/dns-query"},
		{"https://dns.google/dns-query", "https://dns.google/dns-query"},
		{"https://dns.quad9.net/dns-query", "https://dns.quad9.net/dns-query"},
		{"https://dns.adguard-dns.com/dns-query", "https://dns.adguard-dns.com/dns-query"},
	}
	for _, o := range out {
		if o.value == current {
			return out
		}
	}
	if current != "" {
		out = append([]choice{{current, current}}, out...)
	}
	return out
}

// settingHead is the title and hint at the top of a settings row.
func settingHead(c *ui.Context, p palette, title, hint string) ui.Element {
	return ui.Column(c).Gap(tw(0.5)).Children(func() {
		label(c, title, sizeSubhead, 18, 500, p.text)
		if hint != "" {
			label(c, hint, sizeCaption, 14.85, 400, p.muted)
		}
	})
}

// labelOf is the label of a choice, or the value itself when it isn't one of them.
func labelOf(opts []choice, value string) string {
	for _, o := range opts {
		if o.value == value {
			return o.label
		}
	}
	return value
}

// selectRow is a card row with a title, a hint and a drop-down of choices. Inline puts the
// value at the row's end; otherwise the drop-down spans the row under the hint, for a
// value too wide to share the line.
func (a *app) selectRow(c *ui.Context, p palette, key, title, hint, current string, opts []choice, inline bool, set func(string)) {
	selected := labelOf(opts, current)
	byLabel := make(map[string]string, len(opts))
	for _, o := range opts {
		byLabel[o.label] = o.value
	}
	draw := func() {
		sel := ui.SelectBase(c, &selected)
		sel.Trigger.Label(title).Cursor(ui.CursorPointer).Padding(tw(1), tw(2)).
			Radius(tw(1.5)).Gap(tw(1)).AlignItems(ui.Center).
			Background(p.subdued).Border(1, p.borderDefault)
		if inline {
			sel.Trigger.Shrink(0)
		} else {
			sel.Trigger.Justify(ui.SpaceBetween).Margin(4, 0, 0, 0)
		}
		sel.Trigger.Children(func() {
			label(c, selected, sizeCaption, 16, 500, p.text).SingleLine()
			ui.Icon(c, iconChevronDown).FontSize(11).TextColor(p.tertiary).Shrink(0)
		})
		sel.Popup(func(panel ui.Element) {
			panel.Padding(tw(1)).Radius(tw(2)).Background(p.popup).Border(1, p.borderDefault)
			if p.dark {
				panel.Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.35))
			} else {
				panel.Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.12))
			}
			if inline {
				panel.MinWidth(tw(40))
			}
			for _, o := range opts {
				item := sel.Item(o.label).Padding(tw(1.5), tw(2)).Radius(6).Gap(tw(1.5)).
					AlignItems(ui.Center).Cursor(ui.CursorPointer)
				if item.Highlighted() {
					item.Background(p.accentSurface)
				}
				item.Children(func() {
					ui.Box(c).Size(12, 12).Shrink(0).Children(func() {
						if o.value == current {
							ui.Icon(c, iconCheck).FontSize(12).TextColor(p.accent)
						}
					})
					label(c, o.label, sizeCaption, 16, 500, p.text).SingleLine()
				})
			}
		})
		if sel.Trigger.Changed() {
			set(byLabel[selected])
		}
	}
	if inline {
		ui.Row(c.Key("select-"+key)).Padding(tw(2.5), tw(3)).Gap(tw(3)).AlignItems(ui.Center).
			Justify(ui.SpaceBetween).Shrink(0).Children(func() {
			ui.Box(c).Grow(1).Basis(0).MinWidth(0).Children(func() {
				settingHead(c, p, title, hint)
			})
			draw()
		})
		return
	}
	ui.Column(c.Key("select-"+key)).Padding(tw(2.5), tw(3)).Gap(tw(1)).Shrink(0).Children(func() {
		settingHead(c, p, title, hint)
		draw()
	})
}

// settingField is a card row with a title, a hint and a text field under them.
func (a *app) settingField(c *ui.Context, p palette, key, title, hint, placeholder string, f *textSetting, commit func(string)) {
	ui.Column(c.Key("field-"+key)).Padding(tw(2.5), tw(3)).Gap(tw(1)).Shrink(0).Children(func() {
		settingHead(c, p, title, hint)
		box := ui.Row(c).Margin(4, 0, 0).Padding(tw(1), tw(2)).Radius(tw(1.5)).Shrink(0).Clip()
		var in ui.Element
		box.Children(func() {
			in = ui.TextInputBase(c.Key(key), &f.text).Placeholder(placeholder).Grow(1).MinWidth(0).
				FontSize(sizeCaption).TextColor(p.text).Label(title)
			in.Valid()
		})
		bg, border := p.subdued, p.borderDefault
		if in.Focused() {
			bg, border = p.card, p.borderFocus
		}
		box.Background(bg).Border(1, border)
		if in.Submitted() || (!in.Focused() && f.edited) {
			commit(f.text)
		}
		if in.Focused() {
			f.edited = true
		}
	})
}

func (a *app) dnsSection(c *ui.Context, p palette) {
	gateway := a.gateway()
	titled(c, p, "DNS & GATEWAY", nil, func() {
		cardBox(c, p, func() {
			a.selectRow(c, p, "gateway", "TUN Gateway",
				"The tunnel's own address. Pick one that doesn't clash with your network.",
				gateway, gatewayChoices(gateway), false, a.setGateway)
			cardDivider(c, p)
			doh := a.dohURL()
			a.selectRow(c, p, "doh", "DNS over HTTPS",
				"Resolves names for proxied traffic, through the proxy.",
				doh, dohChoices(doh), false, a.setDoH)
			cardDivider(c, p)
			a.settingField(c, p, "dns", "Bootstrap DNS", "Plain DNS for proxy server names, sent direct. Separate addresses with commas.",
				"1.1.1.1, 8.8.8.8", &a.dns, a.saveBootstrapDNS)
			cardDivider(c, p)
			a.settingField(c, p, "tundns", "TUN DNS", "DNS the tunnel advertises. Leave empty to use the bootstrap DNS.",
				"Same as bootstrap DNS", &a.tunDNS, a.saveTunDNS)
		})
	})
}

// logFollowCommand is how the person watches the daemon's log, which carries Xray's own
// warnings and errors too. It is the v1 panel's live tail, without needing root.
const logFollowCommand = "masir logs --follow"

func (a *app) loggingSection(c *ui.Context, p palette) {
	level := a.logLevel()
	titled(c, p, "LOGGING", nil, func() {
		cardBox(c, p, func() {
			a.selectRow(c, p, "loglevel", "Xray Log Level", "",
				level, logLevelChoices(), true, a.setLogLevel)
			cardDivider(c, p)
			ui.Column(c.Key("follow-log")).Padding(tw(2.5), tw(3)).Gap(tw(1)).Shrink(0).Children(func() {
				label(c, "Follow the Log", sizeSubhead, 18, 500, p.text)
				hint := "Xray's own warnings and errors appear in the daemon's log."
				if level == "none" {
					hint = "Nothing is logged while the level is Off."
				}
				label(c, hint, sizeCaption, 14.85, 400, p.muted)
				ui.Row(c).Margin(4, 0, 0).Gap(tw(1)).AlignItems(ui.Center).Children(func() {
					ui.Row(c).Grow(1).Basis(0).MinWidth(0).Padding(tw(1.5), tw(2)).Radius(tw(1.5)).
						Background(p.subdued).Border(1, p.borderDefault).Children(func() {
						mono(c, logFollowCommand, sizeCaption, 16, 400, p.text).SingleLine().Selectable()
					})
					if iconButton(c, "log-copy", iconCopy, "Copy the log command", p, iconSM, variantSubtle, 13) {
						c.WriteClipboard(logFollowCommand)
						a.notify("Copied " + logFollowCommand)
					}
					if iconButton(c, "log-terminal", iconTerminal, "Open in Terminal", p, iconSM, variantSubtle, 13) {
						if err := openTerminalIn(logFollowCommand); err != nil {
							a.fail("Couldn't open a terminal: ", err)
						}
					}
				})
				if !a.cliState().installed {
					label(c, "Install the command line tool below to run it.", sizeCaption, 14, 400, p.degraded)
				}
			})
			cardDivider(c, p)
			ui.Column(c.Key("xray-config")).Padding(tw(2.5), tw(3)).Gap(tw(1)).Shrink(0).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Justify(ui.SpaceBetween).Gap(tw(2)).Children(func() {
					label(c, "Xray Config", sizeSubhead, 18, 500, p.text)
					if thinButton(c, "open-config", "Open JSON", false, false, false, p) {
						a.openXrayConfig()
					}
				})
				label(c, "The configuration the daemon is running right now, as masir get-config prints it.",
					sizeCaption, 14.85, 400, p.muted)
			})
		})
	})
}

// openXrayConfig writes the configuration the daemon is running to a temporary file and
// opens it, as `masir get-config` prints it.
func (a *app) openXrayConfig() {
	a.async(func() {
		ctx, cancel := rpc()
		defer cancel()
		res, err := a.d.GetXrayConfig(ctx, &pb.GetXrayConfigRequest{})
		if err != nil {
			a.post(func() { a.fail("Couldn't read the config: ", err) })
			return
		}
		if res.GetJson() == "" {
			a.post(func() { a.notify("Xray isn't running, so there's no config yet") })
			return
		}
		var pretty bytes.Buffer
		if json.Indent(&pretty, []byte(res.GetJson()), "", "  ") != nil {
			pretty.Reset()
			pretty.WriteString(res.GetJson())
		}
		pretty.WriteByte('\n')
		path := filepath.Join(os.TempDir(), "masir-xray-config.json")
		if err := os.WriteFile(path, pretty.Bytes(), 0o600); err != nil {
			a.post(func() { a.fail("Couldn't write the config: ", err) })
			return
		}
		if err := openFileIn(path); err != nil {
			a.post(func() { a.fail("Couldn't open the config: ", err) })
		}
	})
}

func (a *app) privacySection(c *ui.Context, p palette) {
	titled(c, p, "PRIVACY", nil, func() {
		cardBox(c, p, func() {
			a.settingToggle(c, p, "analytics", "Anonymous Analytics",
				"Sends anonymous usage and crash data to help improve Masir. It never includes addresses, names, or the sites you visit.",
				a.analyticsOn(), a.setAnalytics)
		})
	})
}

// ---- saving the text settings ----

// applyTextSetting saves one settings field. When the daemon refuses it, the field goes
// back to what the daemon has and a toast says why.
func (a *app) applyTextSetting(f *textSetting, restore string, req *pb.UpdateSettingsRequest) {
	a.async(func() {
		ctx, cancel := rpc()
		defer cancel()
		if _, err := a.d.UpdateSettings(ctx, req); err != nil {
			a.post(func() {
				f.text = restore
				a.fail("Couldn't save the setting: ", err)
			})
		}
	})
}

// saveBootstrapDNS saves the plain resolvers used for proxy server names. At least one is
// required; the daemon refuses an empty list.
func (a *app) saveBootstrapDNS(text string) {
	a.dns.edited = false
	list, err := parseIPList(text)
	if err != nil || len(list) == 0 {
		if err == nil {
			a.notify("Bootstrap DNS needs at least one address")
		} else {
			a.notify(err.Error())
		}
		a.dns.text = strings.Join(a.status.GetSettings().GetDnsServers(), ", ")
		return
	}
	a.dns.text = strings.Join(list, ", ")
	saved := a.status.GetSettings().GetDnsServers()
	if sameList(list, saved) {
		return
	}
	a.applyTextSetting(&a.dns, strings.Join(saved, ", "), &pb.UpdateSettingsRequest{DnsServers: &pb.StringList{Values: list}})
}

// saveTunDNS saves the resolvers the tunnel advertises; an empty field means the bootstrap
// DNS.
func (a *app) saveTunDNS(text string) {
	a.tunDNS.edited = false
	list, err := parseIPList(text)
	if err != nil {
		a.notify(err.Error())
		a.tunDNS.text = strings.Join(a.status.GetSettings().GetTunDns(), ", ")
		return
	}
	a.tunDNS.text = strings.Join(list, ", ")
	saved := a.status.GetSettings().GetTunDns()
	if sameList(list, saved) {
		return
	}
	a.applyTextSetting(&a.tunDNS, strings.Join(saved, ", "), &pb.UpdateSettingsRequest{TunDns: &pb.StringList{Values: list}})
}

// parseIPList reads a comma- or space-separated list of IP addresses.
func parseIPList(text string) ([]string, error) {
	var out []string
	for _, f := range strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	}) {
		if net.ParseIP(f) == nil {
			return nil, fmt.Errorf("%s is not an IP address", f)
		}
		out = append(out, f)
	}
	return out, nil
}

// sameList reports whether two lists of addresses are the same, in any order.
func sameList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, s := range a {
		seen[s]++
	}
	for _, s := range b {
		seen[s]--
		if seen[s] < 0 {
			return false
		}
	}
	return true
}

// restoreDefaults puts back what the daemon says each setting is when it isn't
// customized. Power, the privacy choice, and the rules stay as the person left them.
func (a *app) restoreDefaults() {
	a.async(func() {
		ctx, cancel := rpc()
		defer cancel()
		res, err := a.d.GetSettings(ctx, &pb.GetSettingsRequest{})
		if err != nil {
			a.post(func() { a.fail("Couldn't load the defaults: ", err) })
			return
		}
		d := res.GetDefaults()
		req := &pb.UpdateSettingsRequest{
			AlwaysOnProxy:      proto.Bool(d.GetAlwaysOnProxy()),
			AllowLan:           proto.Bool(d.GetAllowLan()),
			ProxyPort:          proto.Uint32(d.GetProxyPort()),
			DohDns:             proto.String(d.GetDohDns()),
			DnsServers:         &pb.StringList{Values: append([]string(nil), d.GetDnsServers()...)},
			TunDns:             &pb.StringList{Values: append([]string(nil), d.GetTunDns()...)},
			TunGateway:         proto.String(d.GetTunGateway()),
			SubIntervalMinutes: proto.Uint32(d.GetSubIntervalMinutes()),
			XrayLogLevel:       proto.String(d.GetXrayLogLevel()),
		}
		if _, err := a.d.UpdateSettings(ctx, req); err != nil {
			a.post(func() { a.fail("Couldn't restore the defaults: ", err) })
			return
		}
		a.post(func() {
			a.portText, a.portEdited = fmt.Sprint(d.GetProxyPort()), false
			a.dns.text = strings.Join(d.GetDnsServers(), ", ")
			a.tunDNS.text = strings.Join(d.GetTunDns(), ", ")
			a.notify("Settings restored to their defaults")
		})
	})
}

// loginEnabled is whether the app starts at login. The system is asked once, not once a
// frame: the answer comes from the OS and is not instant.
func (a *app) loginEnabled() bool {
	if !a.loginChecked {
		a.loginChecked = true
		a.loginOn = openAtLogin()
	}
	return a.loginOn
}

// refreshLoginState asks the system for the login item in the background, so the settings
// page draws at once and catches up if it changed elsewhere.
func (a *app) refreshLoginState() {
	a.async(func() {
		on := openAtLogin()
		a.post(func() {
			if on {
				a.loginNeedsApproval = false
			}
			if a.loginChecked && a.loginOn == on {
				return
			}
			a.loginOn, a.loginChecked = on, true
		})
	})
}

func (a *app) setLoginEnabled(on bool) {
	err := setOpenAtLogin(on)
	if err != nil && on {
		// A login item left behind by an earlier copy of Masir can make register fail
		// ("already registered"). Removing it and registering again is what the person
		// did by hand; do it for them, once.
		if offErr := setOpenAtLogin(false); offErr == nil {
			err = setOpenAtLogin(true)
		}
	}
	if err != nil {
		a.loginNeedsApproval = strings.Contains(err.Error(), "Login Items")
		a.fail("Couldn't change it: ", err)
		return
	}
	a.loginNeedsApproval = false
	a.loginOn, a.loginChecked = on, true
}

// digitsOnly keeps the digits of s, capped at five: a port field that takes a number
// and nothing else.
func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' && b.Len() < 5 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// applyPort saves the port field, or puts the saved port back when it isn't a port.
func (a *app) applyPort() {
	a.portEdited = false
	n, err := strconv.Atoi(a.portText)
	if err != nil || n < 1024 || n > 65535 {
		a.notify("The port must be a number from 1024 to 65535")
		a.portText = fmt.Sprint(a.status.GetSettings().GetProxyPort())
		return
	}
	port := uint32(n)
	if port == a.status.GetSettings().GetProxyPort() {
		return
	}
	a.async(func() {
		ctx, cancel := rpc()
		defer cancel()
		if _, err := a.d.UpdateSettings(ctx, &pb.UpdateSettingsRequest{ProxyPort: &port}); err != nil {
			a.post(func() {
				a.portText = fmt.Sprint(a.status.GetSettings().GetProxyPort())
				a.fail("Couldn't save the setting: ", err)
			})
		}
	})
}

func (a *app) systemSection(c *ui.Context, p palette) {
	st := a.status
	cli := a.cliState()
	titled(c, p, "SYSTEM", nil, func() {
		cardBox(c, p, func() {
			ui.Column(c).Padding(tw(2.5), tw(3)).Gap(tw(1.5)).Shrink(0).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
					label(c, "Background Service", sizeSubhead, 18, 500, p.text)
					switch {
					case st.GetServiceInstalled() && st.GetServiceRunning():
						badge(c, "Active", nil, p.healthy, p.healthySurface, 0, 0)
					case st.GetServiceInstalled() && !st.GetServiceEnabled():
						badge(c, "Disabled", nil, p.degraded, p.degradedSurface, 0, 0)
					case st.GetServiceInstalled():
						badge(c, "Stopped", nil, p.degraded, p.degradedSurface, 0, 0)
					case st.GetPrivileged():
						badge(c, "This Session", nil, p.degraded, p.degradedSurface, 0, 0)
					default:
						badge(c, "Not Installed", nil, p.muted, p.neutralSurface, 0, 0)
					}
				})
				label(c, "Starts the daemon at boot, so connecting in tunnel mode never asks for permission again.", sizeCaption, 14.85, 400, p.muted)
				if st.GetServiceInstalled() && !st.GetServiceEnabled() {
					label(c, "The system has it turned off. Allow Masir in Login Items, or start it here.", sizeCaption, 14.85, 400, p.degraded)
				}
				ui.Column(c).Gap(tw(1)).Margin(4, 0, 0).Children(func() {
					if st.GetServiceInstalled() {
						if !st.GetServiceRunning() {
							if thinButton(c, "service-start", "Start Service", true, a.serviceBusy, a.serviceBusy, p) {
								a.startDaemonService()
							}
						}
						if runtime.GOOS == "darwin" && !st.GetServiceEnabled() {
							if thinButton(c, "service-login-items", "Open Login Items", false, false, false, p) {
								if err := openLoginItems(); err != nil {
									a.fail("Couldn't open Login Items: ", err)
								}
							}
						}
						if thinButton(c, "service-remove", "Remove Service", false, a.serviceBusy, a.serviceBusy, p) {
							a.removeDaemonService()
						}
					} else if thinButton(c, "service-install", "Install Service", true, a.serviceBusy, a.serviceBusy, p) {
						a.installDaemonService()
					}
				})
			})
			cardDivider(c, p)
			ui.Column(c).Padding(tw(2.5), tw(3)).Gap(tw(1.5)).Shrink(0).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Justify(ui.SpaceBetween).Children(func() {
					label(c, "Command Line Tool", sizeSubhead, 18, 500, p.text)
					if cli.installed {
						badge(c, "Installed", nil, p.healthy, p.healthySurface, 0, 0)
					} else {
						badge(c, "Not Installed", nil, p.muted, p.neutralSurface, 0, 0)
					}
				})
				switch {
				case cli.installed && cli.ours:
					label(c, "Use masir in your terminal; the app keeps this command up to date.", sizeCaption, 14.85, 400, p.muted)
				case cli.installed:
					label(c, "masir is already installed at "+cli.path+"; Masir leaves it unchanged.", sizeCaption, 14.85, 400, p.muted)
				default:
					label(c, "Use masir in your terminal; the app sets it up automatically.", sizeCaption, 14.85, 400, p.muted)
				}
				if !cli.installed {
					ui.Column(c).Gap(tw(1)).Margin(4, 0, 0).Children(func() {
						if thinButton(c, "cli-install", "Install", true, a.cliBusy, a.cliBusy, p) {
							a.installCommandLine()
						}
					})
				}
				if cli.needsShell {
					label(c, "Open a new terminal to use masir.", sizeCaption, 14, 400, p.degraded)
				} else if cli.installed && cli.ours && !cli.onPath {
					label(c, "Add "+cli.dir+" to your PATH to run it by name.", sizeCaption, 14, 400, p.degraded)
					mono(c, cli.path, sizeMicro, 13, 400, p.muted).MaxLines(2).Selectable()
				}
			})
			cardDivider(c, p)
			cardRow(c, "app", func() {
				ui.Column(c).Gap(tw(0.5)).Grow(1).Children(func() {
					label(c, "Masir GUI "+versionLabel(a.version), sizeSubhead, 18, 500, p.text)
					mono(c, "Daemon: "+versionLabel(st.GetVersion())+" · xray: "+st.GetXrayVersion(), sizeMicro, 13, 400, p.muted).MaxLines(2)
				})
				if a.showUpdateControl() {
					a.updateControl(c, p)
				}
			})
			cardDivider(c, p)
			ui.Column(c).Padding(tw(2.5), tw(3)).Gap(tw(1.5)).Shrink(0).Children(func() {
				label(c, "Resets the proxy port, DNS, gateway, refresh interval and logging. Your servers, rules, power and privacy choice stay as they are.",
					sizeCaption, 14.85, 400, p.muted)
				ui.Column(c).Gap(tw(1)).Margin(4, 0, 0).Children(func() {
					if thinButton(c, "reset-settings", "Restore Defaults", false, false, false, p) {
						a.restoreDefaults()
					}
				})
			})
		})
	})
}
