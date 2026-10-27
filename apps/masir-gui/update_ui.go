package main

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// updateControlWidth is the whole control: the blue button and the small action beside it.
// The button keeps the same width when it becomes a progress bar.
var updateControlWidth = tw(42)

// updateControl draws the update button, its progress bar, and the small ignore or cancel
// action beside it. Both the footer and Settings use it.
func (a *app) updateControl(c *ui.Context, p palette) {
	xSlot, gap := tw(5), tw(1)
	mainW := updateControlWidth - xSlot - gap
	h := tw(6)
	r := tw(2)

	main := ui.ButtonBase(c.Key("update")).Width(mainW).Height(h).Radius(r).Shrink(0).
		Cursor(ui.CursorPointer).Clip().Center()
	text, icon, bg, fg := a.updateLook(p)
	main.Background(bg).Label(text)
	if fg.A > 0 {
		main.TextColor(fg)
	}
	if a.upd.phase == updateAvailable {
		main.Shadow(0, 1, 3, 0, p.accent.Alpha(0.35))
	}
	if main.Hovered() && (a.upd.phase == updateAvailable || a.upd.phase == updateFailed) {
		main.Background(p.accentHover)
		if a.upd.phase == updateFailed {
			main.Background(p.errorSurface)
		}
	}
	main.Children(func() {
		pct := a.updatePercent()
		switch {
		case a.upd.phase == updateAvailable:
		case a.upd.phase == updateFailed:
		case pct > 0 && (a.upd.phase == updateDownloading || a.upd.phase == updatePaused ||
			a.upd.phase == updateAppDownloading || a.upd.phase == updateAppPaused):
			ui.Box(c).Absolute().Top(0).Bottom(0).Left(0).WidthPercent(float32(max(pct, 3))).
				Background(p.accent)
		default:
			// An indeterminate phase: a short filled lead-in and a spinner are enough.
			ui.Box(c).Absolute().Top(0).Bottom(0).Left(0).WidthPercent(28).Background(p.accent)
		}
		ui.Row(c).Absolute().Top(0).Bottom(0).Left(0).Right(0).Center().Gap(tw(1)).Children(func() {
			label(c, text, sizeCaption, 15, 500, fg).SingleLine()
			if icon != nil {
				ui.Icon(c, icon).FontSize(11).TextColor(fg)
			}
		})
	})

	clicked := main.Clicked()
	switch a.upd.phase {
	case updateAvailable:
		if clicked {
			a.startUpdate()
		}
	case updateDownloading, updateAppDownloading:
		if clicked {
			a.pauseUpdate()
		}
	case updatePaused, updateAppPaused:
		if clicked {
			a.resumeUpdate()
		}
	case updateFailed:
		if clicked {
			a.retryUpdate()
		}
	}

	// The small slot is always there, so showing the X never makes the footer jump.
	ui.Box(c).Width(xSlot).Height(h).Center().Shrink(0).Children(func() {
		switch a.upd.phase {
		case updateAvailable, updateFailed:
			if iconButton(c, "update-ignore", iconX, "Ignore this update", p, iconXS, variantGhost, 11) {
				a.ignoreUpdate()
			}
		case updatePaused, updateAppPaused:
			if iconButton(c, "update-stop", iconStop, "Cancel the update", p, iconXS, variantGhost, 10) {
				a.cancelUpdate()
			}
		}
	})
}

// updateLook returns the control's label, trailing icon, background and foreground.
func (a *app) updateLook(p palette) (text string, icon *ui.SVG, bg, fg ui.Color) {
	text, fg = a.updateLabel(), ui.Hex("#ffffff")
	bg = p.accent
	switch a.upd.phase {
	case updateAvailable:
		icon = iconDownload
	case updateDownloading:
		icon, bg = iconPause, p.accentHover
	case updatePaused:
		icon, bg = iconPlay, p.accentHover
	case updateAppDownloading:
		icon, bg = iconPause, p.accentHover
	case updateAppPaused:
		icon, bg = iconPlay, p.accentHover
	case updateStarting, updateVerifying, updateInstalling, updateRestarting, updateAppInstalling:
		icon, bg = nil, p.accentHover // the filled lead-in shows movement
	case updateFailed:
		icon = iconRefresh
		bg, fg = p.errorSurface, p.errorC
	}
	if text == "" {
		text = fmt.Sprintf("Update %d%%", a.updatePercent())
	}
	return
}
