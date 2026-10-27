//go:build !darwin

package main

import (
	"math"
	"runtime"

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
		opts := mygo.WindowOptions{
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
		}
		if runtime.GOOS == "windows" {
			// A material puts the window on MyGo's DirectComposition path, which shows
			// frames whole and clips them, instead of stretching a frame built for one
			// height into a window of another while it resizes. The content draws its own
			// opaque surface over the material, so the window looks the same.
			opts.Vibrancy = mygo.VibrancyMica
		}
		s.win = mygo.NewWindow(opts)
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

// platformReady makes the window follow the content's height every frame: a window of its
// own has no popover resize animation to fight, and a big jump would stretch the old frame
// until the new one is drawn.
func (s *shell) platformReady() { s.frameSize = true }
