package main

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// palette holds the colors of the design, light and dark: the same tokens the panel has
// always used, as translucent surfaces over the system's material.
type palette struct {
	// Text hierarchy.
	text, muted, tertiary, quaternary ui.Color
	// Surfaces and layers.
	rootTint                                 ui.Color // tints the material under the whole panel
	card, cardHover, cardActive              ui.Color
	elevated, subdued, inset                 ui.Color
	borderSubtle, borderDefault, borderFocus ui.Color
	// Status.
	healthy, healthySurface   ui.Color
	degraded, degradedSurface ui.Color
	errorC, errorSurface      ui.Color
	errorBorder               ui.Color
	neutral, neutralSurface   ui.Color
	// Destinations and accent.
	direct, directSurface ui.Color
	pinned, pinnedSurface ui.Color
	accent, accentHover   ui.Color
	accentSurface         ui.Color
	// Scroll bar thumb.
	scrollbar, scrollbarHover ui.Color
	dark                      bool
}

func paletteFor(c *ui.Context) palette {
	if c.Theme().Dark {
		return palette{
			dark: true,
			text: ui.Hex("#f5f5f7"), muted: ui.RGBA(255, 255, 255, 0.45), tertiary: ui.RGBA(255, 255, 255, 0.35), quaternary: ui.RGBA(255, 255, 255, 0.25),
			rootTint: ui.RGBA(26, 26, 28, 0.32),
			card:     ui.RGBA(255, 255, 255, 0.07), cardHover: ui.RGBA(255, 255, 255, 0.05), cardActive: ui.RGBA(255, 255, 255, 0.08),
			elevated: ui.RGBA(30, 30, 30, 0.85), subdued: ui.RGBA(0, 0, 0, 0.25), inset: ui.RGBA(255, 255, 255, 0.06),
			borderSubtle: ui.RGBA(255, 255, 255, 0.05), borderDefault: ui.RGBA(255, 255, 255, 0.10), borderFocus: ui.Hex("#0a84ff"),
			healthy: ui.Hex("#00d688"), healthySurface: ui.RGBA(0, 192, 120, 0.16),
			degraded: ui.Hex("#ff9f0a"), degradedSurface: ui.RGBA(255, 149, 0, 0.16),
			errorC: ui.Hex("#ff453a"), errorSurface: ui.RGBA(255, 69, 58, 0.16), errorBorder: ui.RGBA(255, 69, 58, 0.25),
			neutral: ui.Hex("#8e8e93"), neutralSurface: ui.RGBA(255, 255, 255, 0.08),
			direct: ui.Hex("#94a3b8"), directSurface: ui.RGBA(148, 163, 184, 0.15),
			pinned: ui.Hex("#0a84ff"), pinnedSurface: ui.RGBA(10, 132, 255, 0.15),
			accent: ui.Hex("#0a84ff"), accentHover: ui.Hex("#0066d6"), accentSurface: ui.RGBA(10, 132, 255, 0.15),
			scrollbar: ui.RGBA(255, 255, 255, 0.28), scrollbarHover: ui.RGBA(255, 255, 255, 0.45),
		}
	}
	return palette{
		text: ui.Hex("#1d1d1f"), muted: ui.RGBA(0, 0, 0, 0.45), tertiary: ui.RGBA(0, 0, 0, 0.35), quaternary: ui.RGBA(0, 0, 0, 0.25),
		rootTint: ui.RGBA(255, 255, 255, 0.22),
		card:     ui.Hex("#ffffff"), cardHover: ui.RGBA(0, 0, 0, 0.035), cardActive: ui.RGBA(0, 0, 0, 0.06),
		elevated: ui.RGBA(255, 255, 255, 0.85), subdued: ui.RGBA(0, 0, 0, 0.03), inset: ui.RGBA(0, 0, 0, 0.04),
		borderSubtle: ui.RGBA(0, 0, 0, 0.05), borderDefault: ui.RGBA(0, 0, 0, 0.08), borderFocus: ui.Hex("#007aff"),
		healthy: ui.Hex("#00c078"), healthySurface: ui.RGBA(0, 192, 120, 0.10),
		degraded: ui.Hex("#ff9500"), degradedSurface: ui.RGBA(255, 149, 0, 0.12),
		errorC: ui.Hex("#ff3b30"), errorSurface: ui.RGBA(255, 59, 48, 0.10), errorBorder: ui.RGBA(255, 59, 48, 0.20),
		neutral: ui.Hex("#787880"), neutralSurface: ui.RGBA(0, 0, 0, 0.05),
		direct: ui.Hex("#475569"), directSurface: ui.RGBA(100, 116, 139, 0.10),
		pinned: ui.Hex("#007aff"), pinnedSurface: ui.RGBA(0, 122, 255, 0.10),
		accent: ui.Hex("#007aff"), accentHover: ui.Hex("#0066d6"), accentSurface: ui.RGBA(0, 122, 255, 0.10),
		scrollbar: ui.RGBA(0, 0, 0, 0.26), scrollbarHover: ui.RGBA(0, 0, 0, 0.42),
	}
}

// Sizes of the panel, in DIPs.
const (
	panelWidth  = 300
	panelRadius = 14
	cardRadius  = 9.75 // rounded-xl

	// The type scale.
	sizeTitle   = 14
	sizeBody    = 13
	sizeSubhead = 12
	sizeCaption = 11
	sizeMicro   = 10

	// Heights of the screens that don't size themselves to their content.
	settingsHeight        = 520
	addSubscriptionHeight = 340
)

// tw converts Tailwind spacing units to DIPs: the design sets its root font size to 13px, so
// a unit (0.25rem) is 3.25 and p-2.5, gap-1.5 and w-7 are not what they are at 16px.
func tw(n float32) float32 { return n * 3.25 }

// The motion of the design: 200ms on CSS's "ease"-like curve for the accordions and the
// pages, and a springier curve for the switch.
const motionDuration = 200 * time.Millisecond

var (
	easeApp    = ui.CubicBezier(0.25, 0.1, 0.25, 1)
	easeSwitch = ui.CubicBezier(0.16, 1, 0.3, 1)
)

func appMotion(d time.Duration) ui.Animation { return ui.Animation{Duration: d, Ease: easeApp} }
