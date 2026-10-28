package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestPageResizeAtOnce checks that a page change asks for the new page's height in the
// same frame as the click, so the window and the pages move together, and then at most
// once more when everything has come to rest.
func TestPageResizeAtOnce(t *testing.T) {
	a := newTestApp(ukServers())
	a.motion.instant = false
	var asked []float32
	a.onHeight = func(h float32) { asked = append(asked, h) }
	tt := ui.NewTester(a.view, panelWidth, 900)
	tt.Frame()
	asked = nil

	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0] != a.subHeight() {
		t.Fatalf("opening settings asked for %v, want one request of %v", asked, a.subHeight())
	}
	for i := 0; i < 40; i++ {
		tt.Advance(8 * time.Millisecond)
		if p := a.motion.page.x; p > 0 && p < 1 && len(asked) > 1 {
			t.Fatalf("resized again while the page moved at %v: %v", p, asked)
		}
	}
	tt.Advance(time.Second)
	if len(asked) > 2 {
		t.Fatalf("a transition asked for the height %d times: %v", len(asked), asked)
	}

	asked = nil
	if err := tt.Click("Back"); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0] >= a.subHeight() {
		t.Fatalf("coming back asked for %v, want one request of the main page's height at once", asked)
	}
	for i := 0; i < 40; i++ {
		tt.Advance(8 * time.Millisecond)
		if p := a.motion.page.x; p > 0 && p < 1 && len(asked) > 1 {
			t.Fatalf("resized again while the page moved back at %v: %v", p, asked)
		}
	}
	tt.Advance(time.Second)
	if len(asked) > 2 {
		t.Fatalf("going back asked for the height %d times: %v", len(asked), asked)
	}
}

// TestListResizeOnlyAtRest is the same rule for the collapsible lists.
func TestListResizeOnlyAtRest(t *testing.T) {
	a := newTestApp(ukServers())
	a.motion.instant = false
	var asked []float32
	a.onHeight = func(h float32) { asked = append(asked, h) }
	tt := ui.NewTester(a.view, panelWidth, 900)
	tt.Frame()
	asked = nil

	if err := tt.Click(ltr("United Kingdom")); err != nil {
		t.Fatal(err)
	}
	if len(asked) != 1 || asked[0] <= a.baseHeight {
		t.Fatalf("opening the list asked for %v, want one taller request", asked)
	}
	for i := 0; i < 40; i++ {
		tt.Advance(8 * time.Millisecond)
		if x := a.motion.servers.x; x > 0 && x < 1 && len(asked) > 1 {
			t.Fatalf("resized while the list moved at %v: %v", x, asked)
		}
	}
	tt.Advance(time.Second)
	if len(asked) > 2 {
		t.Fatalf("opening the list asked for the height %d times: %v", len(asked), asked)
	}
	// The panel ends at the height the content measured, not the estimate it grew to.
	if last := asked[len(asked)-1]; last <= a.baseHeight {
		t.Fatalf("the list's height came to %v, no taller than the closed page %v", last, a.baseHeight)
	}
}

// TestListGivesItsHeightBackAtOnce checks that a folding list asks for the closed height
// as it starts, so the window shrinks with the list rather than a moment after it.
func TestListGivesItsHeightBackAtOnce(t *testing.T) {
	a := newTestApp(ukServers())
	a.motion.instant = false
	var asked []float32
	a.onHeight = func(h float32) { asked = append(asked, h) }
	tt := ui.NewTester(a.view, panelWidth, 900)
	tt.Frame()
	a.toggle(sectionServers)
	tt.Frame()
	tt.Advance(time.Second)
	asked = nil

	a.toggle(sectionServers) // close
	if len(asked) != 1 || asked[0] > a.baseHeight+0.5 {
		t.Fatalf("closing the list asked for %v at once, want the closed height %v", asked, a.baseHeight)
	}
	tt.Frame() // the frame that aims the springs at rest
	for i := 0; i < 40; i++ {
		tt.Advance(8 * time.Millisecond)
		for _, h := range asked {
			if h > a.baseHeight+0.5 {
				t.Fatalf("the window grew back while the list folded: %v", asked)
			}
		}
	}
	tt.Advance(time.Second)
	if len(asked) > 2 {
		t.Fatalf("closing the list asked for the height %d times: %v", len(asked), asked)
	}
}

