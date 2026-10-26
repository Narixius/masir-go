package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

// ---- text ----

// ltr makes a paragraph read left to right whatever script it is in, so it starts at the
// left edge: the design aligns names to the left even when they are in Persian or Arabic.
func ltr(s string) string { return "‎" + s }

// ltrLabel is label for names that may be in a right-to-left script: it is only as wide as
// its text and sits at the left edge of its column, where the design aligns names.
func ltrLabel(c *ui.Context, s string, size, line float32, weight int, color ui.Color) ui.Element {
	return label(c, ltr(s), size, line, weight, color).AlignSelf(ui.Start).SingleLine()
}

// label is text of the type scale: size and line height in DIPs, weight 400, 500 or 600.
func label(c *ui.Context, s string, size, line float32, weight int, color ui.Color) ui.Element {
	return ui.Text(c, s).FontSize(size).FixedLineHeight(line).FontWeight(weight).TextColor(color)
}

// mono is label in the monospaced face, with digits of one width.
func mono(c *ui.Context, s string, size, line float32, weight int, color ui.Color) ui.Element {
	return label(c, s, size, line, weight, color).Font("monospace").FontFeatures("tnum")
}

// ---- states ----

// tint fades an element's background between its colors as the pointer comes and goes.
func tint(e ui.Element, color ui.Color) ui.Element {
	return e.Background(e.AnimateColor("bg", color, appMotion(120*time.Millisecond)))
}

// ---- buttons ----

type iconSize int

const (
	iconXS iconSize = iota // 20x20, icon 11
	iconSM                 // 24x24, icon 13
	iconMD                 // 28x28, icon 15
)

type iconVariant int

const (
	variantGhost iconVariant = iota
	variantSubtle
)

// iconButton is a square button showing an icon. glyph overrides the icon's size.
func iconButton(c *ui.Context, key string, icon *ui.SVG, title string, p palette, size iconSize, variant iconVariant, glyph float32) bool {
	box, radius, def := tw(6), tw(1), float32(13) // w-6 rounded
	switch size {
	case iconXS:
		box, radius, def = tw(5), 4, 11 // w-5 rounded-[4px]
	case iconMD:
		box, radius, def = tw(7), tw(1), 15 // w-7, which every use rounds as rounded-sm
	}
	if glyph == 0 {
		glyph = def
	}
	b := ui.ButtonBase(c.Key(key)).Label(title).Tooltip(title).Size(box, box).Radius(radius).
		Cursor(ui.CursorPointer).Center().Shrink(0)
	hover := b.Hovered()
	switch variant {
	case variantSubtle:
		b.Border(1, p.borderSubtle)
		bg := p.cardHover
		if hover {
			bg = p.cardActive
		}
		tint(b, bg)
	default:
		bg := ui.Color{}
		if hover {
			bg = p.cardHover
		}
		tint(b, bg)
	}
	color := p.muted
	if hover {
		color = p.text
	}
	b.Children(func() {
		ui.Icon(c, icon).FontSize(glyph).TextColor(color)
	})
	return b.Clicked()
}

// ---- switch ----

type switchSize int

const (
	switchDefault switchSize = iota // 38x22
	switchSubdued                   // 26x15
)

// nativeSwitch is the switch of the design: a rounded track with a white knob that slides
// and stretches under the pointer. It reports the new value when clicked.
func nativeSwitch(c *ui.Context, key, name string, on bool, size switchSize, disabled, loading bool, p palette) (bool, bool) {
	trackW, trackH, pad, knob, travel, stretch := float32(38), float32(22), float32(2), float32(18), float32(16), float32(21)
	offBG, onBG := ui.Color{R: p.neutral.R, G: p.neutral.G, B: p.neutral.B, A: 77}, p.healthy // neutral at 30%
	knobShadow := ui.RGBA(0, 0, 0, 0.25)
	if size == switchSubdued {
		trackW, trackH, pad, knob, travel, stretch = 26, 15, 1.5, 12, 11, 15
		offBG = ui.Color{R: p.neutral.R, G: p.neutral.G, B: p.neutral.B, A: 64}
		onBG = ui.Color{R: p.healthy.R, G: p.healthy.G, B: p.healthy.B, A: 230}
		knobShadow = ui.RGBA(0, 0, 0, 0.2)
	}
	interactive := !disabled && !loading
	sw := ui.ButtonBase(c.Key(key)).Size(trackW, trackH).Padding(pad).Radius(trackH / 2).Shrink(0).
		AlignItems(ui.Center).Justify(ui.Start).Label(name)
	if interactive {
		sw.Cursor(ui.CursorPointer)
	} else {
		sw.Opacity(0.65)
	}
	target := offBG
	if on {
		target = onBG
	}
	sw.Background(sw.AnimateColor("track", target, ui.Animation{Duration: 200 * time.Millisecond, Ease: easeSwitch}))
	pressed := interactive && sw.Pressed()
	sw.Children(func() {
		x := float32(0)
		if on {
			x = travel
		}
		w := knob
		if pressed {
			w = stretch
			if on {
				x = travel - (stretch - knob)
			}
		}
		k := ui.Box(c).Height(knob).Radius(knob/2).Background(ui.Hex("#ffffff")).
			Shadow(0, 1, 3, 0, knobShadow).Shrink(0).Center()
		k.Width(k.AnimateWith("w", w, 200*time.Millisecond, easeSwitch))
		k.Translate(k.AnimateWith("x", x, 200*time.Millisecond, easeSwitch), 0)
		if loading {
			k.Children(func() { ui.Spinner(c).Size(knob*0.6, knob*0.6) })
		}
	})
	if interactive && sw.Clicked() {
		return !on, true
	}
	return on, false
}

