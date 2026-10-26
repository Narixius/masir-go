//go:build !darwin

package main

import (
	"math"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// show opens the app's window, or brings it back: a normal window with a title bar and a
// taskbar entry, whose close button puts it away in the tray. It is not resizable by the
// user: its height follows the page and its lists.
func (s *shell) show() {
	if s.open {
		s.win.Focus()
		return
	}
	height := int(math.Round(float64(s.a.measure())))
	s.start()
	if s.win == nil {
		s.win = mygo.NewWindow(mygo.WindowOptions{
			Title:             "Masir",
			Width:             panelWidth,
			Height:            height,
			UseContentSize:    true,
			DisableResize:     true, // the content decides its height
			DisableMaximize:   true,
			DisableFullScreen: true,
			Hidden:            true,
			BackgroundColor:   "light-dark(#f5f5f7, #1e1e1e)",
			Content:           ui.View(s.a.view),
		})
		s.win.OnClose(func(e *mygo.CloseEvent) {
			if s.quitting { // quitting closes it for good
				return
			}
			if s.tray == nil {
				// Without a tray icon there is no way back to the window: quit, and leave
				// the daemon running.
				s.quitting = true
				mygo.App.Quit()
				return
			}
			e.PreventDefault()
			s.hide() // closing puts it away in the tray
		})
	}
	s.wantHeight, s.lastHeight, s.sizeQueued = height, height, false
	s.win.SetContentSize(panelWidth, height)
	s.open = true
	s.win.Show()
	s.win.Focus()
	s.sync()
}

func (s *shell) hide() {
	if s.win == nil || !s.open {
		return
	}
	s.win.Hide()
	s.open = false
	s.sizeQueued = false
	s.stop()
}

// beforeTrayMenu leaves the window alone: its menu is the tray's, not the window's.
func (s *shell) beforeTrayMenu() {}

// platformReady does nothing: a window stays where it is while the app is inactive.
func (s *shell) platformReady() {}
