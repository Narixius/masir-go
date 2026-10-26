package main

import (
	"testing"
	"time"
)

// run feeds scroll events at a rate, then lets the fingers lift and the row settle.
func run(s *swipe, start time.Time, dxs []float32, every time.Duration) time.Time {
	now := start
	for _, dx := range dxs {
		s.scroll(dx, 0, now)
		now = now.Add(every)
		s.advance(now)
	}
	for i := 0; i < 400 && s.advance(now); i++ {
		now = now.Add(5 * time.Millisecond)
	}
	return now
}

func TestSwipingLeftFarEnoughOpens(t *testing.T) {
	var s swipe
	run(&s, time.Now(), []float32{-12, -14, -16, -12, -10, -8, -6}, 16*time.Millisecond)
	if s.x != -swipeWidth || !s.open {
		t.Fatalf("x = %v open = %v", s.x, s.open)
	}
}

func TestShortSlowSwipeSpringsBack(t *testing.T) {
	var s swipe
	run(&s, time.Now(), []float32{-3, -4, -3}, 40*time.Millisecond)
	if s.x != 0 || s.open {
		t.Fatalf("x = %v open = %v", s.x, s.open)
	}
}

func TestFastFlickOpensEvenWhenShort(t *testing.T) {
	var s swipe
	run(&s, time.Now(), []float32{-14, -16, -14}, 8*time.Millisecond) // ~40 units in 16ms
	if !s.open {
		t.Fatalf("a flick left did not open: x = %v", s.x)
	}
}

func TestRowFollowsFingersAndResistsPastTheEnds(t *testing.T) {
	var s swipe
	now := time.Now()
	s.scroll(-20, 0, now)
	if s.x != -20*swipeSensitivity {
		t.Fatalf("1:1 tracking: x = %v", s.x)
	}
	for i := 0; i < 20; i++ {
		now = now.Add(8 * time.Millisecond)
		s.scroll(-30, 0, now)
	}
	if s.x >= -swipeWidth || s.x < -swipeWidth-28 {
		t.Fatalf("past the end it should resist, but within the band: x = %v", s.x)
	}
}

func TestVerticalScrollingIsLeftToTheList(t *testing.T) {
	var s swipe
	if s.scroll(-1, 20, time.Now()) {
		t.Fatal("a vertical scroll was taken")
	}
	if s.mode != swipeIdle {
		t.Fatal("it began tracking")
	}
}

func TestRightSwipeWhenClosedIsIgnored(t *testing.T) {
	var s swipe
	if s.scroll(8, 0, time.Now()) {
		t.Fatal("a swipe right on a closed row was taken")
	}
}

func TestSwipingRightClosesAnOpenRow(t *testing.T) {
	var s swipe
	end := run(&s, time.Now(), []float32{-30, -30, -20}, 16*time.Millisecond)
	if !s.open {
		t.Fatal("did not open first")
	}
	run(&s, end.Add(time.Second), []float32{20, 24, 24, 20}, 16*time.Millisecond)
	if s.open || s.x != 0 {
		t.Fatalf("x = %v open = %v", s.x, s.open)
	}
}

func TestTrailingInertiaDoesNotFightTheSettle(t *testing.T) {
	var s swipe
	now := time.Now()
	for _, dx := range []float32{-20, -20, -20} {
		s.scroll(dx, 0, now)
		now = now.Add(10 * time.Millisecond)
	}
	now = now.Add(60 * time.Millisecond)
	s.advance(now) // the fingers are up: it settles open
	if s.mode != swipeSettling || !s.open {
		t.Fatalf("mode = %v open = %v", s.mode, s.open)
	}
	for i := 0; i < 5; i++ { // inertia keeps arriving, in the same direction
		if !s.scroll(-2, 0, now) {
			t.Fatal("inertia was not absorbed")
		}
		now = now.Add(10 * time.Millisecond)
	}
	for i := 0; i < 300 && s.advance(now); i++ {
		now = now.Add(5 * time.Millisecond)
	}
	if s.x != -swipeWidth || s.mode != swipeIdle {
		t.Fatalf("x = %v mode = %v", s.x, s.mode)
	}
}

func TestSettlingNeverOvershootsTheTarget(t *testing.T) {
	var s swipe
	s.x = -20
	s.settle(true, 0, time.Now())
	now := time.Now()
	for i := 0; i < 400; i++ {
		now = now.Add(2 * time.Millisecond)
		s.advance(now)
		if s.x < -swipeWidth-0.01 {
			t.Fatalf("overshot to %v", s.x)
		}
	}
}

func TestButtonsComeInWithTheRow(t *testing.T) {
	var s swipe
	if s.buttonsOpacity() != 0 || s.buttonsX() != swipeWidth {
		t.Fatal("buttons are not hidden when closed")
	}
	s.x = -5
	if s.buttonsOpacity() != 0.5 {
		t.Fatalf("opacity = %v", s.buttonsOpacity())
	}
	s.x = -swipeWidth
	if s.buttonsOpacity() != 1 || s.buttonsX() != 0 {
		t.Fatal("buttons are not in place when open")
	}
}
