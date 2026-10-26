// Masir is the menu bar app for the masir daemon: a tray icon whose panel turns masir on
// and off, picks the server, and shows the rules.
package main

import (
	"context"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"github.com/narixius/masir-go/libs/masir-core/client"
)

// version is set at build time: -X main.version=...
var version = "dev"

// shell holds what only exists with a running app: the tray and the panel.
type shell struct {
	a     *app
	tray  *mygo.Tray
	panel *mygo.Window // the open panel, or nil

	ticking bool      // the animation timer is running
	last    time.Time // when it last ticked
}

func (s *shell) toggle() {
	if s.panel != nil {
		s.panel.Close()
		return
	}
	height := int(math.Round(float64(s.a.measure())))
	s.panel = mygo.NewFlyout(mygo.FlyoutOptions{
		Tray:      s.tray,
		Placement: mygo.PlacementBottom,
		Width:     panelWidth,
		Height:    height,
		Focusable: true,
		Shadow:    true, // Windows draws the flyout's shadow; macOS's popover has its own
		// A real NSPopover: the system draws its material, arrow and shadow, animates it, and
		// highlights the tray icon while it shows.
		Popover: true,
		Content: ui.View(s.a.view),
	})
	s.a.screen, s.a.open = screenMain, sectionNone
	s.a.motion = motion{instant: s.a.motion.instant} // a new panel starts at rest
	s.a.onHeight = s.setHeight
	s.a.onAnimate = s.kick
	s.a.watchTraffic()
	s.panel.OnClosed(func() {
		s.panel = nil
		s.a.stopTraffic()
	})
}

// setHeight follows the content's height, frame by frame, so the window grows and
// shrinks with the panels that open in it.
func (s *shell) setHeight(h float32) {
	if s.panel == nil || h <= 0 {
		return
	}
	want := int(math.Round(float64(h)))
	// The popover's window is its content and the arrow's room around it, so the content is
	// what is compared and set, at the panel's own width.
	if _, cur := s.panel.ContentSize(); cur != want {
		s.panel.SetSize(panelWidth, want)
	}
}

// kick runs the animation timer if it isn't running. Each tick moves the springs, sets the
// window's height from them, and only then asks for the frame that draws them, so the
// window and its content are never a frame apart.
func (s *shell) kick() {
	if s.ticking {
		return
	}
	s.ticking, s.last = true, time.Now()
	s.schedule()
}

func (s *shell) schedule() {
	time.AfterFunc(8*time.Millisecond, func() { mygo.RunOnMain(s.tick) })
}

func (s *shell) tick() {
	now := time.Now()
	dt := float32(min(now.Sub(s.last).Seconds(), 0.05))
	s.last = now
	moving := s.a.motion.step(dt)
	s.setHeight(s.a.windowHeight())
	if s.panel != nil {
		s.panel.Invalidate()
	}
	if moving && s.panel != nil {
		s.schedule()
		return
	}
	s.ticking = false
}

// sync follows the state: the panel's redraw and the tray's icon and tooltip.
func (s *shell) sync() {
	if s.panel != nil {
		s.panel.Invalidate()
	}
	s.a.updateTray(s.tray)
}

func (s *shell) ready() {
	icon, err := trayIcon(false)
	if err != nil {
		log.Fatal("tray icon: ", err)
	}
	s.tray, err = mygo.NewTray(mygo.TrayOptions{ID: "masir", Icon: icon, IconIsTemplate: true, ToolTip: "Masir"})
	if err != nil {
		log.Fatal("tray: ", err)
	}
	menu := mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Open Masir", Click: func(*mygo.MenuItem, *mygo.Window) { s.toggle() }},
		mygo.Separator(),
		{Role: mygo.RoleQuit},
	})
	if runtime.GOOS == "linux" {
		s.tray.SetMenu(menu) // AppIndicator tells nothing of clicks
	} else {
		s.tray.OnClick(s.toggle)
		s.tray.OnRightClick(func() {
			if s.panel != nil {
				s.panel.Close()
			}
			s.tray.SetMenu(menu) // for this popup only, so clicks keep toggling the panel
			defer s.tray.SetMenu(nil)
			s.tray.PopUpMenu()
		})
	}
	// The popover closes by itself as the user clicks elsewhere, but not reliably when the app
	// was opened from Finder and is active: another app taking the focus must close it too.
	mygo.App.OnDidResignActive(func() {
		if s.panel != nil {
			s.panel.Close()
		}
	})
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { s.tray.Destroy() })
}

// daemonBinary is the masir executable the app starts a daemon from: the one shipped
// in the app, MASIR_BIN, or masir on the PATH.
func daemonBinary() string {
	if p := os.Getenv("MASIR_BIN"); p != "" {
		return p
	}
	name := "masir"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if dir, err := mygo.App.Path(mygo.PathResources); err == nil {
		// In bin: on Windows a file called masir.exe would be the app's own Masir.exe.
		for _, p := range []string{filepath.Join(dir, "bin", name), filepath.Join(dir, name)} {
			if fileExists(p) {
				return p
			}
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return ""
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func main() {
	mygo.App.SetName("Masir")
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	// The panel is the app's only window: keep running as it closes.
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
		if s.panel == nil {
			s.toggle()
		}
	})
	mygo.App.WhenReady(func() {
		a.binary = daemonBinary()
		if v := mygo.App.Version(); v != "" { // the bundle's version, which is the app's
			a.version = v
		}
		s.ready()
		go a.watch(context.Background())
		// MASIR_DEMO=servers|rules|settings|add opens that part of the panel a moment
		// after launch, for watching the transitions when no one is there to click.
		if demo := os.Getenv("MASIR_DEMO"); demo != "" {
			time.AfterFunc(2500*time.Millisecond, func() {
				mygo.RunOnMain(func() {
					if s.panel == nil {
						s.toggle()
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
		// Show the panel once at launch, as the app's greeting, when the tray has settled.
		time.AfterFunc(400*time.Millisecond, func() {
			mygo.RunOnMain(func() {
				if s.panel == nil {
					s.toggle()
				}
			})
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
