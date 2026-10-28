package main

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

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
	ui.Column(c).Padding(tw(4)).Fill().Children(func() {
		a.screenHeader(c, p, "Settings")
		ui.Scroll(c.Key("settings-scroll")).Grow(1).Basis(0).MinHeight(0).Gap(tw(3)).Padding(tw(1), 0, 0).Children(func() {
			a.subscriptionsSection(c, p)
			a.advancedSection(c, p)
			a.systemSection(c, p)
			if button(c, "quit", "Quit Masir", false, false, false, p) {
				quit()
			}
		})
	})
}

func (a *app) subscriptionsSection(c *ui.Context, p palette) {
	add := func() {
		b := ui.ButtonBase(c.Key("sub-add")).Gap(tw(1)).AlignItems(ui.Center).Cursor(ui.CursorPointer)
		b.Children(func() {
			ui.Icon(c, iconPlus).FontSize(12).TextColor(p.accent)
			label(c, "Add", sizeCaption, 14, 500, p.accent)
		})
		if b.Clicked() {
			a.openScreen(screenAddSubscription)
		}
	}
	titled(c, p, "SUBSCRIPTIONS", add, func() {
		if len(a.subs) == 0 {
			cardBox(c, p, func() {
				ui.Column(c).AlignItems(ui.Center).Gap(tw(2)).Padding(tw(4)).Children(func() {
					label(c, "No subscriptions imported yet.", sizeCaption, 15, 400, p.muted)
					if button(c, "empty-add", "Add Subscription", true, false, false, p) {
						a.openScreen(screenAddSubscription)
					}
				})
			})
			return
		}
		ui.Column(c).Gap(tw(2.5)).Shrink(0).Children(func() {
			for _, s := range a.subs {
				a.subscriptionCard(c, p, s)
			}
		})
	})
}

func (a *app) subscriptionCard(c *ui.Context, p palette, s *pb.Subscription) {
	cardBox(c, p, func() {
		ui.Row(c.Key("sub-head-"+s.Id)).Padding(tw(2.5), tw(3)).Gap(tw(2)).Background(p.subdued).AlignItems(ui.Center).Justify(ui.SpaceBetween).Shrink(0).Children(func() {
			ui.Column(c).Grow(1).Basis(0).MinWidth(0).MarginEnd(tw(2)).Children(func() {
				ltrLabel(c, s.Name, sizeBody, 18, 600, p.text)
				ltrLabel(c, fmt.Sprintf("%d outbounds · %s", s.ConfigCount, s.Url), sizeCaption, 14, 400, p.muted)
			})
			ui.Row(c).Gap(tw(1)).AlignItems(ui.Center).Shrink(0).Children(func() {
				if iconButton(c, "refresh-"+s.Id, iconRefresh, "Refresh subscription", p, iconSM, variantGhost, 13) {
					a.refreshSubscription(s.Id)
				}
				a.holdToDelete(c, p, s.Id)
			})
		})
		cardDivider(c, p)
		servers := a.serversOf(s.Id)
		a.subItems[s.Id] = servers
		ui.List(c, a.subList(s.Id), len(servers), func(i int) {
			a.serverItem(c, p, servers[i])
		}).MaxHeight(160).Padding(tw(1), tw(1.5), tw(1), tw(1)).Gap(2).Shrink(0)
	})
}

// serversOf returns the servers of one subscription, in the order the list has them.
func (a *app) serversOf(id string) []server {
	var out []server
	for _, s := range a.servers {
		if s.key.sub == id {
			out = append(out, s)
		}
	}
	return out
}

// subList is the list of a subscription's servers in settings, kept so its scroll and rows
// survive the frames.
func (a *app) subList(id string) *ui.ListState {
	s := a.subLists[id]
	if s == nil {
		s = &ui.ListState{}
		s.Key = func(i int) any {
			items := a.subItems[id]
			if i < 0 || i >= len(items) {
				return nil
			}
			return items[i].key
		}
		s.Label = func(i int) string {
			items := a.subItems[id]
			if i < 0 || i >= len(items) {
				return ""
			}
			_, title, _ := serverTitle(items[i].cfg.GetName())
			return title
		}
		a.subLists[id] = s
	}
	return s
}

// holdToDelete is the trash button that deletes only when held for a while, filling red
// as it goes.
func (a *app) holdToDelete(c *ui.Context, p palette, id string) {
	const hold = 900 * time.Millisecond
	b := ui.ButtonBase(c.Key("remove-"+id)).Label("Hold to delete subscription").Tooltip("Hold to delete subscription").
		Size(tw(6), tw(6)).Radius(tw(1)).Center().Clip().Cursor(ui.CursorPointer).Shrink(0)
	frac := float32(0)
	if b.Pressed() {
		if a.holdID != id {
			a.holdID, a.holdStart = id, time.Now()
		}
		frac = float32(time.Since(a.holdStart)) / float32(hold)
		if frac >= 1 {
			a.holdID = ""
			a.removeSubscription(id)
		} else {
			c.Invalidate()
		}
	} else if a.holdID == id {
		a.holdID = ""
	}
	bg := ui.Color{}
	if b.Hovered() {
		bg = p.cardHover
	}
	tint(b, bg)
	b.Children(func() {
		if frac > 0 {
			ui.Box(c).Absolute().Left(0).Top(0).Bottom(0).Width(tw(6) * min(frac, 1)).Background(p.errorSurface)
		}
		color := p.muted
		if frac > 0 || b.Hovered() {
			color = p.errorC
		}
		ui.Icon(c, iconTrash).FontSize(13).TextColor(color)
	})
}

