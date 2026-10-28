package main

import (
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The first open of a process has no drawn height of its own: it opens at the height the
// launch before remembered, so it doesn't resize a moment after it appears.
func TestHeightForOpenUsesTheRememberedHeight(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel-height")
	if err := os.WriteFile(path, []byte("318\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := newTestApp(ukServers())
	a.heightFile = path
	a.loadPanelHeight()
	if got := a.heightForOpen(); got != 318 {
		t.Fatalf("heightForOpen = %v, want the remembered 318", got)
	}
}

func TestRememberedHeightIgnoresWhatItCannotTrust(t *testing.T) {
	for _, text := range []string{"", "tall", "-4", "0", "99999", "NaN", "Inf"} {
		t.Run(text, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "panel-height")
			if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			a := newTestApp(ukServers())
			a.heightFile = path
			a.loadPanelHeight()
			if a.baseHeight != 0 {
				t.Fatalf("%q was trusted as %v", text, a.baseHeight)
			}
			if got := a.heightForOpen(); got != defaultPanelHeight {
				t.Fatalf("heightForOpen = %v, want the default %v", got, defaultPanelHeight)
			}
		})
	}
}

// A new launch opens at the height the previous one drew, and the first frame that draws
// the same page asks for no resize.
func TestRememberedHeightSizesTheFirstFrame(t *testing.T) {
	drawn := newTestApp(ukServers())
	tester(drawn).Frame()
	if drawn.baseHeight <= 0 {
		t.Fatal("the frame did not measure the main page")
	}
	path := filepath.Join(t.TempDir(), "panel-height")
	value := strconv.FormatFloat(float64(drawn.baseHeight), 'f', -1, 32)
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}

	next := newTestApp(ukServers())
	next.heightFile = path
	next.loadPanelHeight()
	next.askedHeight = next.heightForOpen()
	var asked []float32
	next.onHeight = func(h float32) { asked = append(asked, h) }
	tester(next).Frame()
	if len(asked) != 0 {
		t.Fatalf("the first frame asked for %v, want no resize", asked)
	}
}

// A panel hidden on Settings, or with a list open, must open at the main page's height:
// start resets the page before the height is asked for.
func TestOpeningResetsToTheMainPageBeforeSizing(t *testing.T) {
	a := newTestApp(ukServers())
	tester(a).Frame()
	closed := a.baseHeight
	if closed <= 0 {
		t.Fatal("the frame did not measure the main page")
	}

	a.screen, a.open = screenSettings, sectionServers
	a.measuredOpen, a.mainHeight = sectionServers, 700
	a.resetForOpen()
	if got := a.heightForOpen(); got != closed {
		t.Fatalf("heightForOpen = %v, want the main page's %v", got, closed)
	}
	if a.screen != screenMain || a.open != sectionNone {
		t.Fatalf("reset left screen %v, section %v", a.screen, a.open)
	}
}

// The height the main page draws is written for the next launch, off the frame.
func TestTheDrawnHeightIsRemembered(t *testing.T) {
	a := newTestApp(ukServers())
	path := filepath.Join(t.TempDir(), "panel-height")
	a.heightFile = path
	tester(a).Frame()
	if a.baseHeight <= 0 {
		t.Fatal("the frame did not measure the main page")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 32)
	if err != nil {
		t.Fatalf("%q is not a height: %v", b, err)
	}
	if math.Abs(float64(a.baseHeight)-h) > 0.01 {
		t.Fatalf("remembered %v, want the drawn %v", h, a.baseHeight)
	}
}

// A write still in flight when the app quits must not overwrite the final height.
func TestQuitHeightWinsOverAPendingWrite(t *testing.T) {
	a := newTestApp(ukServers())
	path := filepath.Join(t.TempDir(), "panel-height")
	a.heightFile = path
	var pending []func()
	a.async = func(fn func()) { pending = append(pending, fn) }

	a.rememberPanelHeight(300) // scheduled, not yet written
	a.baseHeight = 310
	a.flushPanelHeight()
	for _, fn := range pending {
		fn()
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); got != "310" {
		t.Fatalf("remembered %q, want the height at quit 310", got)
	}
}
