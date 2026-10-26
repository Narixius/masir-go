// Masir is the desktop app for the masir daemon: a tray icon whose panel turns masir on
// and off, picks the server, and shows the rules. One tray icon on every OS: its panel is
// the system's popover on macOS, and a window of its own elsewhere.
package main

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/egoist/mygo"

	"github.com/narixius/masir-go/libs/masir-core/client"
)

// version is set at build time: -X main.version=...
var version = "dev"

// shell holds what only exists with a running app: the tray and the panel.
type shell struct {
	a        *app
	tray     *mygo.Tray
	win      *mygo.Window // the open popover, or the window while it is hidden
	open     bool
	quitting bool

	// The window's size, coalesced: the view asks for it once per change and never frame
	// by frame, and the shell applies the last ask after the frame that made it.
	wantHeight int
	lastHeight int
	sizeQueued bool
}

func (s *shell) toggle() {
	if s.open {
		s.hide()
		return
	}
	s.show()
}

// start resets the panel to the main page and follows the daemon's traffic while it shows.
func (s *shell) start() {
	s.a.screen, s.a.open, s.a.lastSub = screenMain, sectionNone, screenMain
	s.a.motion = motion{instant: s.a.motion.instant} // a new panel starts at rest
	s.a.lastMotion = time.Time{}
	s.a.onHeight = s.setHeight
	s.a.watchTraffic()
}

// stop stops the traffic stream when the panel goes away.
func (s *shell) stop() {
	s.a.stopTraffic()
}

// setHeight resizes the panel once, after the frame that asked: never from inside a
// frame, and never while something moves. The view asks at rest points only, so the
// window and its content stay together.
func (s *shell) setHeight(h float32) {
	if s.win == nil || !s.open || h <= 0 {
		return
	}
	s.wantHeight = int(round32(h))
	if s.wantHeight == s.lastHeight || s.sizeQueued {
		return
	}
	s.sizeQueued = true
	w := s.win
	w.Update(func() {
		s.sizeQueued = false
		if s.win != w || !s.open || s.wantHeight == s.lastHeight {
			return
		}
		s.lastHeight = s.wantHeight
		w.SetContentSize(panelWidth, s.wantHeight)
	})
}

// sync follows the state: the panel's redraw and the tray's icon and tooltip.
func (s *shell) sync() {
	if s.win != nil {
		s.win.Invalidate()
	}
	s.a.updateTray(s.tray)
}

// ready puts the tray in place and hooks the app's life. A missing tray is not fatal:
// without one (some Linux desktops), the window is still the way in.
func (s *shell) ready() {
	if icon, err := trayIcon(false); err != nil {
		log.Print("tray icon: ", err)
	} else if t, err := mygo.NewTray(mygo.TrayOptions{
		ID:             "masir",
		Icon:           icon,
		IconIsTemplate: runtime.GOOS == "darwin", // the menu bar tints its own; elsewhere the icon keeps its color
		ToolTip:        "Masir",
	}); err != nil {
		log.Print("tray: ", err)
	} else {
		s.tray = t
		menu := mygo.NewMenu([]*mygo.MenuItem{
			{Label: "Open Masir", Click: func(*mygo.MenuItem, *mygo.Window) { s.show() }},
			mygo.Separator(),
			{Role: mygo.RoleQuit},
		})
		if runtime.GOOS == "linux" {
			s.tray.SetMenu(menu) // AppIndicator tells nothing of clicks
		} else {
			s.tray.OnClick(s.toggle)
			s.tray.OnRightClick(func() {
				s.beforeTrayMenu()
				s.tray.SetMenu(menu) // for this popup only, so clicks keep toggling the panel
				defer s.tray.SetMenu(nil)
				s.tray.PopUpMenu()
			})
		}
	}
	s.platformReady()
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		s.quitting = true
		if s.tray != nil {
			s.tray.Destroy()
		}
	})
}

// daemonBinary is the masir executable the app starts a daemon from: the one shipped
// in the app, MASIR_BIN, or masir on the PATH.
func daemonBinary() string {
	if p := os.Getenv("MASIR_BIN"); p != "" {
		return p
	}
	if p := bundledBinary(); p != "" {
		return p
	}
	name := "masir"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

// bundledBinary is the masir the app ships with, even when another is on the PATH: the
// commands the app installs should be this exact build.
func bundledBinary() string {
	for _, p := range bundledBinaryPaths() {
		if fileExists(p) {
			return p
		}
	}
	return ""
}

func bundledBinaryPaths() []string {
	name := "masir"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	dir, err := mygo.App.Path(mygo.PathResources)
	if err != nil {
		return nil
	}
	// In bin: on Windows a file called masir.exe would be the app's own Masir.exe.
	return []string{filepath.Join(dir, "bin", name), filepath.Join(dir, name)}
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func round32(f float32) float32 {
	return float32(int(f + 0.5))
}

func main() {
	name := "Masir"
	if n := os.Getenv("MASIR_APP_NAME"); n != "" {
		// A second copy for development: its own tray, single-instance lock and data.
		name = n
	}
	mygo.App.SetName(name)
	if runtime.GOOS == "darwin" {
		// The panel is the app's only window: keep running as it closes.
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	}
	mygo.App.OnWindowAllClosed(func() {})
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}

	conn, d, err := client.Dial()
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	a := newApp(d, "")
	s := &shell{a: a}
	a.post = func(fn func()) {
		mygo.RunOnMain(func() {
			fn()
			s.sync()
		})
	}
	mygo.App.OnSecondInstance(func([]string, string) {
		if !s.open {
			s.show()
		}
	})
	mygo.App.WhenReady(func() {
		a.binary = daemonBinary()
		if v := mygo.App.Version(); v != "" { // the bundle's version, which is the app's
			a.version = v
		}
		s.ready()
		a.refreshLoginState()
		go a.watch(context.Background())
		// The panel is the app's greeting, unless the session started it: a login item
		// stays in the tray until the user asks for it. Without a tray icon, the window is
		// the only way in, so show it always.
		if !mygo.App.WasOpenedAtLogin() || s.tray == nil {
			time.AfterFunc(400*time.Millisecond, func() {
				mygo.RunOnMain(func() {
					if !s.open {
						s.show()
					}
				})
			})
		}
		// MASIR_DEMO=servers|rules|settings|add opens that part of the panel a moment
		// after launch, for watching the transitions when no one is there to click.
		if demo := os.Getenv("MASIR_DEMO"); demo != "" {
			time.AfterFunc(2500*time.Millisecond, func() {
				mygo.RunOnMain(func() {
					if !s.open {
						s.show()
					}
					switch demo {
					case "servers":
						a.open = sectionServers
					case "rules":
						a.open = sectionRules
					case "settings":
						a.openScreen(screenSettings)
					case "add":
						a.openScreen(screenAddSubscription)
					}
					s.sync()
				})
			})
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