func (a *app) advancedSection(c *ui.Context, p palette) {
	set := a.status.GetSettings()
	titled(c, p, "ADVANCED", nil, func() {
		cardBox(c, p, func() {
			always := a.alwaysOnProxy()
			a.settingToggle(c, p, "always", "Always-on Proxy Port", "A local proxy port always available for apps you configure manually, using your active outbound.", always, func(on bool) {
				a.setAlwaysOnProxy(on)
			})
			cardDivider(c, p)
			ui.Row(c.Key("port-row")).Padding(tw(2.5), tw(3)).Gap(tw(3)).AlignItems(ui.Center).Justify(ui.SpaceBetween).Shrink(0).Children(func() {
				ui.Column(c).Gap(tw(0.5)).Grow(1).Basis(0).MinWidth(0).Children(func() {
					label(c, "Port Number", sizeSubhead, 18, 500, p.text)
					label(c, "Mixed SOCKS5 / HTTP", sizeCaption, 15, 400, p.muted)
				})
				box := ui.Row(c).Width(76).Padding(tw(1), tw(2)).Radius(tw(1.5)).Shrink(0).Clip()
				var in ui.Element
				box.Children(func() {
					in = ui.TextInputBase(c.Key("port"), &a.portText).Grow(1).MinWidth(0).FontSize(sizeSubhead).TextAlign(ui.End).
						Font("monospace").FontWeight(500).TextColor(p.text)
					in.Valid()
				})
				bg, border := p.subdued, p.borderDefault
				if in.Focused() {
					bg, border = p.card, p.borderFocus
				}
				box.Background(bg).Border(1, border)
				if in.Submitted() || (!in.Focused() && a.portText != fmt.Sprint(set.GetProxyPort()) && a.portEdited) {
					a.applyPort()
				}
				if in.Focused() {
					a.portEdited = true
				}
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

// applyPort saves the port field, or puts the saved port back when it isn't a port.
func (a *app) applyPort() {
	a.portEdited = false
	n, err := strconv.Atoi(strings.TrimSpace(a.portText))
	if err != nil || n < 1024 || n > 65535 {
		a.notify("The port must be a number from 1024 to 65535")
		a.portText = fmt.Sprint(a.status.GetSettings().GetProxyPort())
		return
	}
	port := uint32(n)
	if port == a.status.GetSettings().GetProxyPort() {
		return
	}
	a.updateSettings(&pb.UpdateSettingsRequest{ProxyPort: &port})
}

func (a *app) systemSection(c *ui.Context, p palette) {
	st := a.status
	cli := a.cliState()
	titled(c, p, "SYSTEM", nil, func() {
		cardBox(c, p, func() {
			cardRow(c, "app", func() {
				ui.Column(c).Gap(tw(0.5)).Grow(1).Children(func() {
					label(c, "Masir", sizeSubhead, 18, 500, p.text)
					mono(c, versionLabel(a.version), sizeMicro, 13, 400, p.muted)
				})
				if a.showUpdateControl() {
					a.updateControl(c, p)
				}
			})
			cardDivider(c, p)
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
				ui.Row(c).Gap(tw(2)).Margin(4, 0, 0).Children(func() {
					if st.GetServiceInstalled() {
						if !st.GetServiceRunning() {
							if button(c, "service-start", "Start Service", true, a.serviceBusy, a.serviceBusy, p) {
								a.startDaemonService()
							}
						}
						if runtime.GOOS == "darwin" && !st.GetServiceEnabled() {
							if button(c, "service-login-items", "Open Login Items", false, false, false, p) {
								if err := openLoginItems(); err != nil {
									a.fail("Couldn't open Login Items: ", err)
								}
							}
						}
						if button(c, "service-remove", "Remove Service", false, a.serviceBusy, a.serviceBusy, p) {
							a.removeDaemonService()
						}
					} else if button(c, "service-install", "Install Service", true, a.serviceBusy, a.serviceBusy, p) {
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
				ui.Row(c).Gap(tw(2)).Margin(4, 0, 0).Children(func() {
					switch {
					case cli.installed && cli.ours:
						if button(c, "cli-remove", "Remove", false, a.cliBusy, a.cliBusy, p) {
							a.removeCommandLine()
						}
					case !cli.installed:
						if button(c, "cli-install", "Install", true, a.cliBusy, a.cliBusy, p) {
							a.installCommandLine()
						}
					}
				})
				if cli.needsShell {
					label(c, "Open a new terminal to use masir.", sizeCaption, 14, 400, p.degraded)
				} else if cli.installed && cli.ours && !cli.onPath {
					label(c, "Add "+cli.dir+" to your PATH to run it by name.", sizeCaption, 14, 400, p.degraded)
					mono(c, cli.path, sizeMicro, 13, 400, p.muted).MaxLines(2).Selectable()
				}
			})
			cardDivider(c, p)
			cardRow(c, "daemon", func() {
				ui.Column(c).Gap(tw(0.5)).Grow(1).Children(func() {
					label(c, "Daemon", sizeSubhead, 18, 500, p.text)
					mono(c, versionLabel(st.GetVersion())+" · xray "+st.GetXrayVersion(), sizeMicro, 13, 400, p.muted).MaxLines(2)
				})
			})
		})
	})
}
