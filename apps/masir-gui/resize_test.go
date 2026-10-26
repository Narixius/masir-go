package main

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// TestResizeOnlyAtRest checks the rule that keeps the panel smooth: a transition asks for
// the window's height once before it starts and once when it comes to rest, and never
// while the pages move. It is the regression test for the popover freeze.
func TestResizeOnlyAtRest(t *testing.T) {
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
			t.Fatalf("resized while the page moved at %v: %v", p, asked)
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
		t.Fatalf("coming back asked for %v, want one request of the main page's height", asked)
	}
	for i := 0; i < 40; i++ {
		tt.Advance(8 * time.Millisecond)
		if p := a.motion.page.x; p > 0 && p < 1 && len(asked) > 1 {
			t.Fatalf("resized while the page moved back at %v: %v", p, asked)
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

	if err := tt.Click(ltr("🇬🇧 United Kingdom")); err != nil {
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
