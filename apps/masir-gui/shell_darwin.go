//go:build darwin

package main

import (
	"math"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// show opens the panel in the system's popover, anchored to the tray icon: AppKit draws
// its material, arrow and shadow, and animates it.
func (s *shell) show() {
	if s.open {
		s.win.Focus()
		s.a.checkForUpdateOnForeground()
		return
	}
	height := int(math.Round(float64(s.a.heightForOpen())))
	s.start()
	s.win = mygo.NewFlyout(mygo.FlyoutOptions{
		Tray:      s.tray,
		Placement: mygo.PlacementBottom,
		Width:     panelWidth,
		Height:    height,
		Focusable: true,
		Shadow:    true, // macOS's popover has its own
		Popover:   true,
		Content:   ui.View(s.a.view),
	})
	s.open = true
	s.wantHeight, s.lastHeight, s.sizeQueued = height, height, false
	s.a.askedHeight = float32(height)
	s.win.OnClosed(func() {
		s.win, s.open, s.sizeQueued = nil, false, false
		s.stop()
	})
	// A panel is opened to look at masir, so it is also the moment to ask whether a new
	// release is out. The check never blocks the open frame.
	s.a.checkForUpdateOnForeground()
}

func (s *shell) hide() {
	if s.win != nil {
		s.win.Close()
	}
}

// beforeTrayMenu closes the popover before its menu shows, as a status item's menu does.
func (s *shell) beforeTrayMenu() {
	if s.win != nil {
		s.win.Close()
	}
}

// platformReady closes the popover when the app is no longer active, as the popovers of
// menu bar apps do.
func (s *shell) platformReady() {
	s.frameSize = true // the popover follows the springs, as a window of its own does
	mygo.App.OnDidResignActive(func() {
		if s.win != nil {
			s.win.Close()
		}
	})
}