// ---- segmented control ----

// segmented is the control of full width whose options share it equally.
func segmented(c *ui.Context, key string, options []string, selected int, p palette) (int, bool) {
	track := ui.Row(c.Key(key)).Padding(3).Gap(2).Radius(tw(2)).Background(p.cardHover).Shrink(0)
	chosen, changed := selected, false
	track.Children(func() {
		for i, name := range options {
			active := i == selected
			seg := ui.ButtonBase(c.Key(name)).Grow(1).Basis(0).Padding(5, 0).Radius(6).Cursor(ui.CursorPointer).Center()
			bg, fg, weight := ui.Color{}, p.muted, 500
			if active {
				bg, fg, weight = p.card, p.text, 600
			} else if seg.Hovered() {
				fg = p.text
			}
			seg.Background(seg.AnimateColor("bg", bg, appMotion(150*time.Millisecond)))
			if active {
				if p.dark {
					seg.Shadow(0, 1, 4, 0, ui.RGBA(0, 0, 0, 0.35))
				} else {
					seg.Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, 0.1))
				}
			}
			seg.Children(func() {
				label(c, name, sizeSubhead, 18, weight, seg.AnimateColor("fg", fg, appMotion(150*time.Millisecond)))
			})
			if seg.Clicked() && !active {
				chosen, changed = i, true
			}
		}
	})
	return chosen, changed
}

// ---- badges ----

// badge is a small pill with an icon and text; h is its fixed height, or 0.
func badge(c *ui.Context, text string, icon *ui.SVG, fg, bg ui.Color, h float32, glyph float32) {
	row := ui.Row(c).Gap(tw(1)).Padding(tw(0.5), tw(1.5)).Radius(6).Background(bg).Shrink(0).AlignItems(ui.Center)
	if h > 0 {
		row.Height(h)
	}
	row.Children(func() {
		if icon != nil {
			ui.Icon(c, icon).FontSize(glyph).TextColor(fg)
		}
		label(c, text, sizeCaption, 16, 500, fg).SingleLine()
	})
}

// latencyBadge shows a measurement: a dot and the milliseconds, colored by how slow it is.
// ms is negative for a failed probe and zero for none yet. It reports a click.
func latencyBadge(c *ui.Context, key string, ms int64, testing bool, p palette) bool {
	fg, bg, dot := p.tertiary, p.neutralSurface, ui.Color{}
	text, none := "—", false
	switch {
	case ms < 0:
		text = "Timeout"
		fg, bg = p.errorC, p.errorSurface
	case ms == 0:
		none = true
	case ms < 500:
		fg, bg, dot = p.healthy, p.healthySurface, p.healthy
		text = formatMs(ms)
	case ms < 1000:
		fg, bg, dot = p.degraded, p.degradedSurface, p.degraded
		text = formatMs(ms)
	default:
		fg, bg, dot = p.errorC, p.errorSurface, p.errorC
		text = formatMs(ms)
	}
	if testing {
		fg, bg = p.tertiary, p.neutralSurface
		dot = ui.Color{R: p.tertiary.R, G: p.tertiary.G, B: p.tertiary.B, A: 102}
	}
	b := ui.ButtonBase(c.Key(key)).Label("Test latency").Tooltip("Click to test latency now").Cursor(ui.CursorPointer).
		Shrink(0).AlignItems(ui.Center)
	if none || (ms < 0) {
		b.Padding(2.5, 6).Radius(10).MinWidth(28).Center()
		if ms < 0 {
			b.Padding(2.5, 7.5)
		}
	} else {
		b.Padding(2, 4.5, 3).Radius(4).Gap(tw(0.5))
	}
	b.Background(bg)
	if testing { // the probe in flight shimmers
		b.Opacity(0.775 + 0.225*b.Loop("shimmer", 1100*time.Millisecond, ui.EaseInOut))
	}
	b.Children(func() {
		if dot != (ui.Color{}) {
			ui.Box(c).Size(5, 5).Radius(2.5).Background(dot).Shrink(0)
		}
		if ms > 0 { // also while testing: the old number stays on its reels, so the result rolls from it
			// The number rolls; its separator and unit stay still.
			whole := strings.TrimSuffix(text, "ms")
			rollingNumber(c, key+"-ms", float64(ms), whole, sizeCaption, 14, 600, fg, true)
			mono(c, "ms", sizeCaption, 14, 600, fg)
			return
		}
		mono(c, text, sizeCaption, 14, 600, fg).SingleLine()
	})
	return b.Clicked()
}