// TestWindowHeightFollowsThePage checks that a window that follows the springs takes the
// page's height a frame at a time, so the window grows and shrinks with the pages instead
// of jumping to the end and stretching the old frame.
func TestWindowHeightFollowsThePage(t *testing.T) {
	a := newTestApp(ukServers())
	a.frameSize = true
	a.motion.instant = false
	var heights []float32
	a.onHeight = func(h float32) { heights = append(heights, h) }
	tt := ui.NewTester(a.view, panelWidth, 900)
	tt.Frame()
	mainHeight := a.restHeight()
	heights = nil

	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	if len(heights) != 0 {
		t.Fatalf("asked for %v at once; the window should follow the springs", heights)
	}
	for i := 0; i < 40; i++ {
		tt.Advance(8 * time.Millisecond)
	}
	tt.Advance(time.Second)
	if len(heights) < 5 {
		t.Fatalf("only %d height steps for the transition: %v", len(heights), heights)
	}
	if heights[0] >= a.subHeight()-20 {
		t.Fatalf("the first step is already the settings height: %v", heights)
	}
	prev := mainHeight
	for i, h := range heights {
		if h < prev-1 {
			t.Fatalf("height went back at step %d: %v after %v", i, h, prev)
		}
		prev = h
	}
	if last := heights[len(heights)-1]; abs32(last-a.subHeight()) > 2 {
		t.Fatalf("the window ended at %v, want %v", last, a.subHeight())
	}

	heights = nil
	if err := tt.Click("Back"); err != nil {
		t.Fatal(err)
	}
	if len(heights) != 0 {
		t.Fatalf("asked for %v at once on the way back", heights)
	}
	for i := 0; i < 40; i++ {
		tt.Advance(8 * time.Millisecond)
	}
	tt.Advance(time.Second)
	if len(heights) < 5 {
		t.Fatalf("only %d height steps coming back: %v", len(heights), heights)
	}
	if heights[0] <= mainHeight+20 {
		t.Fatalf("the first step back is already the main height: %v", heights)
	}
	prev = a.subHeight()
	for i, h := range heights {
		if h > prev+1 {
			t.Fatalf("height grew at step %d: %v after %v", i, h, prev)
		}
		prev = h
	}
	if last := heights[len(heights)-1]; abs32(last-mainHeight) > 2 {
		t.Fatalf("the window ended at %v, want %v", last, mainHeight)
	}
}

// TestWindowHeightInstantChange checks that a change with no animation still asks for the
// exact height on a window that follows the springs, so nothing is left unsized.
func TestWindowHeightInstantChange(t *testing.T) {
	a := newTestApp(ukServers()) // instant motion
	a.frameSize = true
	var asked []float32
	a.onHeight = func(h float32) { asked = append(asked, h) }
	tt := ui.NewTester(a.view, panelWidth, 900)
	tt.Frame()
	asked = nil
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(time.Second)
	if len(asked) != 1 || asked[0] != a.subHeight() {
		t.Fatalf("asked for %v, want one request of %v", asked, a.subHeight())
	}
}

// TestSpringsFollowTheFrameClock checks that the view moves the springs itself, one step
// per frame, instead of a timer of its own.
func TestSpringsFollowTheFrameClock(t *testing.T) {
	a := newTestApp(ukServers())
	a.motion.instant = false
	tt := ui.NewTester(a.view, panelWidth, 900)
	tt.Frame()
	if !a.motion.settled() {
		t.Fatal("the panel should open at rest")
	}
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(20 * time.Millisecond)
	if a.motion.page.x != 0 || !a.warmPage {
		t.Fatalf("the page started at %v before it was built unseen", a.motion.page.x)
	}
	tt.Advance(20 * time.Millisecond)
	first := a.motion.page.x
	if first <= 0 || first >= 1 {
		t.Fatalf("the page did not start moving in a frame: %v", first)
	}
	tt.Advance(20 * time.Millisecond)
	if second := a.motion.page.x; second <= first {
		t.Fatalf("the page did not move on: %v then %v", first, second)
	}
	frames := 0
	for i := 0; i < 100 && !a.motion.settled(); i++ {
		tt.Advance(20 * time.Millisecond)
		frames++
	}
	if frames > 60 {
		t.Fatalf("the page took %d frames to settle", frames)
	}
	if a.motion.page.x != 1 {
		t.Fatalf("the page settled at %v, not 1", a.motion.page.x)
	}
}

// Data that arrives while the panel is open must resize it at rest: the daemon's
// status, subscriptions, a banner or the update control can all change the height
// without any spring moving.
func TestHeightSettlesWhenDataArrivesAtRest(t *testing.T) {
	f := ukServers()
	a := newApp(f, "")
	a.async = func(fn func()) { fn() }
	a.motion.instant = true
	var asked []float32
	a.onHeight = func(h float32) { asked = append(asked, h) }
	tt := ui.NewTester(a.view, panelWidth, 900)
	tt.Frame()
	asked = nil

	a.applyStatus(f.status) // the first status: banner, server, update control
	tt.Frame()
	if len(asked) == 0 {
		t.Fatal("data arriving at rest did not resize the panel")
	}
	if last := asked[len(asked)-1]; abs32(last-a.restHeight()) > 0.5 {
		t.Fatalf("settled at %v, want %v", last, a.restHeight())
	}
}

// heightForOpen uses what the page really drew, not a headless tester's measurement.
func TestHeightForOpenUsesTheDrawnHeight(t *testing.T) {
	a := newTestApp(ukServers())
	tt := tester(a)
	tt.Frame()
	if a.mainHeight <= 0 {
		t.Fatal("the frame did not measure the main page")
	}
	if got := a.heightForOpen(); got != a.mainHeight {
		t.Fatalf("heightForOpen = %v, want the drawn %v", got, a.mainHeight)
	}
}