func formatMs(ms int64) string {
	s := fmt.Sprint(ms)
	if len(s) > 3 {
		s = s[:len(s)-3] + "," + s[len(s)-3:]
	}
	return s + "ms"
}

// ---- search ----

// searchInput is the search field of the design: a magnifier, the text, and a clear
// button once there is some; it has no box of its own.
func searchInput(c *ui.Context, key string, value *string, placeholder string, p palette) {
	// h-7 with the icon at left-2 and the text from pl-7.
	row := ui.Row(c.Key(key)).Grow(1).Height(tw(7)).AlignItems(ui.Center).Padding(0, tw(1.5), 0, tw(2)).Gap(tw(7) - tw(2) - 12)
	row.Children(func() {
		ui.Icon(c, iconSearch).FontSize(12).TextColor(p.tertiary).Shrink(0)
		in := ui.TextInputBase(c.Key(key+"-input"), value).Placeholder(placeholder).Grow(1).
			FontSize(sizeCaption).TextColor(p.text).Label(placeholder).AutoFocus() // typing starts as the list opens
		_ = in
		if *value != "" {
			clear := ui.ButtonBase(c.Key(key+"-clear")).Label("Clear search").Size(tw(4), tw(4)).Radius(tw(2)).
				Background(p.cardHover).Center().Cursor(ui.CursorPointer).Shrink(0)
			clear.Children(func() { ui.Icon(c, iconX).FontSize(10).TextColor(p.muted) })
			if clear.Clicked() {
				*value = ""
			}
		}
	})
}

// ---- flags and names ----

// flagOf splits a leading flag emoji (two regional indicators) from a name.
func flagOf(name string) (flag, rest string) {
	r := []rune(name)
	if len(r) >= 2 && isRegional(r[0]) && isRegional(r[1]) {
		return string(r[:2]), strings.TrimSpace(string(r[2:]))
	}
	return "", name
}

func isRegional(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }

// countryOf is the English name of the country a flag emoji stands for, or "" when it
// stands for none.
func countryOf(flag string) string {
	r := []rune(flag)
	if len(r) != 2 || !isRegional(r[0]) || !isRegional(r[1]) {
		return ""
	}
	code := string([]rune{r[0] - 0x1F1E6 + 'A', r[1] - 0x1F1E6 + 'A'})
	region, err := language.ParseRegion(code)
	if err != nil {
		return ""
	}
	return display.English.Regions().Name(region)
}

// serverTitle is what a server is called in the panel: its country when the name starts
// with a flag (the provider's own name, often in another script, goes to the subtitle),
// else the name. A server without a flag gets the globe.
func serverTitle(name string) (flag, title, detail string) {
	flag, rest := flagOf(name)
	if country := countryOf(flag); country != "" {
		return flag, country, rest
	}
	if flag == "" {
		flag = "🌐"
	}
	return flag, rest, ""
}

// speed formats bytes per second as the footer shows them: "12KB/s".
func speed(bps uint64) string {
	switch {
	case bps >= 10<<20:
		return fmt.Sprintf("%.0fMB/s", float64(bps)/(1<<20))
	case bps >= 1<<20:
		return fmt.Sprintf("%.1fMB/s", float64(bps)/(1<<20))
	default:
		return fmt.Sprintf("%.0fKB/s", float64(bps)/1024)
	}
}

// duration formats how long masir has been on: "Just connected", "5m", "1h 12m".
func duration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "Just connected"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
